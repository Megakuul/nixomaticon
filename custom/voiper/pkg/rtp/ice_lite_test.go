package rtp

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/pion/ice/v4"
	pion "github.com/pion/rtp"
)

func litePeer(t *testing.T) (*ice.Agent, ICEDescription) {
	t.Helper()
	agent, err := ice.NewAgentWithOptions(ice.WithICELite(true), ice.WithCandidateTypes([]ice.CandidateType{ice.CandidateTypeHost}), ice.WithIncludeLoopback(), ice.WithNetworkTypes([]ice.NetworkType{ice.NetworkTypeUDP4}), ice.WithIPFilter(func(ip net.IP) bool { return ip.IsLoopback() }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { agent.Close() })
	done := make(chan struct{})
	agent.OnCandidate(func(candidate ice.Candidate) {
		if candidate == nil {
			close(done)
		}
	})
	if err = agent.GatherCandidates(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("lite gathering blocked")
	}
	description := ICEDescription{Lite: true}
	description.Username, description.Password, err = agent.GetLocalUserCredentials()
	if err != nil {
		t.Fatal(err)
	}
	candidates, err := agent.GetLocalCandidates()
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range candidates {
		description.Candidates = append(description.Candidates, candidate.Marshal())
	}
	return agent, description
}

func TestICELitePeerKeepsControllingRoleOnRestart(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	session, err := New(ctx, "127.0.0.1:0", 8000)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	local, err := session.GatherICE(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	peer, remote := litePeer(t)
	address := &net.UDPAddr{IP: net.ParseIP(remote.DefaultHost("127.0.0.1")), Port: remote.DefaultPort("127.0.0.1", 0)}
	if err = session.SetRemote(address, address, 8000); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	// A SIP answerer must become controlling when the offerer is ICE-lite.
	go func() { done <- session.ConnectICE(ctx, remote, false) }()
	conn, err := peer.Accept(ctx, local.Username, local.Password)
	if err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	check := func(conn *ice.Conn) {
		t.Helper()
		if err := session.Send(8, []byte{4, 5, 6}, 160, false); err != nil {
			t.Fatal(err)
		}
		conn.SetReadDeadline(time.Now().Add(time.Second))
		data := make([]byte, 2048)
		n, err := conn.Read(data)
		if err != nil {
			t.Fatal(err)
		}
		var packet pion.Packet
		if err = packet.Unmarshal(data[:n]); err != nil || string(packet.Payload) != string([]byte{4, 5, 6}) {
			t.Fatalf("invalid restarted RTP: %v", err)
		}
	}
	check(conn)
	restart, err := session.PrepareICERestart(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer restart.Cancel()
	nextPeer, nextRemote := litePeer(t)
	nextLocal := restart.Description()
	go func() { done <- restart.Connect(nextRemote) }()
	nextConn, err := nextPeer.Accept(ctx, nextLocal.Username, nextLocal.Password)
	if err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	check(nextConn)
}
