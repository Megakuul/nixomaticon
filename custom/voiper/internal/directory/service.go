// Package directory owns the bounded background refresh of a configured LDAP
// phonebook. Contact persistence remains independent of desktop credentials.
package directory

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/megakuul/voiper/internal/store"
)

type State struct {
	Profile store.DirectoryProfile
	Status  store.DirectoryStatus
	Running bool
}

type Service struct {
	ctx             context.Context
	cancel          context.CancelFunc
	done            chan struct{}
	wake            chan struct{}
	store           *store.Store
	password        func(context.Context, store.DirectoryProfile) (string, error)
	changed         func()
	configure       sync.Mutex
	mu              sync.Mutex
	profile         store.DirectoryProfile
	sessionPassword string
	running         bool
	cancelLookup    context.CancelFunc
}

func New(ctx context.Context, database *store.Store, password func(context.Context, store.DirectoryProfile) (string, error), changed func()) (*Service, error) {
	profile, err := database.DirectoryProfile()
	if err != nil {
		return nil, err
	}
	if err = profile.Validate(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	s := &Service{ctx: ctx, cancel: cancel, done: make(chan struct{}), wake: make(chan struct{}, 1), store: database, password: password, changed: changed, profile: profile}
	go s.run()
	return s, nil
}

func (s *Service) Close() { s.cancel(); <-s.done }

func (s *Service) State() (State, error) {
	s.mu.Lock()
	state := State{Profile: s.profile, Running: s.running}
	s.mu.Unlock()
	var err error
	state.Status, err = s.store.DirectoryStatus()
	return state, err
}

func (s *Service) Configure(profile store.DirectoryProfile, password string) error {
	if err := profile.Validate(); err != nil {
		return err
	}
	if len(password) > 8192 {
		return errors.New("directory password is too long")
	}
	s.configure.Lock()
	defer s.configure.Unlock()
	if err := s.ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	if s.cancelLookup != nil {
		s.cancelLookup()
	}
	s.mu.Unlock()
	if err := s.store.SaveDirectoryProfile(profile); err != nil {
		return err
	}
	s.mu.Lock()
	if profile.URL != s.profile.URL || profile.BaseDN != s.profile.BaseDN || profile.BindDN != s.profile.BindDN || profile.UseSecretService != s.profile.UseSecretService {
		s.sessionPassword = ""
	}
	if password != "" {
		s.sessionPassword = password
	}
	s.profile = profile
	s.mu.Unlock()
	if profile.Enabled {
		s.Refresh()
	}
	s.changed()
	return nil
}

// Refresh coalesces repeated button clicks; the running lookup and one queued
// refresh are the maximum work retained for this directory.
func (s *Service) Refresh() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Credentials uses saved authentication only for the exact configured endpoint
// and identity. A preview of a different server must supply its own password.
func (s *Service) Credentials(ctx context.Context, cfg store.DirectoryConfig) (store.DirectoryConfig, error) {
	if cfg.Password != "" {
		return cfg, nil
	}
	s.mu.Lock()
	profile, password := s.profile, s.sessionPassword
	s.mu.Unlock()
	if cfg.URL != profile.URL || cfg.BindDN != profile.BindDN {
		return cfg, nil
	}
	if profile.UseSecretService {
		var err error
		password, err = s.password(ctx, profile)
		if err != nil {
			return cfg, err
		}
	}
	cfg.Password = password
	return cfg, nil
}

func (s *Service) run() {
	defer close(s.done)
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		manual := false
		select {
		case <-s.ctx.Done():
			return
		case <-s.wake:
			manual = true
		case <-ticker.C:
		}
		if s.ctx.Err() != nil {
			return
		}
		s.mu.Lock()
		profile, password := s.profile, s.sessionPassword
		s.mu.Unlock()
		if !profile.Enabled {
			continue
		}
		status, err := s.store.DirectoryStatus()
		if err != nil {
			continue
		}
		if !manual && time.Since(status.LastAttempt) < time.Duration(profile.IntervalMinutes)*time.Minute {
			continue
		}
		ctx, cancel := context.WithTimeout(s.ctx, 40*time.Second)
		s.mu.Lock()
		if s.profile != profile {
			s.mu.Unlock()
			cancel()
			continue
		}
		s.running = true
		s.cancelLookup = cancel
		s.mu.Unlock()
		s.changed()
		if profile.UseSecretService {
			password, err = s.password(ctx, profile)
		} else if profile.BindDN != "" && password == "" {
			err = errors.New("enter the directory password for this session or save it in the desktop wallet")
		}
		result := store.DirectoryResult{}
		if err == nil {
			result, err = store.LookupDirectory(ctx, store.DirectoryConfig{URL: profile.URL, BaseDN: profile.BaseDN, BindDN: profile.BindDN, Password: password, CAFile: profile.CAFile, Limit: profile.Limit})
		}
		// A cancelled or replaced configuration must not publish stale contacts.
		s.configure.Lock()
		if ctx.Err() == nil || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			if ctx.Err() != nil {
				err = ctx.Err()
			}
			if commitErr := s.store.ApplyDirectory(profile, result, err); commitErr != nil {
				_ = s.store.ApplyDirectory(profile, store.DirectoryResult{}, commitErr)
			}
		}
		s.configure.Unlock()
		cancel()
		s.mu.Lock()
		s.running = false
		s.cancelLookup = nil
		s.mu.Unlock()
		s.changed()
	}
}
