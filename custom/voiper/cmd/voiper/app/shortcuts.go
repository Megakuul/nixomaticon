package app

import (
	"errors"

	"github.com/megakuul/voiper/internal/desktop"
)

func (a *App) shortcutService() (*desktop.Service, error) {
	if err := a.ready(); err != nil {
		return nil, err
	}
	a.mu.Lock()
	service := a.desktop
	a.mu.Unlock()
	if service == nil {
		return nil, errors.New("desktop services are starting")
	}
	return service, nil
}

func (a *App) GetGlobalShortcuts() (desktop.ShortcutSettings, error) {
	service, err := a.shortcutService()
	if err != nil {
		return desktop.ShortcutSettings{}, err
	}
	return service.Shortcuts(), nil
}

func (a *App) EnableGlobalShortcuts() error {
	service, err := a.shortcutService()
	if err != nil {
		return err
	}
	return service.EnableShortcuts(a.ctx)
}

func (a *App) ConfigureGlobalShortcuts() error {
	service, err := a.shortcutService()
	if err != nil {
		return err
	}
	return service.ConfigureShortcuts(a.ctx)
}

func (a *App) DisableGlobalShortcuts() error {
	service, err := a.shortcutService()
	if err != nil {
		return err
	}
	return service.DisableShortcuts()
}
