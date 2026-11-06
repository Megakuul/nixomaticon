package rtp

import (
	"context"
	"errors"
	"net"
	"sync"
)

// ICERestart owns a replacement candidate set until connectivity succeeds or Cancel is called.
// The active RTP path remains available throughout gathering and connectivity checks.
type ICERestart struct {
	session     *Session
	transport   *iceTransport
	ctx         context.Context
	cancel      context.CancelFunc
	controlling bool
	connecting  bool
	mu          sync.Mutex
}

func (s *Session) PrepareICERestart(ctx context.Context, servers []ICEServer) (*ICERestart, error) {
	s.mu.Lock()
	if s.closed || s.ice == nil || s.iceConn == nil || s.restart != nil {
		s.mu.Unlock()
		return nil, errors.New("ICE restart requires an active call without a pending restart")
	}
	role := s.ice.role.Load()
	if role == 0 {
		s.mu.Unlock()
		return nil, errors.New("ICE role is not confirmed yet; retry network recovery")
	}
	ctx, cancel := context.WithCancel(ctx)
	restart := &ICERestart{session: s, ctx: ctx, cancel: cancel, controlling: role == 1}
	s.restart = restart
	s.workers.Add(1)
	s.mu.Unlock()
	defer s.workers.Done()
	address := *s.LocalAddr()
	address.Port = 0
	socket, err := net.ListenUDP("udp", &address)
	if err != nil {
		restart.Cancel()
		return nil, err
	}
	dispatcher := &iceSocket{conn: socket, done: make(chan struct{}), direct: true}
	transport, err := gatherTransport(ctx, dispatcher, servers, false)
	if err != nil {
		socket.Close()
		restart.Cancel()
		return nil, err
	}
	restart.mu.Lock()
	if ctx.Err() != nil {
		restart.mu.Unlock()
		transport.close()
		restart.Cancel()
		return nil, ctx.Err()
	}
	restart.transport = transport
	restart.mu.Unlock()
	return restart, nil
}

func (r *ICERestart) Description() ICEDescription {
	r.mu.Lock()
	defer r.mu.Unlock()
	description := r.transport.description
	description.Candidates = append([]string(nil), description.Candidates...)
	return description
}

func (r *ICERestart) Connect(remote ICEDescription) error {
	r.mu.Lock()
	if r.ctx.Err() != nil || r.connecting || r.transport == nil {
		r.mu.Unlock()
		return errors.New("ICE restart is cancelled or already connecting")
	}
	r.connecting = true
	transport := r.transport
	r.mu.Unlock()
	s := r.session
	s.mu.Lock()
	if s.closed || s.restart != r {
		s.mu.Unlock()
		return net.ErrClosed
	}
	if remote.Lite != s.ice.remote.Lite {
		s.mu.Unlock()
		r.Cancel()
		return errors.New("ICE restart cannot change full/lite mode")
	}
	s.workers.Add(1)
	s.mu.Unlock()
	defer s.workers.Done()
	conn, err := transport.connect(r.ctx, remote, r.controlling)
	if err != nil {
		r.Cancel()
		return err
	}
	pair, err := transport.agent.GetSelectedCandidatePair()
	if err != nil || pair == nil {
		r.Cancel()
		return errors.New("ICE restart has no selected pair")
	}
	s.writeMu.Lock()
	s.mu.Lock()
	if s.closed || s.restart != r || r.ctx.Err() != nil {
		s.mu.Unlock()
		s.writeMu.Unlock()
		r.Cancel()
		return net.ErrClosed
	}
	old := s.ice
	s.ice, s.iceConn = transport, conn
	s.restart = nil
	transport.remote = remote
	s.stats.ICEState = "connected"
	s.setICEPair(pair.Local, pair.Remote)
	s.workers.Add(1)
	s.mu.Unlock()
	s.writeMu.Unlock()
	s.observeICE(transport)
	go s.receiveICE(conn)
	old.close()
	return nil
}

func (r *ICERestart) Cancel() {
	r.cancel()
	s := r.session
	s.mu.Lock()
	r.mu.Lock()
	transport := r.transport
	// Once selected, the session owns the transport and cancellation must not close it.
	selected := s.ice == transport
	r.mu.Unlock()
	s.mu.Unlock()
	if transport != nil && !selected {
		transport.close()
	}
	s.mu.Lock()
	if s.restart == r {
		s.restart = nil
	}
	s.mu.Unlock()
}
