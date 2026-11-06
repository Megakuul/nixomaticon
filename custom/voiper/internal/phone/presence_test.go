package phone

import (
	"context"
	"errors"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/megakuul/voiper/internal/config"
	"github.com/megakuul/voiper/pkg/sip"
)

const standardPresence = `<presence xmlns="urn:ietf:params:xml:ns:pidf" entity="sip:bob@example.org"><tuple id="1"><status><basic>open</basic></status></tuple></presence>`

func TestPresenceFallbackRejectsMalformedDocuments(t *testing.T) {
	presence, err := parsePresence([]byte(standardPresence), "auto")
	if err != nil || presence.Source != "standard" || presence.State != "available" {
		t.Fatalf("standard fallback: %+v, %v", presence, err)
	}
	if _, err := parsePresence([]byte(standardPresence+`<extra/>`), "auto"); err == nil {
		t.Fatal("malformed vendor parse fell back to permissive standard parse")
	}
	if _, err := parsePresence([]byte(standardPresence), "swyx"); err == nil {
		t.Fatal("explicit Swyx mode accepted document without extension")
	}
}

func TestWatchTargetNormalization(t *testing.T) {
	cfg := config.Config{Domain: "EXAMPLE.ORG", Server: "127.0.0.1"}
	for _, target := range []string{"bob", "bob@EXAMPLE.ORG", "sip:bob@example.org:5060", "sip:bob@example.org;transport=tcp"} {
		if got := normalizeWatchTarget(target, cfg); got != "sip:bob@example.org" {
			t.Errorf("%s normalized to %s", target, got)
		}
	}
}

func TestPresenceUsesSubscriptionIdentity(t *testing.T) {
	m := testManager(t)
	owner := testAccount(t, m, "office")
	remote := "sip:watched@example.org"
	key := watchKey("office", remote, "presence")
	m.mu.Lock()
	m.presence[key] = Presence{Account: "office", Remote: remote, State: "subscribing", SubscriptionID: "subscription"}
	m.mu.Unlock()
	m.handle(event{account: "office", owner: owner, sip: sip.Event{Type: "presence", State: "presence", CallID: "subscription", RemoteURI: remote, Body: []byte(standardPresence)}})
	snapshot := m.Snapshot()
	if len(snapshot.Presence) != 1 || snapshot.Presence[0].Remote != remote || snapshot.Presence[0].State != "available" {
		t.Fatalf("body entity changed watch identity: %+v", snapshot.Presence)
	}
	m.handle(event{account: "office", owner: owner, sip: sip.Event{Type: "presence", State: "presence", CallID: "unknown", RemoteURI: "sip:unsolicited@example.org", Body: []byte(standardPresence)}})
	if len(m.Snapshot().Presence) != 1 {
		t.Fatal("unsolicited presence allocated state")
	}
}

func TestDialogFullAndPartialUpdates(t *testing.T) {
	full := []byte(`<dialog-info xmlns="urn:ietf:params:xml:ns:dialog-info" entity="sip:bob@example.org" version="5" state="full"><dialog id="call"><state>confirmed</state></dialog></dialog-info>`)
	presence, err := applyDialogInfo(full, Presence{})
	if err != nil || presence.State != "in-call" {
		t.Fatalf("full state: %+v, %v", presence, err)
	}
	partial := []byte(`<dialog-info xmlns="urn:ietf:params:xml:ns:dialog-info" entity="sip:bob@example.org" version="6" state="partial"><dialog id="call"><state>terminated</state></dialog></dialog-info>`)
	presence, err = applyDialogInfo(partial, presence)
	if err != nil || presence.State != "available" {
		t.Fatalf("partial termination: %+v, %v", presence, err)
	}
	gap := []byte(`<dialog-info xmlns="urn:ietf:params:xml:ns:dialog-info" entity="sip:bob@example.org" version="8" state="partial"/>`)
	presence, err = applyDialogInfo(gap, presence)
	if err != nil || presence.State != "unknown" {
		t.Fatalf("version gap must discard confidence: %+v, %v", presence, err)
	}
}

func TestVoicemailSummary(t *testing.T) {
	summary, err := parseMessageSummary([]byte("Messages-Waiting: yes\r\nMessage-Account: sip:voicemail@example.org\r\nVoice-Message: 3/7 (1/2)\r\n"))
	if err != nil || !summary.Waiting || summary.New != 3 || summary.Old != 7 || summary.UrgentNew != 1 || summary.UrgentOld != 2 {
		t.Fatalf("unexpected summary: %+v, %v", summary, err)
	}
	for _, body := range []string{"Voice-Message: 2/1", "Messages-Waiting: maybe", "Messages-Waiting: yes\nVoice-Message: -1/0"} {
		if _, err := parseMessageSummary([]byte(body)); err == nil {
			t.Errorf("accepted invalid summary %q", body)
		}
	}
}

