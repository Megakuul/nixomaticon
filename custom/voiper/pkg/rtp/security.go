package rtp

import (
	"bytes"
	"errors"

	"github.com/pion/srtp/v3"
)

// ConfigureSRTP installs SDES AES_CM_128_HMAC_SHA1_80 keys (16-byte key and
// 14-byte salt). Signaling must authenticate and protect their exchange.
func (s *Session) ConfigureSRTP(local, remote []byte) error {
	return s.configureSRTP(local, remote, srtp.ProtectionProfileAes128CmHmacSha1_80)
}

func (s *Session) configureSRTP(local, remote []byte, profile srtp.ProtectionProfile) error {
	saltLength := 14
	if profile == srtp.ProtectionProfileAeadAes128Gcm {
		saltLength = 12
	}
	if len(local) != 16+saltLength || len(remote) != 16+saltLength {
		return errors.New("invalid SRTP master key and salt length")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("RTP session is closed")
	}
	if s.protectionProfile != 0 && s.protectionProfile != profile {
		return errors.New("changing SRTP profile during a call is unsupported")
	}
	if bytes.Equal(s.localKey, local) && bytes.Equal(s.remoteKey, remote) {
		return nil
	}
	outgoing := s.outgoing
	if !bytes.Equal(s.localKey, local) {
		var err error
		outgoing, err = srtp.CreateContext(local[:16], local[16:], profile)
		if err != nil {
			return err
		}
	}
	incoming, err := srtp.CreateContext(remote[:16], remote[16:], profile, srtp.SRTPReplayProtection(128), srtp.SRTCPReplayProtection(128))
	if err != nil {
		return err
	}
	s.protectionProfile = profile
	s.outgoing = outgoing
	s.incoming = incoming
	s.controlSeen = false
	s.localKey = append([]byte(nil), local...)
	s.remoteKey = append([]byte(nil), remote...)
	return nil
}
