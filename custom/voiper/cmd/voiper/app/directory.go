package app

import (
	"context"
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"time"

	"github.com/megakuul/voiper/internal/directory"
	"github.com/megakuul/voiper/internal/store"
)

func (a *App) directorySecret(profile store.DirectoryProfile) string {
	identity := sha256.Sum256([]byte(profile.URL + "\x00" + profile.BaseDN + "\x00" + profile.BindDN))
	return fmt.Sprintf("directory-%s-%x", secretAccount(filepath.Join(a.basePath, "directory")), identity)
}

func (a *App) directoryPassword(ctx context.Context, profile store.DirectoryProfile) (string, error) {
	service, err := a.secretService()
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return service.GetSecret(ctx, a.directorySecret(profile))
}

func (a *App) DirectoryState() (directory.State, error) {
	if err := a.ready(); err != nil {
		return directory.State{}, err
	}
	return a.directory.State()
}

func (a *App) SaveDirectoryProfile(profile store.DirectoryProfile, password string) error {
	if err := a.ready(); err != nil {
		return err
	}
	if err := profile.Validate(); err != nil {
		return err
	}
	if profile.UseSecretService && password != "" {
		service, err := a.secretService()
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(a.ctx, 15*time.Second)
		defer cancel()
		if err = service.SetSecret(ctx, a.directorySecret(profile), password); err != nil {
			return err
		}
		password = ""
	}
	return a.directory.Configure(profile, password)
}

func (a *App) RefreshDirectory() error {
	if err := a.ready(); err != nil {
		return err
	}
	a.directory.Refresh()
	return nil
}

func (a *App) DeleteDirectoryPassword() error {
	if err := a.ready(); err != nil {
		return err
	}
	state, err := a.directory.State()
	if err != nil {
		return err
	}
	service, err := a.secretService()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(a.ctx, 15*time.Second)
	defer cancel()
	return service.DeleteSecret(ctx, a.directorySecret(state.Profile))
}

func (a *App) RestoreDirectoryContact(id int64) error {
	if err := a.ready(); err != nil {
		return err
	}
	return a.store.RestoreDirectoryContact(id)
}

func (a *App) DiscoverDirectoryServers(domain string) ([]string, error) {
	if err := a.ready(); err != nil {
		return nil, err
	}
	return store.DiscoverDirectoryServers(a.ctx, domain)
}
