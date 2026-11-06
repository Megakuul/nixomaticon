package phone

import (
	"context"
	"fmt"
	"github.com/emiago/sipgo"
	wire "github.com/emiago/sipgo/sip"
	"github.com/megakuul/voiper/internal/config"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/megakuul/voiper/pkg/sip"
)

func TestSuspendEndsRemoteDialog(t *testing.T) {
	manager := testManager(t)
	owner := testAccount(t, manager, "office")
	remoteEvents := make(chan sip.Event, 16)
	remote, err := sip.NewClient(sip.Config{Server: "127.0.0.1", Username: "peer", LocalAddress: "127.0.0.1:0"}, func(event sip.Event) { remoteEvents <- event })
	if err != nil {
		t.Fatal(err)
	}
	defer remote.Close()
	ctx, cancel := context.WithTimeout(manager.ctx, 5*time.Second)
	defer cancel()
	const sdp = "v=0\r\no=- 1 1 IN IP4 127.0.0.1\r\ns=call\r\nc=IN IP4 127.0.0.1\r\nt=0 0\r\nm=audio 8000 RTP/AVP 0\r\na=sendrecv\r\n"
	if err = owner.client.DialID(ctx, "sleep-call", "sip:peer@"+remote.LocalAddr(), []byte(sdp)); err != nil {
		t.Fatal(err)
	}
	awaitCallEvent(t, remoteEvents, "incoming")
	if err = remote.Answer(ctx, "sleep-call", []byte(sdp)); err != nil {
		t.Fatal(err)
	}
	awaitCallEvent(t, remoteEvents, "connected")
	manager.mu.Lock()
	manager.calls["sleep-call"] = &call{state: CallState{ID: "sleep-call", Account: "office", Remote: "peer", Direction: "outgoing", State: "connected", Started: time.Now(), Connected: time.Now()}, owner: owner, cancel: cancel, ended: make(chan struct{})}
	manager.mu.Unlock()
	manager.Suspend()
	awaitCallEvent(t, remoteEvents, "ended")
	if len(manager.Snapshot().Calls) != 0 {
		t.Fatal("suspended call remains visible")
	}
	history, err := manager.store.History()
	if err != nil || len(history) != 1 {
		t.Fatalf("call history: %+v,%v", history, err)
	}
}

func awaitCallEvent(t *testing.T, events <-chan sip.Event, kind string) {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case event := <-events:
			if event.Type == kind {
				return
			}
		case <-timer.C:
			t.Fatalf("timed out waiting for remote %s", kind)
		}
	}
}

func TestRecoveryAfterInitialRegistrationFailure(t *testing.T) {
	for _, status := range []int{503, 403} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			manager := testManager(t)
			ua, err := sipgo.NewUA()
			if err != nil {
				t.Fatal(err)
			}
			defer ua.Close()
			server, err := sipgo.NewServer(ua)
			if err != nil {
				t.Fatal(err)
			}
			listener, err := net.ListenPacket("udp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			var available atomic.Bool
			server.OnRegister(func(request *wire.Request, transaction wire.ServerTransaction) {
				responseStatus, reason := status, "Unavailable"
				if available.Load() {
					responseStatus, reason = 200, "OK"
				}
				response := wire.NewResponseFromRequest(request, responseStatus, reason, nil)
				response.AppendHeader(wire.NewHeader("Expires", "3600"))
				transaction.Respond(response)
			})
			serveDone := make(chan struct{})
			go func() { defer close(serveDone); server.ServeUDP(listener) }()
			defer func() { listener.Close(); <-serveDone }()
			changed := make(chan struct{}, 32)
			manager.emit = func(string, any) {
				select {
				case changed <- struct{}{}:
				default:
				}
			}
			if err = manager.Enable("office", config.Config{Server: "127.0.0.1", Port: listener.LocalAddr().(*net.UDPAddr).Port, Username: "alice", LocalAddress: "127.0.0.1:0"}); err != nil {
				t.Fatal(err)
			}
			waitRegistrationState(t, manager, changed, "failed")
			available.Store(true)
			manager.RecoverRegistrations()
			if status == 503 {
				waitRegistrationState(t, manager, changed, "registered")
			} else if manager.Snapshot().Accounts[0].State != "failed" {
				t.Fatal("authentication failure retried without explicit user action")
			}
			if err := manager.RetryRegistration("office"); err != nil {
				t.Fatal(err)
			}
			waitRegistrationState(t, manager, changed, "registered")
			if err := manager.RetryRegistration("office"); err != nil {
				t.Fatalf("refresh registered account: %v", err)
			}
			if err := manager.RetryRegistration("missing"); err == nil {
				t.Fatal("retry accepted disabled account")
			}
		})
	}
}

func waitRegistrationState(t *testing.T, manager *Manager, changed <-chan struct{}, state string) {
	t.Helper()
	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()
	for {
		snapshot := manager.Snapshot()
		if len(snapshot.Accounts) == 1 && snapshot.Accounts[0].State == state {
			return
		}
		select {
		case <-changed:
		case <-timeout.C:
			t.Fatalf("account did not become %s: %+v", state, snapshot.Accounts)
		}
	}
}

// An account renews its client by itself once the registrar is reachable again,
// but a refused account waits for the user.
func TestSupervisorRenewsAccountAfterTransientFailure(t *testing.T) {
	checkInterval, retryDelay := registrationCheckInterval, registrationRetryDelay
	registrationCheckInterval, registrationRetryDelay = 20*time.Millisecond, 50*time.Millisecond
	t.Cleanup(func() { registrationCheckInterval, registrationRetryDelay = checkInterval, retryDelay })
	for _, status := range []int{503, 403} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			manager := testManager(t)
			ua, err := sipgo.NewUA()
			if err != nil {
				t.Fatal(err)
			}
			defer ua.Close()
			server, err := sipgo.NewServer(ua)
			if err != nil {
				t.Fatal(err)
			}
			listener, err := net.ListenPacket("udp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			var registrations atomic.Int32
			server.OnRegister(func(request *wire.Request, transaction wire.ServerTransaction) {
				responseStatus, reason := status, "Unavailable"
				if registrations.Add(1) > 3 && status == 503 {
					responseStatus, reason = 200, "OK"
				}
				response := wire.NewResponseFromRequest(request, responseStatus, reason, nil)
				response.AppendHeader(wire.NewHeader("Expires", "3600"))
				transaction.Respond(response)
			})
			serveDone := make(chan struct{})
			go func() { defer close(serveDone); server.ServeUDP(listener) }()
			defer func() { listener.Close(); <-serveDone }()
			changed := make(chan struct{}, 32)
			manager.emit = func(string, any) {
				select {
				case changed <- struct{}{}:
				default:
				}
			}
			if err = manager.Enable("office", config.Config{Server: "127.0.0.1", Port: listener.LocalAddr().(*net.UDPAddr).Port, Username: "alice", LocalAddress: "127.0.0.1:0"}); err != nil {
				t.Fatal(err)
			}
			if status == 503 {
				waitRegistrationState(t, manager, changed, "registered")
				return
			}
			waitRegistrationState(t, manager, changed, "failed")
			time.Sleep(300 * time.Millisecond)
			if registrations.Load() != 1 {
				t.Fatalf("refused account registered %d times without user action", registrations.Load())
			}
		})
	}
}
