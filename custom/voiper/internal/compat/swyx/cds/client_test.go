package cds

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeServer answers the subset of the client API that Voiper uses, with the
// field names and error shape of SwyxWare 14.21.
type fakeServer struct {
	logins, refreshes int
	accessToken       string
	raw               map[string]any
}

func (f *fakeServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, apiPath)
	switch {
	case path == "/login/acquiretoken":
		var credentials struct{ UserName, Password string }
		json.NewDecoder(r.Body).Decode(&credentials)
		if credentials.UserName != "linus" || credentials.Password != "secret" {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"ErrorDetails":"bad password","HttpStatusCode":401,"SecurityErrorCode":"AuthenticationFailed"}`))
			return
		}
		f.logins++
		f.accessToken = "access-1"
		json.NewEncoder(w).Encode(map[string]any{"AccessToken": f.accessToken, "RefreshToken": "refresh", "UserId": 42})
		return
	case path == "/login/refreshtoken":
		if r.URL.Query().Get("token") != "refresh" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		f.refreshes++
		f.accessToken = "access-2"
		json.NewEncoder(w).Encode(map[string]any{"AccessToken": f.accessToken, "UserId": 42})
		return
	}
	if r.Header.Get("Authorization") != "bearer "+f.accessToken {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	switch {
	case path == "/user/42/presenceconfiguration" && r.Method == http.MethodGet:
		json.NewEncoder(w).Encode(f.raw)
	case path == "/user/42/presenceconfiguration" && r.Method == http.MethodPut:
		f.raw = map[string]any{}
		json.NewDecoder(r.Body).Decode(&f.raw)
	case path == "/employees/presence":
		json.NewEncoder(w).Encode(map[string]any{"Timstamp": 7, "List": []any{
			map[string]any{"SiteId": 1, "EmployeeId": 5, "Presence": map[string]any{"Status": StatusAway, "StatusText": "Lunch"}},
		}})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func newTestClient(t *testing.T, server *fakeServer, password string) *Client {
	t.Helper()
	httpServer := httptest.NewTLSServer(server)
	t.Cleanup(httpServer.Close)
	sum := sha256.Sum256(httpServer.Certificate().Raw)
	pinned := hex.EncodeToString(sum[:])
	client, err := New(Config{
		Server:   strings.TrimPrefix(httpServer.URL, "https://"),
		Username: "linus",
		Password: password,
		Trust: func(host, fingerprint string) error {
			if fingerprint != pinned {
				return errors.New("unexpected certificate")
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	return client
}

func TestPresenceConfigurationKeepsOtherFields(t *testing.T) {
	server := &fakeServer{raw: map[string]any{
		"IsAway": false, "DoNotDisturb": false, "AwayText": "", "LoginDeviceType": 16,
		"AppointmentText": "Meeting", "AppointmentExpiration": "/Date(1792048343537+0200)/",
	}}
	client := newTestClient(t, server, "secret")
	ctx := context.Background()
	cfg, err := client.PresenceConfiguration(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cfg.DoNotDisturb, cfg.AwayText = true, "Focus time"
	if err := client.SetPresenceConfiguration(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if server.raw["DoNotDisturb"] != true || server.raw["AwayText"] != "Focus time" {
		t.Fatalf("status was not written: %v", server.raw)
	}
	if server.raw["AppointmentExpiration"] != "/Date(1792048343537+0200)/" || server.raw["LoginDeviceType"] != float64(16) || server.raw["AppointmentText"] != "Meeting" {
		t.Fatalf("unrelated fields changed: %v", server.raw)
	}
	if server.logins != 1 {
		t.Fatalf("logins = %d, want 1", server.logins)
	}
}

func TestExpiredTokenIsRefreshed(t *testing.T) {
	server := &fakeServer{}
	client := newTestClient(t, server, "secret")
	ctx := context.Background()
	if err := client.Login(ctx); err != nil {
		t.Fatal(err)
	}
	server.accessToken = "rotated-by-server"
	changes, timestamp, err := client.PresenceChanges(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if server.refreshes != 1 || timestamp != 7 || len(changes) != 1 || changes[0].Presence.StatusText != "Lunch" {
		t.Fatalf("refreshes=%d timestamp=%d changes=%+v", server.refreshes, timestamp, changes)
	}
}

func TestLoginFailureReportsServerDetails(t *testing.T) {
	client := newTestClient(t, &fakeServer{}, "wrong")
	err := client.Login(context.Background())
	var apiError *Error
	if !errors.As(err, &apiError) || apiError.Status != 401 || apiError.Details != "bad password" {
		t.Fatalf("err = %v", err)
	}
}

func TestUntrustedCertificateIsRejected(t *testing.T) {
	server := httptest.NewTLSServer(&fakeServer{})
	defer server.Close()
	client, err := New(Config{
		Server: strings.TrimPrefix(server.URL, "https://"), Username: "linus", Password: "secret",
		Trust: func(host, fingerprint string) error { return errors.New("certificate changed") },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := client.Login(context.Background()); err == nil || !strings.Contains(err.Error(), "certificate changed") {
		t.Fatalf("err = %v", err)
	}
}
