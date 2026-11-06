package rtp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/ice/v4"
	"github.com/pion/stun/v4"
)

type ICEServer struct {
	URLs                 []string
	Username, Credential string
}
type ICEDescription struct {
	Lite               bool
	Username, Password string
	Candidates         []string
}
type iceTransport struct {
	agent       *ice.Agent
	mux         *ice.UniversalUDPMuxDefault
	socket      *iceSocket
	description ICEDescription
	remote      ICEDescription
	once        sync.Once
	role        atomic.Int32 // 0 unknown, 1 controlling, 2 controlled
	cancel      context.CancelFunc
}

// GatherICE shares the RTP port, preserving the SDP default address when a peer declines ICE.
func (s *Session) GatherICE(ctx context.Context, servers []ICEServer) (ICEDescription, error) {
	return s.gatherICE(ctx, servers, false)
}
func (s *Session) gatherICE(ctx context.Context, servers []ICEServer, relayOnly bool) (ICEDescription, error) {
	socket := &iceSocket{conn: s.conn, packets: make(chan datagram, 256), done: make(chan struct{})}
	s.mu.Lock()
	if s.closed || s.ice != nil || s.iceGathering {
		s.mu.Unlock()
		return ICEDescription{}, errors.New("ICE session is closed or already configured")
	}
	s.iceGathering = true
	s.stats.ICEState = "gathering"
	// The fallback reader must dispatch STUN during candidate gathering.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	placeholder := &iceTransport{socket: socket, cancel: cancel}
	s.ice = placeholder
	s.workers.Add(1)
	defer s.workers.Done()
	s.mu.Unlock()
	transport, err := gatherTransport(ctx, socket, servers, relayOnly)
	s.mu.Lock()
	s.iceGathering = false
	if err != nil || s.closed || s.ice != placeholder {
		if s.ice == placeholder {
			s.ice = nil
		}
		s.mu.Unlock()
		if transport != nil {
			transport.close()
		}
		if err == nil {
			err = net.ErrClosed
		}
		return ICEDescription{}, err
	}
	s.ice = transport
	s.stats.ICEState = "gathered"
	s.mu.Unlock()
	s.observeICE(transport)
	return transport.description, nil
}

