package phone

import (
	"net"
	"testing"
	"time"

	"github.com/megakuul/voiper/internal/config"
)

func TestPendingMediaSetupDoesNotQueueAnotherDial(t *testing.T) {
	manager := testManager(t)
	owner := testAccount(t, manager, "office")
	stun, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer stun.Close()
	owner.config.ICEPolicy = "required"
	owner.config.ICEServers = []config.ICEServer{{URLs: []string{"stun:" + stun.LocalAddr().String()}}}
	first := make(chan error, 1)
	go func() { first <- manager.Dial("office", "bob") }()
	data := make([]byte, 1500)
	stun.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, _, err = stun.ReadFrom(data); err != nil {
		t.Fatal(err)
	}
	second := make(chan error, 1)
	go func() { second <- manager.Dial("office", "charlie") }()
	select {
	case err := <-second:
		if err == nil {
			t.Fatal("duplicate call accepted")
		}
	case <-time.After(time.Second):
		t.Error("second dial blocked behind pending media setup")
	}
	snapshot := manager.Snapshot()
	if len(snapshot.Calls) != 1 {
		t.Fatalf("calls: %+v", snapshot.Calls)
	}
	if err = manager.Hangup(snapshot.Calls[0].ID); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-first:
		if err == nil {
			t.Fatal("cancelled setup succeeded")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled media setup did not finish")
	}
}
