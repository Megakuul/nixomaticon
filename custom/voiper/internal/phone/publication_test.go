package phone

import (
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/emiago/sipgo"
	wire "github.com/emiago/sipgo/sip"
	"github.com/megakuul/voiper/internal/config"
)

func publicationManager(t *testing.T, handler func(*wire.Request, wire.ServerTransaction)) *Manager {
	t.Helper()
	ua, err := sipgo.NewUA()
	if err != nil {
		t.Fatal(err)
	}
	server, err := sipgo.NewServer(ua)
	if err != nil {
		ua.Close()
		t.Fatal(err)
	}
	server.OnPublish(handler)
	server.OnRegister(func(req *wire.Request, tx wire.ServerTransaction) {
		res := wire.NewResponseFromRequest(req, 200, "OK", nil)
		res.AppendHeader(wire.NewHeader("Expires", "3600"))
		_ = tx.Respond(res)
	})
	listener, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		ua.Close()
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); _ = server.ServeUDP(listener) }()
	t.Cleanup(func() { listener.Close(); <-done; ua.Close() })
	m := testManager(t)
	changed := make(chan struct{}, 32)
	m.emit = func(string, any) {
		select {
		case changed <- struct{}{}:
		default:
		}
	}
	if err = m.Enable("office", config.Config{Server: "127.0.0.1", Port: listener.LocalAddr().(*net.UDPAddr).Port, Username: "alice", LocalAddress: "127.0.0.1:0"}); err != nil {
		t.Fatal(err)
	}
	waitRegistrationState(t, m, changed, "registered")
	return m
}

func nextPublication(t *testing.T, requests <-chan *wire.Request) *wire.Request {
	t.Helper()
	select {
	case req := <-requests:
		return req
	case <-time.After(5 * time.Second):
		t.Fatal("publication did not arrive")
		return nil
	}
}

func TestPublicationRefreshRecoversLostTagAndWithdraws(t *testing.T) {
	requests := make(chan *wire.Request, 16)
	var sequence atomic.Int32
	m := publicationManager(t, func(req *wire.Request, tx wire.ServerTransaction) {
		requests <- req.Clone()
		n := sequence.Add(1)
		if n == 2 {
			_ = tx.Respond(wire.NewResponseFromRequest(req, 412, "Conditional Request Failed", nil))
			return
		}
		res := wire.NewResponseFromRequest(req, 200, "OK", nil)
		res.AppendHeader(wire.NewHeader("Expires", "1"))
		res.AppendHeader(wire.NewHeader("SIP-ETag", "tag"+strconv.Itoa(int(n))))
		_ = tx.Respond(res)
	})
	if err := m.PublishStatus("office", true, "Desk & phone"); err != nil {
		t.Fatal(err)
	}
	nextPublication(t, requests)
	refresh := nextPublication(t, requests)
	if len(refresh.Body()) != 0 || refresh.GetHeader("SIP-If-Match").Value() != "tag1" {
		t.Fatalf("refresh: %s", refresh.String())
	}
	recreated := nextPublication(t, requests)
	if recreated.GetHeader("SIP-If-Match") != nil || !strings.Contains(string(recreated.Body()), "Desk &amp; phone") {
		t.Fatalf("lost tag recovery: %s", recreated.String())
	}
	if err := m.PublishStatus("office", false, "Away"); err != nil {
		t.Fatal(err)
	}
	update := nextPublication(t, requests)
	if update.GetHeader("SIP-If-Match").Value() != "tag3" || !strings.Contains(string(update.Body()), "<basic>closed</basic>") {
		t.Fatalf("updated state: %s", update.String())
	}
	state := m.Snapshot().Accounts[0]
	if state.PresenceState != "unavailable" || state.PresenceNote != "Away" || state.PresenceError != "" {
		t.Fatalf("published state: %+v", state)
	}
	m.Disable("office")
	removal := nextPublication(t, requests)
	if removal.GetHeader("Expires").Value() != "0" || removal.GetHeader("SIP-If-Match").Value() != "tag4" || len(removal.Body()) != 0 {
		t.Fatalf("disable removal: %s", removal.String())
	}
	select {
	case request := <-requests:
		t.Fatalf("publication survived disable: %s", request.String())
	case <-time.After(1100 * time.Millisecond):
	}
}

