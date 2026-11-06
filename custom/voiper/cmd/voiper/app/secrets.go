package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"path/filepath"
	"sort"
	"time"

	"github.com/megakuul/voiper/internal/config"
	"github.com/megakuul/voiper/internal/desktop"
)

func secretAccount(path string) string {
	absolute, err := filepath.Abs(path)
	if err != nil {
		absolute = path
	}
	key := sha256.Sum256([]byte(absolute))
	return hex.EncodeToString(key[:])
}

// swyxSecretAccount is the wallet entry for the account's Swyx client-API
// password, kept separate from the SIP password.
func swyxSecretAccount(path string) string {
	absolute, err := filepath.Abs(path)
	if err != nil {
		absolute = path
	}
	key := sha256.Sum256([]byte(absolute + "\x00swyx"))
	return hex.EncodeToString(key[:])
}
func (a *App) secretService() (*desktop.Service, error) {
	a.mu.Lock()
	service := a.desktop
	a.mu.Unlock()
	if service == nil {
		return nil, errors.New("desktop services are starting; try again shortly")
	}
	if !service.Capabilities().Secrets {
		return nil, desktop.ErrUnavailable
	}
	return service, nil
}
func (a *App) DeleteSavedPassword(name string) error {
	if err := a.ready(); err != nil {
		return err
	}
	path, err := config.Path(a.basePath, name)
	if err != nil {
		return err
	}
	service, err := a.secretService()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(a.ctx, 15*time.Second)
	defer cancel()
	if err := service.DeleteSecret(ctx, swyxSecretAccount(path)); err != nil && !errors.Is(err, desktop.ErrSecretNotFound) {
		return err
	}
	return service.DeleteSecret(ctx, secretAccount(path))
}

func (a *App) enableStartupAccounts() {
	profiles, err := config.ListConfigs(a.basePath)
	if err != nil {
		return
	}
	names := []string{}
	for name, encrypted := range profiles {
		if !encrypted {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	var offline []string
	// Ordinary accounts must not wait for another account's locked wallet.
	for _, wallet := range []bool{false, true} {
		for _, name := range names {
			if a.ctx.Err() != nil {
				return
			}
			cfg, err := a.GetConfig(name, "")
			if err != nil || !cfg.AutoEnable || cfg.UseSecretService != wallet {
				continue
			}
			if err = a.EnableConfig(name, ""); err != nil {
				a.emitPhoneEvent("phone-error", "Could not enable "+name+": "+err.Error())
				var network net.Error
				if errors.As(err, &network) {
					offline = append(offline, name)
				}
			}
		}
	}
	// Without a route to the server, which is common right after login, no SIP
	// client can be created. Retry those accounts until the network is up.
	for attempt := 0; len(offline) > 0; attempt++ {
		timer := time.NewTimer(min(30*time.Second, 5*time.Second<<min(attempt, 3)))
		select {
		case <-a.ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		enabled := map[string]bool{}
		for _, account := range a.phone.Snapshot().Accounts {
			enabled[account.Name] = true
		}
		var remaining []string
		for _, name := range offline {
			cfg, err := a.GetConfig(name, "")
			if enabled[name] || err != nil || !cfg.AutoEnable {
				continue
			}
			var network net.Error
			if err = a.EnableConfig(name, ""); errors.As(err, &network) {
				remaining = append(remaining, name)
			} else if err != nil {
				a.emitPhoneEvent("phone-error", "Could not enable "+name+": "+err.Error())
			}
		}
		offline = remaining
	}
}
