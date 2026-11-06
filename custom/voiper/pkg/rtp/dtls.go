package rtp

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync/atomic"
	"time"

	"github.com/pion/dtls/v3"
	"github.com/pion/dtls/v3/pkg/crypto/selfsign"
	"github.com/pion/srtp/v3"
	"github.com/pion/transport/v5/packetio"
)

type dtlsTransport struct {
	certificate               tls.Certificate
	fingerprint               string
	remoteFingerprint         string
	active, connecting, ready bool
	failure                   string
	socket                    *dtlsSocket
	conn                      *dtls.Conn
}

// NormalizeDTLSFingerprint accepts the SHA-256 certificate identity carried by SDP.
func NormalizeDTLSFingerprint(value string) (string, error) {
	fields := strings.Fields(value)
	if len(fields) != 2 || !strings.EqualFold(fields[0], "sha-256") {
		return "", errors.New("DTLS requires a SHA-256 fingerprint")
	}
	octets := strings.Split(fields[1], ":")
	if len(octets) != sha256.Size {
		return "", errors.New("DTLS fingerprint must contain 32 octets")
	}
	for _, octet := range octets {
		if len(octet) != 2 {
			return "", errors.New("invalid DTLS fingerprint octet")
		}
		if _, err := hex.DecodeString(octet); err != nil {
			return "", errors.New("invalid DTLS fingerprint hex")
		}
	}
	return "sha-256 " + strings.ToUpper(fields[1]), nil
}
func certificateFingerprint(raw []byte) string {
	digest := sha256.Sum256(raw)
	octets := make([]string, len(digest))
	for i, b := range digest {
		octets[i] = fmt.Sprintf("%02X", b)
	}
	return "sha-256 " + strings.Join(octets, ":")
}

// PrepareDTLS creates a per-call identity and blocks plaintext RTP until the authenticated handshake completes.
func (s *Session) PrepareDTLS() (string, error) {
	certificate, err := selfsign.GenerateSelfSigned()
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return "", net.ErrClosed
	}
	if s.dtls != nil {
		return s.dtls.fingerprint, nil
	}
	if s.outgoing != nil || s.incoming != nil {
		return "", errors.New("cannot replace an existing SRTP key exchange")
	}
	buffer := packetio.NewBuffer()
	buffer.SetLimitCount(128)
	buffer.SetLimitSize(256 * 1024)
	s.dtls = &dtlsTransport{certificate: certificate, fingerprint: certificateFingerprint(certificate.Certificate[0]), socket: &dtlsSocket{session: s, packets: buffer}}
	return s.dtls.fingerprint, nil
}

