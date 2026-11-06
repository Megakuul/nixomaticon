package directory

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/megakuul/voiper/internal/store"
)

func TestConfigurationAndCloseCancelPendingCredentials(t *testing.T) {
	database, err := store.Open(filepath.Join(t.TempDir(), "contacts.db"), filepath.Join(t.TempDir(), "contacts.toml"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	started := make(chan struct{}, 4)
	cancelled := make(chan struct{}, 4)
	service, err := New(context.Background(), database, func(ctx context.Context, _ store.DirectoryProfile) (string, error) {
		started <- struct{}{}
		<-ctx.Done()
		cancelled <- struct{}{}
		return "", ctx.Err()
	}, func() {})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	profile := store.DirectoryProfile{URL: "ldaps://directory.example.org", BaseDN: "dc=example", Limit: 1000, IntervalMinutes: 60, Enabled: true, UseSecretService: true}
	if err = service.Configure(profile, ""); err != nil {
		t.Fatal(err)
	}
	wait := func(ch <-chan struct{}, label string) {
		t.Helper()
		select {
		case <-ch:
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for %s", label)
		}
	}
	wait(started, "wallet request")
	profile.Enabled = false
	if err = service.Configure(profile, ""); err != nil {
		t.Fatal(err)
	}
	wait(cancelled, "configuration cancellation")
	state, err := service.State()
	if err != nil || !state.Status.LastSuccess.IsZero() {
		t.Fatalf("cancelled lookup committed: %+v %v", state, err)
	}
	profile.Enabled = true
	if err = service.Configure(profile, ""); err != nil {
		t.Fatal(err)
	}
	wait(started, "second wallet request")
	service.Close()
	wait(cancelled, "shutdown cancellation")
}

func TestPreviewCredentialsStayWithConfiguredServer(t *testing.T) {
	database, err := store.Open(filepath.Join(t.TempDir(), "contacts.db"), filepath.Join(t.TempDir(), "contacts.toml"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	service, err := New(context.Background(), database, func(context.Context, store.DirectoryProfile) (string, error) { return "wallet-secret", nil }, func() {})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	p := store.DirectoryProfile{URL: "ldaps://directory.example.org", BaseDN: "dc=example", BindDN: "reader", Limit: 1000, IntervalMinutes: 60}
	if err = service.Configure(p, "session-secret"); err != nil {
		t.Fatal(err)
	}
	same := store.DirectoryConfig{URL: p.URL, BindDN: p.BindDN}
	got, err := service.Credentials(context.Background(), same)
	if err != nil || got.Password != "session-secret" {
		t.Fatalf("configured credentials: %+v %v", got, err)
	}
	foreign := same
	foreign.URL = "ldaps://other.example.org"
	got, err = service.Credentials(context.Background(), foreign)
	if err != nil || got.Password != "" {
		t.Fatal("credentials escaped configured endpoint")
	}
	p.UseSecretService = true
	if err = service.Configure(p, ""); err != nil {
		t.Fatal(err)
	}
	got, err = service.Credentials(context.Background(), same)
	if err != nil || got.Password != "wallet-secret" {
		t.Fatal("wallet credentials not used")
	}
	p.UseSecretService = false
	if err = service.Configure(p, ""); err != nil {
		t.Fatal(err)
	}
	got, err = service.Credentials(context.Background(), same)
	if err != nil || got.Password != "" {
		t.Fatal("old session password retained after changing credential mode")
	}
}