func TestPublicationRemovalStopsRefresh(t *testing.T) {
	requests := make(chan *wire.Request, 16)
	m := publicationManager(t, func(req *wire.Request, tx wire.ServerTransaction) {
		requests <- req.Clone()
		res := wire.NewResponseFromRequest(req, 200, "OK", nil)
		res.AppendHeader(wire.NewHeader("Expires", "1"))
		res.AppendHeader(wire.NewHeader("SIP-ETag", "tag"))
		_ = tx.Respond(res)
	})
	if err := m.PublishStatus("office", true, ""); err != nil {
		t.Fatal(err)
	}
	nextPublication(t, requests)
	if err := m.UnpublishStatus("office"); err != nil {
		t.Fatal(err)
	}
	if nextPublication(t, requests).GetHeader("Expires").Value() != "0" {
		t.Fatal("publication not withdrawn")
	}
	if m.Snapshot().Accounts[0].PresenceState != "" {
		t.Fatal("withdrawn status still claimed")
	}
	select {
	case request := <-requests:
		t.Fatalf("withdrawn status refreshed: %s", request.String())
	case <-time.After(1100 * time.Millisecond):
	}
}

func TestDisableCancelsPublication(t *testing.T) {
	requests := make(chan *wire.Request, 16)
	m := publicationManager(t, func(req *wire.Request, _ wire.ServerTransaction) { requests <- req.Clone() })
	result := make(chan error, 1)
	go func() { result <- m.PublishStatus("office", true, "") }()
	nextPublication(t, requests)
	m.Disable("office")
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("pending publication: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("publication not canceled")
	}
}

func TestRejectedPublicationRemainsVisibleAndDoesNotRetry(t *testing.T) {
	var requests atomic.Int32
	m := publicationManager(t, func(req *wire.Request, tx wire.ServerTransaction) {
		requests.Add(1)
		_ = tx.Respond(wire.NewResponseFromRequest(req, 403, "Forbidden", nil))
	})
	if err := m.PublishStatus("office", true, ""); err == nil {
		t.Fatal("rejected publication reported success")
	}
	state := m.Snapshot().Accounts[0]
	if state.PresenceState != "unknown" || state.PresenceError == "" {
		t.Fatalf("rejection not visible: %+v", state)
	}
	m.RecoverRegistrations()
	time.Sleep(2100 * time.Millisecond)
	if requests.Load() != 1 {
		t.Fatal("forbidden publication automatically retried")
	}
}

func TestPublicationRecoveryRefreshesBeforeOriginalTimer(t *testing.T) {
	requests := make(chan *wire.Request, 16)
	var sequence atomic.Int32
	m := publicationManager(t, func(req *wire.Request, tx wire.ServerTransaction) {
		requests <- req.Clone()
		status, reason := 200, "OK"
		if sequence.Add(1) == 2 {
			status, reason = 412, "Conditional Request Failed"
		}
		res := wire.NewResponseFromRequest(req, status, reason, nil)
		res.AppendHeader(wire.NewHeader("Expires", "3600"))
		res.AppendHeader(wire.NewHeader("SIP-ETag", "tag"))
		_ = tx.Respond(res)
	})
	if err := m.PublishStatus("office", true, "Back at desk"); err != nil {
		t.Fatal(err)
	}
	nextPublication(t, requests)
	m.RecoverRegistrations()
	refresh := nextPublication(t, requests)
	if len(refresh.Body()) != 0 || refresh.GetHeader("SIP-If-Match") == nil {
		t.Fatalf("recovery did not refresh existing publication: %s", refresh.String())
	}
	replacement := nextPublication(t, requests)
	if replacement.GetHeader("SIP-If-Match") != nil || !strings.Contains(string(replacement.Body()), "Back at desk") {
		t.Fatalf("recovery did not restore expired state: %s", replacement.String())
	}
}

func TestPublicationTransientRefreshRetriesWithoutDuplicatingState(t *testing.T) {
	requests := make(chan *wire.Request, 16)
	var sequence atomic.Int32
	m := publicationManager(t, func(req *wire.Request, tx wire.ServerTransaction) {
		requests <- req.Clone()
		n := sequence.Add(1)
		if n == 2 {
			_ = tx.Respond(wire.NewResponseFromRequest(req, 503, "Unavailable", nil))
			return
		}
		res := wire.NewResponseFromRequest(req, 200, "OK", nil)
		res.AppendHeader(wire.NewHeader("Expires", "3600"))
		res.AppendHeader(wire.NewHeader("SIP-ETag", "tag"+strconv.Itoa(int(n))))
		_ = tx.Respond(res)
	})
	if err := m.PublishStatus("office", true, ""); err != nil {
		t.Fatal(err)
	}
	nextPublication(t, requests)
	m.RecoverRegistrations()
	nextPublication(t, requests)
	retry := nextPublication(t, requests)
	if len(retry.Body()) != 0 || retry.GetHeader("SIP-If-Match").Value() != "tag1" {
		t.Fatalf("transient retry duplicated instead of refreshing state: %s", retry.String())
	}
	if err := m.UnpublishStatus("office"); err != nil {
		t.Fatal(err)
	}
	if tag := nextPublication(t, requests).GetHeader("SIP-If-Match").Value(); tag != "tag3" {
		t.Fatalf("retry's new tag was lost: %s", tag)
	}
}
