package rtp

import (
	"context"
	"github.com/pion/turn/v5"
	"net"
	"strings"
	"testing"
	"time"
)

func restartSessions(t *testing.T, servers []ICEServer, relay bool) (*Session, *Session, ICEDescription) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	a, err := New(ctx, "127.0.0.1:0", 8000)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	b, err := New(ctx, "127.0.0.1:0", 8000)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	offer, err := a.gatherICE(ctx, servers, relay)
	if err != nil {
		t.Fatal(err)
	}
	answer, err := b.gatherICE(ctx, servers, relay)
	if err != nil {
		t.Fatal(err)
	}
	a.SetRemote(b.LocalAddr(), b.ControlAddr(), 8000)
	b.SetRemote(a.LocalAddr(), a.ControlAddr(), 8000)
	done := make(chan error, 1)
	go func() { done <- a.ConnectICE(ctx, answer, true) }()
	if err = b.ConnectICE(ctx, offer, false); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	return a, b, answer
}

func checkICEPacket(t *testing.T, a, b *Session) {
	t.Helper()
	if err := a.Send(8, []byte{1, 2, 3}, 160, false); err != nil {
		t.Fatal(err)
	}
	select {
	case p := <-b.Packets():
		if p == nil || string(p.Payload) != string([]byte{1, 2, 3}) {
			t.Fatal("wrong media payload")
		}
	case <-time.After(time.Second):
		t.Fatal("previous media path stopped")
	}
}

func TestFailedICERestartKeepsPreviousPath(t *testing.T) {
	a, b, remote := restartSessions(t, nil, false)
	before := a.Stats()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	replacement, err := a.PrepareICERestart(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.Cancel()
	if _, err = a.PrepareICERestart(ctx, nil); err == nil {
		t.Fatal("allowed overlapping replacements")
	}
	// Valid credentials that the peer does not know cannot nominate a new pair.
	remote.Username = "unknown-restart"
	remote.Password = "unknown-restart-password-0123456789"
	if err = replacement.Connect(remote); err == nil {
		t.Fatal("invalid credentials established replacement")
	}
	after := a.Stats()
	if before.LocalAddress != after.LocalAddress || before.RemoteAddress != after.RemoteAddress {
		t.Fatal("failed restart changed active addresses")
	}
	checkICEPacket(t, a, b)
	checkICEPacket(t, b, a)
	retry, err := a.PrepareICERestart(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	retry.Cancel()
}

func TestCloseCancelsICEReplacementChecks(t *testing.T) {
	a, _, remote := restartSessions(t, nil, false)
	replacement, err := a.PrepareICERestart(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	remote.Username = "unknown-restart"
	done := make(chan error, 1)
	go func() { done <- replacement.Connect(remote) }()
	closed := make(chan struct{})
	go func() { a.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("close blocked on replacement checks")
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("closed replacement succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("replacement worker survived close")
	}
}

func TestCancelledICEReplacementReleasesPort(t *testing.T) {
	a, _, _ := restartSessions(t, nil, false)
	replacement, err := a.PrepareICERestart(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	description := replacement.Description()
	host := description.DefaultHost("127.0.0.1")
	address := &net.UDPAddr{IP: net.ParseIP(host), Port: description.DefaultPort(host, 0)}
	replacement.Cancel()
	socket, err := net.ListenUDP("udp", address)
	if err != nil {
		t.Fatalf("cancelled replacement retained candidate socket: %v", err)
	}
	socket.Close()
}

func TestICERestartViaTURN(t *testing.T) {
	listener, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server, err := turn.NewServer(turn.ServerConfig{Realm: "restart", AuthHandler: func(request *turn.RequestAttributes) (string, []byte, bool) {
		return request.Username, turn.GenerateAuthKey(request.Username, request.Realm, "password"), request.Username == "voiper"
	}, PacketConnConfigs: []turn.PacketConnConfig{{PacketConn: listener, RelayAddressGenerator: &turn.RelayAddressGeneratorStatic{RelayAddress: net.ParseIP("127.0.0.1"), Address: "127.0.0.1"}}}})
	if err != nil {
		listener.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { server.Close() })
	servers := []ICEServer{{URLs: []string{"turn:" + listener.LocalAddr().String() + "?transport=udp"}, Username: "voiper", Credential: "password"}}
	a, b, _ := restartSessions(t, servers, true)
	oldLocal := a.Stats().LocalAddress
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ar, err := a.PrepareICERestart(ctx, servers)
	if err != nil {
		t.Fatal(err)
	}
	defer ar.Cancel()
	br, err := b.PrepareICERestart(ctx, servers)
	if err != nil {
		t.Fatal(err)
	}
	defer br.Cancel()
	offer, answer := ar.Description(), br.Description()
	for _, description := range []*ICEDescription{&offer, &answer} {
		candidates := []string{}
		for _, candidate := range description.Candidates {
			if strings.Contains(candidate, " typ relay ") {
				candidates = append(candidates, candidate)
			}
		}
		if len(candidates) == 0 {
			t.Fatal("replacement did not gather relay candidates")
		}
		description.Candidates = candidates
	}
	checkICEPacket(t, a, b)
	done := make(chan error, 1)
	go func() { done <- ar.Connect(answer) }()
	if err = br.Connect(offer); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if oldLocal == a.Stats().LocalAddress {
		t.Fatal("restart retained old relay")
	}
	checkICEPacket(t, a, b)
	checkICEPacket(t, b, a)
}

func TestCloseCancelsInitialICEGathering(t *testing.T) {
	blocker, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Close()
	session, err := New(context.Background(), "127.0.0.1:0", 8000)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	done := make(chan error, 1)
	go func() {
		_, err := session.GatherICE(context.Background(), []ICEServer{{URLs: []string{"stun:" + blocker.LocalAddr().String()}}})
		done <- err
	}()
	blocker.SetReadDeadline(time.Now().Add(time.Second))
	if _, _, err = blocker.ReadFrom(make([]byte, 2048)); err != nil {
		t.Fatal(err)
	}
	closed := make(chan struct{})
	go func() { session.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("close did not cancel gathering")
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("gather succeeded after close")
		}
	case <-time.After(time.Second):
		t.Fatal("gather outlived closed session")
	}
}
