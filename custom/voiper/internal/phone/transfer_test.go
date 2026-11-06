package phone

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/megakuul/voiper/internal/config"
	"github.com/megakuul/voiper/internal/store"
	"github.com/megakuul/voiper/pkg/sip"
)

const transferTestSDP = "v=0\r\no=- 1 1 IN IP4 127.0.0.1\r\ns=Transfer test\r\nc=IN IP4 127.0.0.1\r\nt=0 0\r\nm=audio 40000 RTP/AVP 8\r\na=rtpmap:8 PCMA/8000\r\na=inactive\r\n"

func TestSuccessfulTransferReleasesOriginalDialogs(t *testing.T) {
	for _, attended := range []bool{false, true} {
		name := "blind"
		if attended {
			name = "attended"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			manager := testManager(t)
			owner, ownerEvents := transferAccount(t, manager)
			peer, peerEvents := transferCall(t, ctx, manager, owner, ownerEvents, "original")
			var consultationEvents <-chan sip.Event
			if attended {
				_, consultationEvents = transferCall(t, ctx, manager, owner, ownerEvents, "consultation")
			}
			complete := make(chan struct{})
			peer.SetTransferHandler(func(ctx context.Context, id, target string) error {
				if id != "original" || attended != strings.Contains(strings.ToLower(target), "replaces=") {
					return fmt.Errorf("incorrect transfer target or original dialog: id=%s target=%s", id, target)
				}
				select {
				case <-complete:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			})
			var err error
			if attended {
				err = manager.AttendedTransfer("original", "consultation")
			} else {
				err = manager.Transfer("original", "sip:target@127.0.0.1")
			}
			if err != nil {
				t.Fatal(err)
			}
			wantCalls := 1
			if attended {
				wantCalls = 2
			}
			if len(manager.Snapshot().Calls) != wantCalls {
				t.Fatal("REFER acceptance ended a dialog before final NOTIFY")
			}
			close(complete)
			transferEvent(t, ctx, peerEvents, "ended", "original")
			if attended {
				transferEvent(t, ctx, consultationEvents, "ended", "consultation")
			}
			for {
				history, err := manager.store.History()
				if err != nil {
					t.Fatal(err)
				}
				if len(history) == wantCalls && len(manager.Snapshot().Calls) == 0 {
					for _, entry := range history {
						if entry.Status != "transferred" {
							t.Fatalf("incorrect history status: %+v", history)
						}
					}
					break
				}
				select {
				case <-time.After(5 * time.Millisecond):
				case <-ctx.Done():
					t.Fatalf("transfer cleanup did not finish: calls=%+v history=%+v", manager.Snapshot().Calls, history)
				}
			}
		})
	}
}

func TestFailedTransferKeepsOriginalDialogs(t *testing.T) {
	for _, scenario := range []struct {
		name                       string
		attended, replacementFails bool
	}{
		{"blind/refer-rejected", false, false},
		{"blind/replacement-failed", false, true},
		{"attended/refer-rejected", true, false},
		{"attended/replacement-failed", true, true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			manager := testManager(t)
			owner, ownerEvents := transferAccount(t, manager)
			peer, peerEvents := transferCall(t, ctx, manager, owner, ownerEvents, "original")
			var consultationEvents <-chan sip.Event
			wantCalls := 1
			if scenario.attended {
				_, consultationEvents = transferCall(t, ctx, manager, owner, ownerEvents, "consultation")
				wantCalls = 2
			}
			if scenario.replacementFails {
				peer.SetTransferHandler(func(context.Context, string, string) error {
					return errors.New("replacement was rejected")
				})
			}
			var err error
			if scenario.attended {
				err = manager.AttendedTransfer("original", "consultation")
			} else {
				err = manager.Transfer("original", "sip:target@127.0.0.1")
			}
			if (err != nil) == scenario.replacementFails {
				t.Fatalf("unexpected REFER result: %v", err)
			}
			for {
				calls := manager.Snapshot().Calls
				if len(calls) != wantCalls {
					t.Fatalf("failed transfer removed a dialog: %+v", calls)
				}
				failed := false
				for _, c := range calls {
					if c.State != "connected" {
						t.Fatalf("failed transfer changed call state: %+v", calls)
					}
					if c.ID == "original" && strings.HasPrefix(c.TransferStatus, "failed") {
						failed = true
					}
				}
				if failed {
					break
				}
				select {
				case <-time.After(5 * time.Millisecond):
				case <-ctx.Done():
					t.Fatal("failed transfer did not clear pending status")
				}
			}
			// A late or unsolicited success cannot complete an already failed transfer.
			manager.handle(event{account: "transfer", owner: owner, sip: sip.Event{Type: "transfer", CallID: "original", State: "success", StatusCode: 200}})
			if len(manager.Snapshot().Calls) != wantCalls {
				t.Fatal("late success removed a dialog")
			}
			if err := manager.Hangup("original"); err != nil {
				t.Fatal(err)
			}
			transferEvent(t, ctx, peerEvents, "ended", "original")
			if scenario.attended {
				if err := manager.Hangup("consultation"); err != nil {
					t.Fatal(err)
				}
				transferEvent(t, ctx, consultationEvents, "ended", "consultation")
			}
		})
	}
}

