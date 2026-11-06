//go:build secretservice && cgo

package desktop

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

func TestCancelledSecretOperationDoesNotOpenWallet(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, operation := range []string{"get", "set", "delete"} {
		if _, err := secretOperation(ctx, operation, "test-account", "password"); !errors.Is(err, context.Canceled) {
			t.Fatalf("%s: %v", operation, err)
		}
	}
}

func TestSecretServiceDeadlineWhileServiceIsUnresponsive(t *testing.T) {
	address := privateSessionBus(t)
	conn, err := dbus.Connect(address)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	release := make(chan struct{})
	defer close(release)
	service := &unresponsiveSecretService{release: release, called: make(chan struct{}, 1)}
	if err = conn.Export(service, "/org/freedesktop/secrets", "org.freedesktop.Secret.Service"); err != nil {
		t.Fatal(err)
	}
	if err = conn.Export(service, "/org/freedesktop/secrets", "org.freedesktop.DBus.Properties"); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.RequestName("org.freedesktop.secrets", dbus.NameFlagDoNotQueue); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err = secretOperation(ctx, "get", "isolated-cancellation-test", "")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want deadline cancellation, got %v", err)
	}
	if time.Since(started) > 2*time.Second {
		t.Fatal("wallet operation ignored deadline")
	}
	select {
	case <-service.called:
	default:
		t.Fatal("operation did not reach isolated wallet service")
	}
}

type unresponsiveSecretService struct {
	release <-chan struct{}
	called  chan struct{}
}

func (s *unresponsiveSecretService) OpenSession(string, dbus.Variant) (dbus.Variant, dbus.ObjectPath, *dbus.Error) {
	select {
	case s.called <- struct{}{}:
	default:
	}
	<-s.release
	return dbus.MakeVariant(""), "/", nil
}
func (s *unresponsiveSecretService) GetAll(string) (map[string]dbus.Variant, *dbus.Error) {
	return map[string]dbus.Variant{"Collections": dbus.MakeVariant([]dbus.ObjectPath{})}, nil
}
