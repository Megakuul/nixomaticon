// Package rtp owns bounded RTP/RTCP sessions; Pion provides packet encoding.
package rtp

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"net"
	"sync"
	"time"

	"github.com/pion/rtcp"
	pion "github.com/pion/rtp"
	"github.com/pion/srtp/v3"
)

type Stats struct {
	LastSent, LastReceived                                 time.Time
	ReceiverReportAt                                       time.Time
	RemoteFractionLost                                     uint8
	RemoteLastSequenceNumber                               uint32
	DTLSError                                              string
	Encrypted                                              bool
	ICEState, ICECandidateType                             string
	RTCPMux                                                bool
	PacketsSent, PacketsReceived, BytesSent, BytesReceived uint64
	PacketsLost, PacketsDropped                            uint64
	JitterMilliseconds, RTTMilliseconds                    float64
	LocalAddress, RemoteAddress                            string
}

type Session struct {
	symmetric                    bool
	controlSource, controlIndex  uint32
	controlSeen                  bool
	dtls                         *dtlsTransport
	protectionProfile            srtp.ProtectionProfile
	senderPackets                uint32
	resetTransit                 bool
	ice                          *iceTransport
	iceGathering                 bool
	restart                      *ICERestart
	iceConn                      net.Conn
	closed                       bool
	mux                          bool
	writeMu                      sync.Mutex
	expectedPrior, receivedPrior uint64
	sentReports                  map[uint32]time.Time
	sourceCanChange              bool
	sourcePackets, previousLost  uint64
	outgoing, incoming           *srtp.Context
	localKey, remoteKey          []byte
	payloadBytes                 uint64
	conn, control                *net.UDPConn
	packets                      chan *pion.Packet
	cancel                       context.CancelFunc
	workers                      sync.WaitGroup
	closeOnce                    sync.Once
	mu                           sync.Mutex
	remote, remoteControl        *net.UDPAddr
	clock                        int
	ssrc                         uint32
	sequence                     uint16
	timestamp                    uint32
	stats                        Stats
	received                     bool
	highest                      uint32
	first                        uint32
	seen                         uint64
	source                       uint32
	jitter                       float64
	transit                      int64
	lastSR                       uint32
	lastSRAt                     time.Time
}