func TestDialogPartialUpdatesStayBounded(t *testing.T) {
	previous := Presence{Remote: "sip:bob@example.org", dialogs: make(map[string]string), dialogVersion: 1, hasDialogState: true}
	for i := 0; i < 256; i++ {
		previous.dialogs[strconv.Itoa(i)] = "confirmed"
	}
	body := []byte(`<dialog-info xmlns="urn:ietf:params:xml:ns:dialog-info" entity="sip:bob@example.org" version="2" state="partial"><dialog id="overflow"><state>confirmed</state></dialog></dialog-info>`)
	if _, err := applyDialogInfo(body, previous); err == nil {
		t.Fatal("partial update grew dialog state beyond the cap")
	}
	if len(previous.dialogs) != 256 {
		t.Fatal("failed update changed prior state")
	}
}

func TestUnwatchCancelsPendingSubscription(t *testing.T) {
	m := testManager(t)
	owner := testAccount(t, m, "office")
	listener, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	target := "sip:bob@" + listener.LocalAddr().String()
	result := make(chan error, 1)
	go func() { result <- m.Watch("office", target) }()
	listener.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, _, err = listener.ReadFrom(make([]byte, 65536)); err != nil {
		t.Fatal(err)
	}
	if err = m.Unwatch("office", target); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("pending watch not canceled: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("pending watch did not stop")
	}
	m.handle(event{account: "office", owner: owner, sip: sip.Event{Type: "presence", State: "presence", CallID: "late", RemoteURI: target, Body: []byte(standardPresence)}})
	if len(m.Snapshot().Presence) != 0 {
		t.Fatal("late subscription result restored stopped watch")
	}
}

func TestDialogRecoveryStartsNewVersionSequence(t *testing.T) {
	m := testManager(t)
	owner := testAccount(t, m, "office")
	owner.config.PresenceMode = "dialog"
	const remote = "sip:bob@example.org"
	m.mu.Lock()
	m.presence[watchKey("office", remote, "dialog")] = Presence{Account: "office", Remote: remote, Target: "bob@example.org", SubscriptionID: "logical", State: "in-call", Source: "dialog", dialogs: map[string]string{"old": "confirmed"}, dialogVersion: 7, hasDialogState: true}
	m.mu.Unlock()
	m.handle(event{account: "office", owner: owner, sip: sip.Event{Type: "presence", State: "unknown", CallID: "logical", RemoteURI: remote}})
	body := []byte(`<dialog-info xmlns="urn:ietf:params:xml:ns:dialog-info" entity="sip:bob@example.org" version="0" state="full"></dialog-info>`)
	m.handle(event{account: "office", owner: owner, sip: sip.Event{Type: "presence", State: "dialog", CallID: "logical", RemoteURI: remote, Body: body}})
	presence := m.Snapshot().Presence[0]
	if presence.State != "available" || presence.Target != "bob@example.org" || presence.SubscriptionID != "logical" {
		t.Fatalf("new subscription retained old dialog state: %+v", presence)
	}
}

func TestVisibleWatchesEvictOldestAutomaticOnly(t *testing.T) {
	m := testManager(t)
	owner := testAccount(t, m, "office")
	owner.config.Domain = "example.org"
	explicit := "sip:explicit@example.org"
	m.mu.Lock()
	m.presence[watchKey("office", explicit, "presence")] = Presence{Account: "office", Remote: explicit, State: "available"}
	for i := range automaticWatchLimit {
		remote := "sip:" + strconv.Itoa(100+i) + "@example.org"
		m.presence[watchKey("office", remote, "presence")] = Presence{Account: "office", Remote: remote, State: "available", automatic: true, lastWanted: time.Unix(int64(i), 0)}
	}
	m.mu.Unlock()

	// 101 is visible again and must survive; 100 is the oldest unrequested watch.
	owner.cancel() // keep the background subscription from reaching the network
	if err := m.WatchVisible("office", []string{"101", "999"}); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.presence[watchKey("office", "sip:100@example.org", "presence")]; ok {
		t.Error("oldest automatic watch was kept")
	}
	if p := m.presence[watchKey("office", "sip:101@example.org", "presence")]; !p.lastWanted.After(time.Unix(automaticWatchLimit, 0)) {
		t.Error("visible watch was not refreshed")
	}
	if _, ok := m.presence[watchKey("office", explicit, "presence")]; !ok {
		t.Error("explicit watch was evicted")
	}
}