func TestCloseWaitsForTransferredCallHistory(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	path := filepath.Join(t.TempDir(), "transfer.sqlite")
	database, err := store.Open(path, filepath.Join(t.TempDir(), "contacts.toml"))
	if err != nil {
		t.Fatal(err)
	}
	manager := New(context.Background(), database, nil)
	t.Cleanup(func() { manager.Close(); database.Close() })
	owner, ownerEvents := transferAccount(t, manager)
	peer, peerEvents := transferCall(t, ctx, manager, owner, ownerEvents, "original")
	original, err := manager.getCall("original")
	if err != nil {
		t.Fatal(err)
	}
	peer.SetTransferHandler(func(context.Context, string, string) error { return nil })

	locker, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	locker.SetMaxOpenConns(1)
	defer locker.Close()
	if _, err := locker.Exec("BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	// Always unblock the worker before test cleanup closes the manager.
	defer locker.Exec("ROLLBACK")
	if err := manager.Transfer("original", "sip:target@127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	transferEvent(t, ctx, peerEvents, "ended", "original")
	select {
	case <-original.ended:
	case <-ctx.Done():
		t.Fatal("transfer did not reach history cleanup")
	}
	if len(manager.Snapshot().Calls) != 0 {
		t.Fatal("transferred call remained in the manager")
	}

	closed := make(chan struct{})
	go func() { manager.Close(); close(closed) }()
	select {
	case <-manager.done:
	case <-ctx.Done():
		t.Fatal("manager event loop did not stop")
	}
	select {
	case <-closed:
		t.Fatal("manager closed while transfer history was still blocked")
	case <-time.After(100 * time.Millisecond):
	}
	if _, err := locker.Exec("ROLLBACK"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-closed:
	case <-ctx.Done():
		t.Fatal("manager did not close after history became writable")
	}
	history, err := database.History()
	if err != nil || len(history) != 1 || history[0].Status != "transferred" {
		t.Fatalf("manager closed before saving transfer history: %+v / %v", history, err)
	}
}

func transferAccount(t *testing.T, manager *Manager) (*account, <-chan sip.Event) {
	t.Helper()
	ctx, cancel := context.WithCancel(manager.ctx)
	owner := &account{ctx: ctx, cancel: cancel, state: AccountState{Name: "transfer", State: "registered"}, config: config.Config{Server: "127.0.0.1", Username: "transfer", LocalAddress: "127.0.0.1:0"}}
	events := make(chan sip.Event, 32)
	client, err := sip.NewClient(sip.Config{Server: "127.0.0.1", Username: "transfer", LocalAddress: "127.0.0.1:0"}, func(e sip.Event) {
		select {
		case events <- e:
		case <-ctx.Done():
		}
		if e.Type == "transfer" || e.Type == "ended" {
			select {
			case manager.events <- event{account: "transfer", owner: owner, sip: e}:
			case <-ctx.Done():
			}
		}
	})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	owner.client = client
	manager.mu.Lock()
	manager.accounts["transfer"] = owner
	manager.mu.Unlock()
	return owner, events
}

// Real SIP dialogs exercise REFER, NOTIFY and BYE; inactive SDP avoids devices.
// The peer handler models replacement completion. Independent Baresip fixtures
// separately verify the replacement INVITE and attended Replaces exchange.
func transferCall(t *testing.T, ctx context.Context, manager *Manager, owner *account, ownerEvents <-chan sip.Event, id string) (*sip.Client, <-chan sip.Event) {
	t.Helper()
	events := make(chan sip.Event, 32)
	peer, err := sip.NewClient(sip.Config{Server: "127.0.0.1", Username: id, LocalAddress: "127.0.0.1:0"}, func(e sip.Event) {
		select {
		case events <- e:
		case <-ctx.Done():
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { peer.Close() })
	target := "sip:" + id + "@" + peer.LocalAddr()
	if err := owner.client.DialID(ctx, id, target, []byte(transferTestSDP)); err != nil {
		t.Fatal(err)
	}
	transferEvent(t, ctx, events, "incoming", id)
	if err := peer.Answer(ctx, id, []byte(transferTestSDP)); err != nil {
		t.Fatal(err)
	}
	transferEvent(t, ctx, ownerEvents, "connected", id)
	transferEvent(t, ctx, events, "connected", id)
	manager.mu.Lock()
	manager.calls[id] = &call{owner: owner, mediaConnected: true, ended: make(chan struct{}), state: CallState{ID: id, Account: "transfer", Remote: target, Direction: "outgoing", State: "connected", Started: time.Now(), Connected: time.Now()}}
	manager.mu.Unlock()
	return peer, events
}

func transferEvent(t *testing.T, ctx context.Context, events <-chan sip.Event, kind, id string) sip.Event {
	t.Helper()
	for {
		select {
		case e := <-events:
			if e.Type == kind && e.CallID == id {
				return e
			}
		case <-ctx.Done():
			t.Fatalf("waiting for %s on %s: %v", kind, id, ctx.Err())
		}
	}
}
