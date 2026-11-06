package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

// fakeSession simulates a VPN session for UI development (VIBEBENDER_FAKE=1):
//   - password "wrong"          -> rejected login (no auto-reconnect)
//   - username containing "otp" -> 2FA prompt, any non-empty code passes
//   - VIBEBENDER_FAKE_DROP=N    -> tunnel drops after N seconds (exercises reconnect)
func fakeSession(ctx context.Context, cfg *Config, prompt PromptFunc, onUp ConnectedFunc) error {
	slog.Info("logging in...", "server", cfg.Server, "fake", true)
	if err := sleep(ctx, 700*time.Millisecond); err != nil {
		return err
	}
	if cfg.Password == "wrong" {
		return &authError{"login rejected: invalid credentials (fake)"}
	}
	if strings.Contains(cfg.Username, "otp") {
		code, err := prompt(ctx, "Enter the code from your authenticator app (fake).")
		if err != nil {
			return err
		}
		if code == "" {
			return &authError{"login rejected: empty OTP (fake)"}
		}
	}

	slog.Info("starting session...")
	if err := sleep(ctx, 500*time.Millisecond); err != nil {
		return err
	}
	slog.Info("dialing tunnel...")
	slog.Debug("pppd: Using interface ppp0")
	if err := sleep(ctx, 700*time.Millisecond); err != nil {
		return err
	}
	onUp("ppp0", "10.255.0.1", []string{"10.0.0.0/255.0.0.0", "192.168.10.0/24"}, []string{"10.0.0.53"})
	slog.Info("remote routing configured, VPN is up")

	var drop <-chan time.Time
	if n, _ := strconv.Atoi(os.Getenv("VIBEBENDER_FAKE_DROP")); n > 0 {
		drop = time.After(time.Duration(n) * time.Second)
	}
	select {
	case <-ctx.Done():
		slog.Info("disconnecting, terminating pppd")
		return ctx.Err()
	case <-drop:
		return errors.New("tunnel read ended: connection reset by peer (fake)")
	}
}

func sleep(ctx context.Context, d time.Duration) error {
	select {
	case <-time.After(d):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
