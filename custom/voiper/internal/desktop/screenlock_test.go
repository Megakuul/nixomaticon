package desktop

import (
	"context"
	"testing"
)

func TestScreenLockCommandExitStatus(t *testing.T) {
	ctx := context.Background()
	if locked, err := ScreenLocked(ctx, "exit 0"); err != nil || !locked {
		t.Fatalf("exit 0: locked=%v err=%v", locked, err)
	}
	if locked, err := ScreenLocked(ctx, "exit 1"); err != nil || locked {
		t.Fatalf("exit 1: locked=%v err=%v", locked, err)
	}
	if _, err := ScreenLocked(ctx, "exit 3"); err == nil {
		t.Fatal("other exit statuses must be reported as errors")
	}
}