func New(ctx context.Context, bind string, clockRate int) (*Session, error) {
	if clockRate <= 0 {
		return nil, errors.New("RTP clock rate must be positive")
	}
	if bind == "" {
		bind = "0.0.0.0:0"
	}
	addr, err := net.ResolveUDPAddr("udp", bind)
	if err != nil {
		return nil, err
	}
	var conn, control *net.UDPConn
	for attempt := 0; attempt < 12; attempt++ {
		conn, err = net.ListenUDP("udp", addr)
		if err != nil {
			return nil, err
		}
		local := conn.LocalAddr().(*net.UDPAddr)
		if local.Port < 65535 {
			control, err = net.ListenUDP("udp", &net.UDPAddr{IP: local.IP, Port: local.Port + 1, Zone: local.Zone})
		} else {
			err = errors.New("no adjacent RTCP port")
		}
		if err == nil {
			break
		}
		_ = conn.Close()
		if addr.Port != 0 {
			return nil, fmt.Errorf("bind RTCP: %w", err)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("allocate RTP/RTCP ports: %w", err)
	}
	var seed [10]byte
	if _, err = rand.Read(seed[:]); err != nil {
		conn.Close()
		control.Close()
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	s := &Session{sentReports: make(map[uint32]time.Time), conn: conn, control: control, packets: make(chan *pion.Packet, 128), cancel: cancel, clock: clockRate, ssrc: binary.BigEndian.Uint32(seed[:4]), sequence: binary.BigEndian.Uint16(seed[4:6]), timestamp: binary.BigEndian.Uint32(seed[6:]), stats: Stats{LocalAddress: conn.LocalAddr().String()}}
	s.workers.Add(3)
	go s.receive()
	go s.receiveControl()
	go s.report(ctx)
	go func() { <-ctx.Done(); s.Close() }()
	return s, nil
}
func (s *Session) LocalAddr() *net.UDPAddr      { return s.conn.LocalAddr().(*net.UDPAddr) }
func (s *Session) ControlAddr() *net.UDPAddr    { return s.control.LocalAddr().(*net.UDPAddr) }
func (s *Session) Packets() <-chan *pion.Packet { return s.packets }
func (s *Session) SetRemote(media, control *net.UDPAddr, clockRate int) error {
	if media == nil || media.IP == nil || media.Port <= 0 || media.Port > 65535 || media.IP.IsMulticast() {
		return errors.New("invalid remote RTP address")
	}
	if control == nil {
		if media.Port == 65535 {
			return errors.New("no remote RTCP port")
		}
		control = &net.UDPAddr{IP: media.IP, Port: media.Port + 1}
	}
	if control.Port <= 0 || control.Port > 65535 || control.IP == nil || control.IP.IsMulticast() {
		return errors.New("invalid remote RTCP address")
	}
	if clockRate <= 0 {
		return errors.New("invalid RTP clock rate")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.clock != clockRate {
		var seed [10]byte
		if _, err := rand.Read(seed[:]); err != nil {
			return err
		}
		nextSource := binary.BigEndian.Uint32(seed[:4])
		if nextSource == s.ssrc {
			nextSource++
		}
		s.ssrc = nextSource
		s.sequence = binary.BigEndian.Uint16(seed[4:6])
		s.timestamp = binary.BigEndian.Uint32(seed[6:])
		s.senderPackets = 0
		s.payloadBytes = 0
		clear(s.sentReports)
		s.stats.ReceiverReportAt = time.Time{}
		s.stats.RemoteFractionLost = 0
		s.stats.RemoteLastSequenceNumber = 0
		s.jitter = 0
		s.resetTransit = true
	}
	s.sourceCanChange = true
	s.remote = media
	s.remoteControl = control
	s.clock = clockRate
	if s.iceConn == nil {
		s.stats.RemoteAddress = media.String()
	}
	return nil
}
func (s *Session) Send(payloadType uint8, payload []byte, samples uint32, marker bool) error {
	s.mu.Lock()
	if s.dtls != nil && !s.dtls.ready {
		s.mu.Unlock()
		return errors.New("DTLS-SRTP handshake is not complete")
	}
	if s.remote == nil {
		s.mu.Unlock()
		return errors.New("remote RTP address is not configured")
	}
	packet := pion.Packet{Header: pion.Header{Version: 2, PayloadType: payloadType, SequenceNumber: s.sequence, Timestamp: s.timestamp, SSRC: s.ssrc, Marker: marker}, Payload: payload}
	data, err := packet.Marshal()
	if err == nil && s.outgoing != nil {
		data, err = s.outgoing.EncryptRTP(nil, data, nil)
	}
	if err != nil {
		s.mu.Unlock()
		return err
	}
	s.sequence++
	s.timestamp += samples
	remote := s.remote
	transport := s.iceConn
	s.mu.Unlock()
	n, err := s.write(data, remote, transport, false)
	if err == nil {
		s.mu.Lock()
		s.senderPackets++
		s.payloadBytes += uint64(len(payload))
		s.stats.PacketsSent++
		s.stats.LastSent = time.Now()
		s.stats.BytesSent += uint64(n)
		s.mu.Unlock()
	}
	return err
}
func (s *Session) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	stats := s.stats
	stats.Encrypted = s.incoming != nil && s.outgoing != nil
	if s.dtls != nil {
		stats.Encrypted = stats.Encrypted && s.dtls.ready
		stats.DTLSError = s.dtls.failure
	}
	if s.received {
		expected := uint64(s.highest-s.first) + 1
		if expected > s.sourcePackets {
			stats.PacketsLost = expected - s.sourcePackets
		}
	}
	stats.PacketsLost += s.previousLost
	stats.JitterMilliseconds = s.jitter * 1000 / float64(s.clock)
	return stats
}
func (s *Session) receive() {
	defer s.workers.Done()
	data := make([]byte, 65536)
	for {
		n, from, err := s.conn.ReadFromUDP(data)
		if err != nil {
			return
		}
		s.mu.Lock()
		transport := s.ice
		active := s.iceConn != nil
		mux := s.mux
		s.mu.Unlock()
		if transport != nil && (active || isSTUN(data[:n])) {
			transport.socket.deliver(data[:n], from)
			continue
		}
		if s.receiveDTLS(data[:n], from, false) {
			continue
		}
		if mux && isRTCP(data[:n]) {
			s.handleControl(data[:n], from, false)
		} else {
			s.handleRTP(data[:n], from, false)
		}
	}
}
func (s *Session) handleRTP(data []byte, from *net.UDPAddr, trusted bool) {
	s.mu.Lock()
	rebinding := !trusted && !sameAddress(from, s.remote) && s.canRebind(from, s.remote)
	allowed := (trusted || (s.iceConn == nil && sameAddress(from, s.remote)) || rebinding) && (s.dtls == nil || s.dtls.ready)
	plaintext := data
	var err error
	if allowed && s.incoming != nil {
		plaintext, err = s.incoming.DecryptRTP(nil, data, nil)
		if err != nil {
			allowed = false
			s.stats.PacketsDropped++
		}
	}
	if !allowed {
		s.mu.Unlock()
		return
	}
	packet := new(pion.Packet)
	if err = packet.Unmarshal(plaintext); err != nil || packet.Version != 2 || len(packet.Payload) == 0 {
		s.mu.Unlock()
		return
	}
	// A delayed packet from the previous mapping must not move the return path
	// backwards. A new SSRC must first arrive on the currently selected path.
	if rebinding && s.received && (packet.SSRC != s.source || int16(packet.SequenceNumber-uint16(s.highest)) <= 0) {
		s.mu.Unlock()
		return
	}
	if !s.track(packet, len(data), time.Now()) {
		s.mu.Unlock()
		return
	}
	if rebinding {
		s.remote = from
		s.stats.RemoteAddress = from.String()
		if s.mux {
			s.remoteControl = from
		}
	}
	packet.Payload = append([]byte(nil), packet.Payload...)
	s.mu.Unlock()
	select {
	case s.packets <- packet:
	default:
		s.mu.Lock()
		s.stats.PacketsDropped++
		s.mu.Unlock()
	}
}

// SetSymmetricRTP permits authenticated SDES-SRTP port changes on the signaled
// IP. It never learns plain RTP peers, changes IP, or overrides ICE/DTLS paths.
func (s *Session) SetSymmetricRTP(enabled bool) {
	s.mu.Lock()
	s.symmetric = enabled
	s.mu.Unlock()
}

func (s *Session) canRebind(from, remote *net.UDPAddr) bool {
	return s.symmetric && s.protectionProfile == srtp.ProtectionProfileAes128CmHmacSha1_80 && s.incoming != nil && s.dtls == nil && s.ice == nil && s.iceConn == nil && from != nil && remote != nil && from.Port > 0 && from.IP.Equal(remote.IP) && from.Zone == remote.Zone
}

func isRTCP(data []byte) bool {
	return len(data) >= 2 && data[0]>>6 == 2 && data[1] >= 192 && data[1] <= 223
}
func isSTUN(data []byte) bool {
	return len(data) >= 20 && data[0]&0xc0 == 0 && binary.BigEndian.Uint32(data[4:8]) == 0x2112a442
}
func (s *Session) write(data []byte, remote *net.UDPAddr, transport net.Conn, control bool) (int, error) {
	return s.writeWithDeadline(data, remote, transport, control, time.Now().Add(200*time.Millisecond))
}
func (s *Session) writeWithDeadline(data []byte, remote *net.UDPAddr, transport net.Conn, control bool, deadline time.Time) (int, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if transport != nil {
		s.mu.Lock()
		transport = s.iceConn
		s.mu.Unlock()
		if err := transport.SetWriteDeadline(deadline); err != nil {
			return 0, err
		}
		return transport.Write(data)
	}
	conn := s.conn
	if control {
		conn = s.control
	}
	if err := conn.SetWriteDeadline(deadline); err != nil {
		return 0, err
	}
	return conn.WriteToUDP(data, remote)
}
func (s *Session) SetRTCPMux(enabled bool) {
	s.mu.Lock()
	s.mux = enabled
	s.stats.RTCPMux = enabled
	s.mu.Unlock()
}
func sameAddress(a, b *net.UDPAddr) bool {
	return a != nil && b != nil && a.Port == b.Port && a.IP.Equal(b.IP)
}
func (s *Session) track(p *pion.Packet, size int, now time.Time) bool {
	if s.received && p.SSRC != s.source {
		if !s.sourceCanChange {
			return false
		}
		expected := uint64(s.highest-s.first) + 1
		if expected > s.sourcePackets {
			s.previousLost += expected - s.sourcePackets
		}
		s.received = false
		s.sourcePackets = 0
		s.jitter = 0
		s.expectedPrior = 0
		s.receivedPrior = 0
		s.lastSR = 0
		s.lastSRAt = time.Time{}
	}
	if !s.received {
		s.sourceCanChange = false
		s.received = true
		s.source = p.SSRC
		s.highest = uint32(p.SequenceNumber)
		s.first = s.highest
		s.seen = 1
		s.transit = (now.Unix()*int64(s.clock) + int64(now.Nanosecond())*int64(s.clock)/int64(time.Second)) - int64(p.Timestamp)
	} else {
		if p.SSRC != s.source {
			return false
		}
		delta := int32(int16(p.SequenceNumber - uint16(s.highest)))
		if delta > 0 {
			if delta >= 64 {
				s.seen = 1
			} else {
				s.seen = s.seen<<uint(delta) | 1
			}
			s.highest += uint32(delta)
		} else {
			offset := -delta
			if offset >= 64 || uint32(offset) > s.highest-s.first || s.seen&(uint64(1)<<uint(offset)) != 0 {
				return false
			}
			s.seen |= uint64(1) << uint(offset)
		}
		transit := (now.Unix()*int64(s.clock) + int64(now.Nanosecond())*int64(s.clock)/int64(time.Second)) - int64(p.Timestamp)
		// Timestamp subtraction is modular, including RTP timestamp wraparound.
		difference := math.Abs(float64(int32(transit - s.transit)))
		if !s.resetTransit {
			s.jitter += (difference - s.jitter) / 16
		}
		s.resetTransit = false
		s.transit = transit
	}
	s.resetTransit = false
	s.sourcePackets++
	s.stats.PacketsReceived++
	s.stats.LastReceived = now
	s.stats.BytesReceived += uint64(size)
	return true
}
func ntp(now time.Time) uint64 {
	return uint64(now.Unix()+2208988800)<<32 | uint64(now.Nanosecond())*(1<<32)/1e9
}
func (s *Session) report(ctx context.Context) {
	defer s.workers.Done()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			s.mu.Lock()
			if s.remoteControl == nil || (s.dtls != nil && !s.dtls.ready) {
				s.mu.Unlock()
				continue
			}
			timestamp := ntp(now)
			reports := []rtcp.ReceptionReport{}
			if s.received {
				expected := uint64(s.highest-s.first) + 1
				lost := uint32(0)
				if expected > s.sourcePackets {
					lost = uint32(min(expected-s.sourcePackets, 0x7fffff))
				}
				intervalExpected := expected - s.expectedPrior
				intervalReceived := s.sourcePackets - s.receivedPrior
				fraction := uint8(0)
				if intervalExpected > intervalReceived && intervalExpected > 0 {
					fraction = uint8(min(uint64(255), (intervalExpected-intervalReceived)*256/intervalExpected))
				}
				s.expectedPrior = expected
				s.receivedPrior = s.sourcePackets
				delay := uint32(0)
				if !s.lastSRAt.IsZero() {
					delay = uint32(now.Sub(s.lastSRAt).Seconds() * 65536)
				}
				reports = append(reports, rtcp.ReceptionReport{SSRC: s.source, FractionLost: fraction, TotalLost: lost, LastSequenceNumber: s.highest, Jitter: uint32(s.jitter), LastSenderReport: s.lastSR, Delay: delay})
			}
			sr := &rtcp.SenderReport{SSRC: s.ssrc, NTPTime: timestamp, RTPTime: s.timestamp, PacketCount: s.senderPackets, OctetCount: uint32(s.payloadBytes), Reports: reports}
			s.sentReports[uint32(timestamp>>16)] = now
			for stamp, sent := range s.sentReports {
				if now.Sub(sent) > 30*time.Second {
					delete(s.sentReports, stamp)
				}
			}
			cname := &rtcp.SourceDescription{Chunks: []rtcp.SourceDescriptionChunk{{Source: s.ssrc, Items: []rtcp.SourceDescriptionItem{{Type: rtcp.SDESCNAME, Text: fmt.Sprintf("voiper-%08x", s.ssrc)}}}}}
			data, err := rtcp.Marshal([]rtcp.Packet{sr, cname})
			if err == nil && s.outgoing != nil {
				data, err = s.outgoing.EncryptRTCP(nil, data, nil)
			}
			remote := s.remoteControl
			transport, mux := s.iceConn, s.mux
			s.mu.Unlock()
			if err == nil {
				_, _ = s.write(data, remote, transport, !mux)
			}
		}
	}
}
func (s *Session) receiveControl() {
	defer s.workers.Done()
	data := make([]byte, 4096)
	for {
		n, from, err := s.control.ReadFromUDP(data)
		if err != nil {
			return
		}
		s.handleControl(data[:n], from, false)
	}
}
func (s *Session) handleControl(data []byte, from *net.UDPAddr, trusted bool) {
	var err error
	s.mu.Lock()
	rebinding := !trusted && !sameAddress(from, s.remoteControl) && s.canRebind(from, s.remoteControl)
	allowed := (trusted || (s.iceConn == nil && sameAddress(from, s.remoteControl)) || rebinding) && (s.dtls == nil || s.dtls.ready)
	var plaintext []byte
	if allowed && s.incoming != nil {
		plaintext, err = s.incoming.DecryptRTCP(nil, data, nil)
		if err != nil {
			allowed = false
			s.stats.PacketsDropped++
		}
	} else {
		plaintext = data
	}
	if !allowed {
		s.mu.Unlock()
		return
	}
	packets, err := rtcp.Unmarshal(plaintext)
	if err != nil || len(packets) == 0 {
		s.mu.Unlock()
		return
	}
	if s.symmetric && s.dtls == nil && s.incoming != nil && s.protectionProfile == srtp.ProtectionProfileAes128CmHmacSha1_80 && len(plaintext) >= 8 && len(data) >= 14 {
		// This SDES profile has a ten-byte authentication tag and no MKI.
		// Pion already authenticated both this index and the sender SSRC.
		index := binary.BigEndian.Uint32(data[len(data)-14:]) & 0x7fffffff
		source := binary.BigEndian.Uint32(plaintext[4:])
		fresh := !s.controlSeen || (source == s.controlSource && index > s.controlIndex) || (!rebinding && source != s.controlSource && (!s.received || source == s.source))
		if rebinding && (!fresh || (s.received && source != s.source)) {
			s.mu.Unlock()
			return
		}
		if fresh {
			s.controlSource, s.controlIndex, s.controlSeen = source, index, true
		}
		if rebinding {
			s.remoteControl = from
			if s.mux {
				s.remote = from
				s.stats.RemoteAddress = from.String()
			}
		}
	}
	for _, packet := range packets {
		var reports []rtcp.ReceptionReport
		switch p := packet.(type) {
		case *rtcp.SenderReport:
			if s.received && p.SSRC != s.source {
				continue
			}
			s.lastSR = uint32(p.NTPTime >> 16)
			s.lastSRAt = time.Now()
			reports = p.Reports
		case *rtcp.ReceiverReport:
			reports = p.Reports
		}
		for _, r := range reports {
			// Only fresh reports about this sender can influence its encoder. Older
			// reports can arrive after newer ones because RTCP also uses UDP.
			if r.SSRC == s.ssrc && s.senderPackets > 0 && (s.stats.ReceiverReportAt.IsZero() || int32(r.LastSequenceNumber-s.stats.RemoteLastSequenceNumber) > 0) {
				s.stats.ReceiverReportAt = time.Now()
				s.stats.RemoteFractionLost = r.FractionLost
				s.stats.RemoteLastSequenceNumber = r.LastSequenceNumber
			}
			if sent, ok := s.sentReports[r.LastSenderReport]; r.SSRC == s.ssrc && r.LastSenderReport != 0 && ok {
				rtt := time.Since(sent).Seconds() - float64(r.Delay)/65536
				if rtt >= 0 {
					s.stats.RTTMilliseconds = rtt * 1000
				}
			}
		}
	}
	s.mu.Unlock()
}
func (s *Session) Close() error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		transport := s.ice
		restart := s.restart
		secure := s.dtls
		s.mu.Unlock()
		s.cancel()
		if secure != nil {
			secure.socket.Close()
			s.mu.Lock()
			secureConn := secure.conn
			s.mu.Unlock()
			if secureConn != nil {
				secureConn.Close()
			}
		}
		if restart != nil {
			restart.Cancel()
		}
		if transport != nil {
			transport.close()
		}
		_ = s.conn.Close()
		_ = s.control.Close()
		s.workers.Wait()
		close(s.packets)
	})
	return nil
}

func (s *Session) Advance(samples uint32) { s.mu.Lock(); s.timestamp += samples; s.mu.Unlock() }