func gatherTransport(ctx context.Context, socket *iceSocket, servers []ICEServer, relayOnly bool) (*iceTransport, error) {
	var urls []*stun.URI
	if len(servers) > 8 {
		return nil, errors.New("too many ICE servers")
	}
	for _, server := range servers {
		for _, value := range server.URLs {
			if len(urls) >= 16 {
				return nil, errors.New("too many ICE server URLs")
			}
			uri, err := stun.ParseURI(value)
			if err != nil {
				return nil, errors.New("invalid ICE server URL")
			}
			uri.Username, uri.Password = server.Username, server.Credential
			urls = append(urls, uri)
		}
	}
	mux := ice.NewUniversalUDPMuxDefault(ice.UniversalUDPMuxParams{UDPConn: socket})
	transport := &iceTransport{mux: mux, socket: socket}
	options := []ice.AgentOption{ice.WithUrls(urls), ice.WithUDPMux(mux), ice.WithUDPMuxSrflx(mux), ice.WithNetworkTypes([]ice.NetworkType{ice.NetworkTypeUDP4, ice.NetworkTypeUDP6}), ice.WithMulticastDNSMode(ice.MulticastDNSModeDisabled), ice.WithSTUNGatherTimeout(3 * time.Second), ice.WithDisconnectedTimeout(5 * time.Second), ice.WithFailedTimeout(10 * time.Second), ice.WithKeepaliveInterval(2 * time.Second)}
	if relayOnly {
		options = append(options, ice.WithCandidateTypes([]ice.CandidateType{ice.CandidateTypeRelay}))
	}
	if socket.LocalAddr().(*net.UDPAddr).IP.IsLoopback() {
		options = append(options, ice.WithIncludeLoopback())
	}
	options = append(options, ice.WithBindingRequestHandler(func(message *stun.Message, _, _ ice.Candidate, _ *ice.CandidatePair) bool {
		var control ice.AttrControl
		if control.GetFrom(message) == nil {
			if control.Role == ice.Controlled {
				transport.role.Store(1)
			} else {
				transport.role.Store(2)
			}
		}
		return false
	}))
	agent, err := ice.NewAgentWithOptions(options...)
	if err != nil {
		mux.Close()
		return nil, fmt.Errorf("initialize ICE: %w", err)
	}
	transport.agent = agent
	done := make(chan struct{})
	agent.OnCandidate(func(candidate ice.Candidate) {
		if candidate == nil {
			close(done)
		}
	})
	if err = agent.GatherCandidates(); err != nil {
		transport.close()
		return nil, errors.New("ICE gathering failed")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	select {
	case <-done:
	case <-ctx.Done():
		transport.close()
		return nil, fmt.Errorf("ICE gathering: %w", ctx.Err())
	}
	candidates, err := agent.GetLocalCandidates()
	if err != nil || len(candidates) == 0 {
		transport.close()
		return nil, errors.New("no ICE candidates available")
	}
	description := ICEDescription{}
	description.Username, description.Password, err = agent.GetLocalUserCredentials()
	if err != nil {
		transport.close()
		return nil, errors.New("ICE credentials unavailable")
	}
	for _, candidate := range candidates {
		description.Candidates = append(description.Candidates, candidate.Marshal())
	}
	transport.description = description
	return transport, nil
}

func (s *Session) observeICE(transport *iceTransport) {
	transport.agent.OnConnectionStateChange(func(state ice.ConnectionState) {
		s.mu.Lock()
		if s.ice == transport {
			s.stats.ICEState = strings.ToLower(state.String())
		}
		s.mu.Unlock()
	})
	transport.agent.OnSelectedCandidatePairChange(func(local, remote ice.Candidate) {
		s.mu.Lock()
		if s.ice == transport {
			s.setICEPair(local, remote)
		}
		s.mu.Unlock()
	})
}
func (s *Session) setICEPair(local, remote ice.Candidate) {
	s.stats.ICECandidateType = local.Type().String()
	s.stats.LocalAddress = net.JoinHostPort(local.Address(), fmt.Sprint(local.Port()))
	s.stats.RemoteAddress = net.JoinHostPort(remote.Address(), fmt.Sprint(remote.Port()))
}

func (s *Session) ConnectICE(ctx context.Context, remote ICEDescription, controlling bool) error {
	s.mu.Lock()
	transport := s.ice
	if s.closed || transport == nil || transport.agent == nil {
		s.mu.Unlock()
		return errors.New("ICE is not available")
	}
	if s.iceConn != nil {
		same := remote.Username == transport.remote.Username && remote.Password == transport.remote.Password && remote.Lite == transport.remote.Lite
		s.mu.Unlock()
		if !same {
			return errors.New("ICE restart is not supported during a call")
		}
		return nil
	}
	s.mu.Unlock()
	conn, err := transport.connect(ctx, remote, controlling)
	if err != nil {
		return err
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		conn.Close()
		return errors.New("RTP session closed during ICE checks")
	}
	s.stats.ICEState = "connected"
	s.iceConn = conn
	transport.remote = remote
	s.mux = true
	s.stats.RTCPMux = true
	s.workers.Add(1)
	s.mu.Unlock()
	go s.receiveICE(conn)
	return nil
}
func (transport *iceTransport) connect(ctx context.Context, remote ICEDescription, controlling bool) (*ice.Conn, error) {
	if remote.Lite {
		controlling = true
		transport.role.Store(1)
	}
	if err := transport.agent.SetRemoteICELite(remote.Lite); err != nil {
		return nil, err
	}
	if len(remote.Username) < 4 || len(remote.Username) > 256 || len(remote.Password) < 22 || len(remote.Password) > 256 || len(remote.Candidates) == 0 || len(remote.Candidates) > 64 {
		return nil, errors.New("invalid ICE credentials or candidates")
	}
	supported := 0
	for _, value := range remote.Candidates {
		candidate, err := ice.UnmarshalCandidate(value)
		if err != nil {
			return nil, errors.New("unsupported ICE candidate")
		}
		if candidate.Component() != 1 || !candidate.NetworkType().IsUDP() {
			continue
		}
		supported++
		ip := net.ParseIP(candidate.Address())
		if ip == nil || ip.IsMulticast() || ip.IsUnspecified() {
			return nil, errors.New("ICE candidate must use a unicast IP")
		}
		if err = transport.agent.AddRemoteCandidate(candidate); err != nil {
			return nil, errors.New("invalid remote ICE candidate")
		}
	}
	if supported == 0 {
		return nil, errors.New("no supported ICE candidate")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var conn *ice.Conn
	var err error
	if controlling {
		conn, err = transport.agent.Dial(ctx, remote.Username, remote.Password)
	} else {
		conn, err = transport.agent.Accept(ctx, remote.Username, remote.Password)
	}
	if err != nil {
		return nil, fmt.Errorf("ICE connectivity checks failed: %w", err)
	}
	return conn, nil
}
func (s *Session) receiveICE(conn net.Conn) {
	defer s.workers.Done()
	data := make([]byte, 4096)
	for {
		n, err := conn.Read(data)
		if errors.Is(err, io.ErrShortBuffer) {
			s.mu.Lock()
			s.stats.PacketsDropped++
			s.mu.Unlock()
			continue
		}
		if err != nil {
			return
		}
		if s.receiveDTLS(data[:n], nil, true) {
			continue
		}
		if isRTCP(data[:n]) {
			s.handleControl(data[:n], nil, true)
		} else {
			s.handleRTP(data[:n], nil, true)
		}
	}
}
func (s *Session) DeclineICE() {
	s.mu.Lock()
	transport := s.ice
	s.ice = nil
	s.stats.ICEState = "fallback"
	s.mu.Unlock()
	if transport != nil {
		transport.close()
	}
}
func (t *iceTransport) close() {
	t.once.Do(func() {
		if t.cancel != nil {
			t.cancel()
		}
		if t.agent != nil {
			t.agent.Close()
		}
		if t.mux != nil {
			t.mux.Close()
		}
	})
}

type datagram struct {
	data    []byte
	address net.Addr
}

// iceSocket closes its read queue independently of the shared fallback UDP socket.
type iceSocket struct {
	conn    *net.UDPConn
	packets chan datagram
	done    chan struct{}
	once    sync.Once
	writeMu sync.Mutex
	direct  bool
}

func (s *iceSocket) deliver(data []byte, from net.Addr) {
	select {
	case s.packets <- datagram{append([]byte(nil), data...), from}:
	default:
	}
}
func (s *iceSocket) ReadFrom(data []byte) (int, net.Addr, error) {
	if s.direct {
		return s.conn.ReadFrom(data)
	}
	for {
		select {
		case <-s.done:
			return 0, nil, net.ErrClosed
		case packet := <-s.packets:
			// An oversized UDP datagram must not close Pion's shared socket reader.
			if len(packet.data) > len(data) {
				continue
			}
			return copy(data, packet.data), packet.address, nil
		}
	}
}
func (s *iceSocket) WriteTo(data []byte, to net.Addr) (int, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	select {
	case <-s.done:
		return 0, net.ErrClosed
	default:
	}
	if err := s.conn.SetWriteDeadline(time.Now().Add(200 * time.Millisecond)); err != nil {
		return 0, err
	}
	return s.conn.WriteTo(data, to)
}
func (s *iceSocket) Close() error {
	s.once.Do(func() {
		close(s.done)
		if s.direct {
			s.conn.Close()
		}
	})
	return nil
}
func (s *iceSocket) LocalAddr() net.Addr { return s.conn.LocalAddr() }
func (s *iceSocket) SetReadDeadline(t time.Time) error {
	if !t.IsZero() {
		return errors.New("ICE dispatcher read deadlines are unsupported")
	}
	return nil
}
func (s *iceSocket) SetWriteDeadline(t time.Time) error { return s.conn.SetWriteDeadline(t) }
func (s *iceSocket) SetDeadline(t time.Time) error {
	if err := s.SetReadDeadline(t); err != nil {
		return err
	}
	return s.SetWriteDeadline(t)
}

func (description ICEDescription) DefaultHost(preferred string) string {
	fallback := ""
	for _, value := range description.Candidates {
		candidate, err := ice.UnmarshalCandidate(value)
		if err != nil || candidate.Type() != ice.CandidateTypeHost || candidate.Component() != 1 {
			continue
		}
		if candidate.Address() == preferred {
			return preferred
		}
		if fallback == "" {
			fallback = candidate.Address()
		}
	}
	return fallback
}

func (description ICEDescription) DefaultPort(host string, fallback int) int {
	for _, value := range description.Candidates {
		candidate, err := ice.UnmarshalCandidate(value)
		if err == nil && candidate.Component() == 1 && candidate.Address() == host {
			return candidate.Port()
		}
	}
	return fallback
}
