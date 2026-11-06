package phone

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/megakuul/voiper/internal/compat/swyx/cds"
)

// swyxPresenceServer stores the presence configuration like SwyxWare does.
func swyxPresenceServer(t *testing.T, initial map[string]any) (*cds.Client, map[string]any) {
	t.Helper()
	stored := initial
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/login/acquiretoken"):
			json.NewEncoder(w).Encode(map[string]any{"AccessToken": "token", "UserId": 7})
		case strings.HasSuffix(r.URL.Path, "/user/7/presenceconfiguration") && r.Method == http.MethodGet:
			json.NewEncoder(w).Encode(stored)
		case strings.HasSuffix(r.URL.Path, "/user/7/presenceconfiguration") && r.Method == http.MethodPut:
			clear(stored)
			json.NewDecoder(r.Body).Decode(&stored)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	client, err := cds.New(cds.Config{
		Server: strings.TrimPrefix(server.URL, "https://"), Username: "linus", Password: "secret",
		Trust: func(host, fingerprint string) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	return client, stored
}

func TestSwyxStatusFollowsDNDWithoutTouchingOtherFields(t *testing.T) {
	client, stored := swyxPresenceServer(t, map[string]any{"IsAway": true, "DoNotDisturb": false, "AwayText": "Set in SwyxIt", "LoginDeviceType": 16})
	a := &account{state: AccountState{Name: "work"}}
	m := &Manager{accounts: map[string]*account{"work": a}}
	ctx := context.Background()

	// Starting Voiper with nothing to say must not reset the status from SwyxIt!.
	if err := m.syncSwyxStatus(ctx, a, client, swyxStatus{}, swyxStatus{}); err != nil {
		t.Fatal(err)
	}
	if stored["IsAway"] != true || stored["AwayText"] != "Set in SwyxIt" || a.state.PresenceState != "away" {
		t.Fatalf("status changed on start: %v, state %q", stored, a.state.PresenceState)
	}

	if err := m.syncSwyxStatus(ctx, a, client, swyxStatus{dnd: true}, swyxStatus{}); err != nil {
		t.Fatal(err)
	}
	if stored["DoNotDisturb"] != true || stored["IsAway"] != true || stored["AwayText"] != "Set in SwyxIt" || stored["LoginDeviceType"] != float64(16) {
		t.Fatalf("DND not written alone: %v", stored)
	}
	if a.state.PresenceState != "dnd" {
		t.Fatalf("state = %q, want dnd", a.state.PresenceState)
	}

	if err := m.syncSwyxStatus(ctx, a, client, swyxStatus{}, swyxStatus{dnd: true}); err != nil {
		t.Fatal(err)
	}
	if stored["DoNotDisturb"] != false {
		t.Fatalf("DND not cleared: %v", stored)
	}
}

func TestSwyxPresenceCompletesSIPStatus(t *testing.T) {
	statuses := map[string]cds.Presence{"318": {Status: cds.StatusAway, StatusText: "Lunch"}}
	p := withSwyxPresence(Presence{Target: "sip:318@pbx", State: "available"}, statuses)
	if p.State != "available" || p.Note != "Lunch" {
		t.Fatalf("SIP state must win, text must be added: %+v", p)
	}
	p = withSwyxPresence(Presence{Target: "sip:318@pbx", State: "unavailable"}, statuses)
	if p.State != "away" {
		t.Fatalf("API status must fill a missing SIP state: %+v", p)
	}
}
