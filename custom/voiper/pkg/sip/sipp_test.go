package sip

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSIPpReliableIncoming(t *testing.T) { testSIPpIncoming(t, "reliable-uac.xml", false) }

func TestSIPpDelayedOffer(t *testing.T) { testSIPpIncoming(t, "delayed-uac.xml", true) }

func testSIPpIncoming(t *testing.T, fixture string, delayed bool) {
	t.Helper()
	binary := os.Getenv("VOIPER_SIPP")
	if binary == "" {
		binary, _ = exec.LookPath("sipp")
	}
	if binary == "" {
		t.Skip("SIPp is available in the Nix development shell")
	}
	receiver, events := newTestClient(t, "bob")
	if delayed {
		receiver.SetOfferHandler(func(_ string, offer []byte) ([]byte, error) {
			if len(offer) != 0 {
				return nil, fmt.Errorf("expected bodyless re-INVITE")
			}
			return []byte(offerSDP), nil
		})
		receiver.SetAnswerHandler(func(_ string, answer []byte) error {
			if !strings.Contains(string(answer), "m=audio 8000 RTP/AVP 0") {
				return fmt.Errorf("missing negotiated audio")
			}
			return nil
		})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	scenario, err := filepath.Abs(filepath.Join("testdata", fixture))
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(ctx, binary, receiver.LocalAddr(), "-sf", scenario, "-i", "127.0.0.1", "-p", "0", "-m", "1", "-nostdin", "-timeout", "8s", "-timeout_error", "-trace_err", "-trace_msg")
	command.Dir = t.TempDir()
	output := make(chan error, 1)
	go func() {
		data, err := command.CombinedOutput()
		if err != nil {
			t.Logf("SIPp: %s", data)
		}
		output <- err
	}()
	incoming := nextEvent(t, events, "incoming")
	if incoming.DelayedOffer != delayed {
		t.Fatal("incorrect delayed offer event")
	}
	cl, err := receiver.getCall(incoming.CallID)
	if err != nil {
		t.Fatal(err)
	}
	cl.mu.Lock()
	prack := cl.prack
	cl.mu.Unlock()
	if prack != nil {
		select {
		case <-prack:
		case <-ctx.Done():
			t.Fatal("PRACK not received")
		}
	}
	if err := receiver.Answer(ctx, incoming.CallID, []byte(offerSDP)); err != nil {
		t.Fatal(err)
	}
	connected := nextEvent(t, events, "connected")
	if connected.DelayedOffer != delayed || len(connected.SDP) == 0 {
		t.Fatal("missing negotiated SDP")
	}
	if delayed {
		nextEvent(t, events, "updated")
	}
	nextEvent(t, events, "ended")
	if err := <-output; err != nil {
		logs, _ := filepath.Glob(filepath.Join(command.Dir, "*"))
		for _, path := range logs {
			data, _ := os.ReadFile(path)
			t.Logf("%s: %s", path, data)
		}
		t.Fatal(err)
	}
}

func TestSIPpReliableOutgoing(t *testing.T) {
	binary := os.Getenv("VOIPER_SIPP")
	if binary == "" {
		binary, _ = exec.LookPath("sipp")
	}
	if binary == "" {
		t.Skip("SIPp is available in the Nix development shell")
	}
	socket, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := socket.LocalAddr().String()
	_, port, _ := net.SplitHostPort(address)
	socket.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	scenario, err := filepath.Abs("testdata/reliable-uas.xml")
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(ctx, binary, "-sf", scenario, "-i", "127.0.0.1", "-p", port, "-m", "1", "-nostdin", "-timeout", "8s", "-timeout_error", "-trace_err", "-trace_msg")
	command.Dir = t.TempDir()
	output := make(chan error, 1)
	go func() {
		data, err := command.CombinedOutput()
		if err != nil {
			t.Logf("SIPp: %s", data)
		}
		output <- err
	}()
	caller, events := newTestClient(t, "alice")
	id, err := caller.Dial(ctx, "sip:bob@"+address, []byte(offerSDP))
	if err != nil {
		t.Fatal(err)
	}
	nextEvent(t, events, "connected")
	cl, _ := caller.getCall(id)
	sequence := cl.outgoing.InviteRequest.CSeq().SeqNo
	if err := caller.Update(ctx, id, nil); err != nil {
		t.Fatal(err)
	}
	if err := caller.Hangup(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := <-output; err != nil {
		logs, _ := filepath.Glob(filepath.Join(command.Dir, "*"))
		for _, path := range logs {
			data, _ := os.ReadFile(path)
			t.Logf("%s: %s", path, data)
		}
		t.Fatal(err)
	}
	logs, _ := filepath.Glob(filepath.Join(command.Dir, "*_messages.log"))
	if len(logs) != 1 {
		t.Fatal("missing SIPp wire trace")
	}
	data, err := os.ReadFile(logs[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), fmt.Sprintf("CSeq: %d ACK\r\n", sequence)) {
		t.Fatalf("ACK did not preserve INVITE CSeq %d: %s", sequence, data)
	}
	if !strings.Contains(string(data), fmt.Sprintf("RAck: 1 %d INVITE\r\n", sequence)) {
		t.Fatalf("PRACK did not acknowledge INVITE CSeq %d: %s", sequence, data)
	}

}