func (s *Session) ConnectDTLS(ctx context.Context, fingerprint string, active bool) (result error) {
	fingerprint, err := NormalizeDTLSFingerprint(fingerprint)
	if err != nil {
		return err
	}
	s.mu.Lock()
	transport := s.dtls
	if s.closed || transport == nil || s.remote == nil || !s.mux {
		s.mu.Unlock()
		return errors.New("DTLS requires a configured peer and RTCP multiplexing")
	}
	if transport.ready {
		same := transport.remoteFingerprint == fingerprint && transport.active == active
		s.mu.Unlock()
		if !same {
			return errors.New("changing the DTLS identity or setup role requires a new call")
		}
		return nil
	}
	if transport.connecting || transport.conn != nil {
		s.mu.Unlock()
		return errors.New("DTLS handshake is already started")
	}
	transport.connecting = true
	transport.active = active
	transport.remoteFingerprint = fingerprint
	transport.socket.remote = *s.remote
	s.workers.Add(1)
	s.mu.Unlock()
	defer s.workers.Done()
	options := []dtls.Option{
		dtls.WithCertificates(transport.certificate),
		dtls.WithSRTPProtectionProfiles(dtls.SRTP_AEAD_AES_128_GCM, dtls.SRTP_AES128_CM_HMAC_SHA1_80),
		dtls.WithExtendedMasterSecret(dtls.RequireExtendedMasterSecret),
		// SDP supplies the authenticated self-signed certificate identity instead of a public CA.
		dtls.WithInsecureSkipVerify(true),
		dtls.WithVerifyPeerCertificate(func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) == 0 || subtle.ConstantTimeCompare([]byte(certificateFingerprint(rawCerts[0])), []byte(fingerprint)) != 1 {
				return errors.New("DTLS peer certificate does not match the SDP fingerprint")
			}
			return nil
		}),
	}
	var conn *dtls.Conn
	if active {
		clientOptions := make([]dtls.ClientOption, len(options))
		for i, option := range options {
			clientOptions[i] = option
		}
		conn, err = dtls.ClientWithOptions(transport.socket, &transport.socket.remote, clientOptions...)
	} else {
		serverOptions := make([]dtls.ServerOption, 0, len(options)+1)
		for _, option := range options {
			serverOptions = append(serverOptions, option)
		}
		serverOptions = append(serverOptions, dtls.WithClientAuth(dtls.RequireAnyClientCert))
		conn, err = dtls.ServerWithOptions(transport.socket, &transport.socket.remote, serverOptions...)
	}
	if err != nil {
		transport.socket.Close()
		return err
	}
	defer func() {
		if result != nil {
			transport.socket.Close()
			conn.Close()
		}
	}()
	s.mu.Lock()
	transport.conn = conn
	closed := s.closed
	s.mu.Unlock()
	if closed {
		transport.socket.Close()
		conn.Close()
		return net.ErrClosed
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err = conn.HandshakeContext(ctx); err != nil {
		transport.socket.Close()
		conn.Close()
		return fmt.Errorf("DTLS handshake failed: %w", err)
	}
	protection, ok := conn.SelectedSRTPProtectionProfile()
	if !ok {
		transport.socket.Close()
		conn.Close()
		return errors.New("DTLS did not negotiate SRTP")
	}
	profile := srtp.ProtectionProfileAes128CmHmacSha1_80
	if protection == dtls.SRTP_AEAD_AES_128_GCM {
		profile = srtp.ProtectionProfileAeadAes128Gcm
	} else if protection != dtls.SRTP_AES128_CM_HMAC_SHA1_80 {
		transport.socket.Close()
		conn.Close()
		return errors.New("unsupported DTLS-SRTP protection profile")
	}
	state, ok := conn.ConnectionState()
	if !ok {
		return errors.New("DTLS handshake has no connection state")
	}
	keys := srtp.Config{Profile: profile}
	if err = keys.ExtractSessionKeysFromDTLS(&state, active); err != nil {
		return fmt.Errorf("export DTLS-SRTP keys: %w", err)
	}
	local := append(keys.Keys.LocalMasterKey, keys.Keys.LocalMasterSalt...)
	remote := append(keys.Keys.RemoteMasterKey, keys.Keys.RemoteMasterSalt...)
	if err = s.configureSRTP(local, remote, profile); err != nil {
		return err
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return net.ErrClosed
	}
	transport.ready = true
	transport.connecting = false
	s.workers.Add(1)
	s.mu.Unlock()
	go s.monitorDTLS(transport)
	return nil
}

func (s *Session) monitorDTLS(transport *dtlsTransport) {
	defer s.workers.Done()
	data := make([]byte, 2048)
	for {
		if _, err := transport.conn.Read(data); err != nil {
			s.mu.Lock()
			if !s.closed {
				transport.ready = false
				transport.failure = fmt.Sprintf("DTLS association closed: %v", err)
			}
			s.mu.Unlock()
			return
		}
	}
}

func (s *Session) receiveDTLS(data []byte, from *net.UDPAddr, trusted bool) bool {
	if len(data) == 0 || data[0] < 20 || data[0] > 63 {
		return false
	}
	s.mu.Lock()
	transport := s.dtls
	allowed := trusted || (s.iceConn == nil && sameAddress(from, s.remote))
	s.mu.Unlock()
	if transport != nil && allowed {
		_, _ = transport.socket.packets.Write(data, nil)
	}
	return true
}

type dtlsSocket struct {
	session       *Session
	packets       *packetio.Buffer
	remote        net.UDPAddr
	closed        atomic.Bool
	writeDeadline atomic.Int64
}

func (s *dtlsSocket) ReadFrom(data []byte) (int, net.Addr, error) {
	n, _, err := s.packets.Read(data, nil)
	return n, &s.remote, err
}
func (s *dtlsSocket) WriteTo(data []byte, _ net.Addr) (int, error) {
	if s.closed.Load() {
		return 0, net.ErrClosed
	}
	deadline := time.Now().Add(200 * time.Millisecond)
	if value := s.writeDeadline.Load(); value != 0 {
		if requested := time.Unix(0, value); requested.Before(deadline) {
			deadline = requested
		}
	}
	session := s.session
	session.mu.Lock()
	remote, transport := session.remote, session.iceConn
	session.mu.Unlock()
	return session.writeWithDeadline(data, remote, transport, false, deadline)
}
func (s *dtlsSocket) Close() error                      { s.closed.Store(true); return s.packets.Close() }
func (s *dtlsSocket) LocalAddr() net.Addr               { return s.session.LocalAddr() }
func (s *dtlsSocket) SetReadDeadline(t time.Time) error { return s.packets.SetReadDeadline(t) }
func (s *dtlsSocket) SetWriteDeadline(t time.Time) error {
	value := int64(0)
	if !t.IsZero() {
		value = t.UnixNano()
	}
	s.writeDeadline.Store(value)
	return nil
}
func (s *dtlsSocket) SetDeadline(t time.Time) error {
	s.SetWriteDeadline(t)
	return s.SetReadDeadline(t)
}
