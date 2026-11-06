package phone

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"

	"github.com/megakuul/voiper/pkg/sip"
)

func (m *Manager) Suspend() {
	m.mu.Lock()
	calls := make([]*call, 0, len(m.calls))
	for id, c := range m.calls {
		calls = append(calls, c)
		delete(m.calls, id)
	}
	m.mu.Unlock()
	ctx, cancel := context.WithTimeout(m.ctx, time.Second)
	defer cancel()
	var signaling sync.WaitGroup
	for _, c := range calls {
		signaling.Add(1)
		go func(c *call) {
			defer signaling.Done()
			if c.owner == nil || c.owner.client == nil {
				return
			}
			m.mu.Lock()
			ringing := c.state.State == "ringing"
			id := c.state.ID
			m.mu.Unlock()
			if ringing {
				c.owner.client.Reject(ctx, id)
			} else {
				c.owner.client.Hangup(ctx, id)
			}
			// Sleep ends local ownership even if the peer could not acknowledge cleanup.
			c.owner.client.AbortCall(id)
		}(c)
	}
	signaling.Wait()
	for _, c := range calls {
		m.finish(c, "ended for system sleep")
	}
	m.notify()
}
func (m *Manager) registerAccount(a *account) error {
	err := a.client.Register(a.ctx)
	m.mu.Lock()
	a.registrationError = err
	if err != nil && a.ctx.Err() == nil && m.accounts[a.state.Name] == a {
		a.state.State = "failed"
		a.state.Error = err.Error()
		a.registrationRetryable = sip.IsTransientRegistrationError(err)
	}
	m.mu.Unlock()
	if err == nil {
		select {
		case a.publicationWake <- struct{}{}:
		default:
		}
	}
	m.notify()
	return err
}

// RetryRegistration registers the account with a new SIP client, as disabling
// and enabling it would. While the account has calls it renews the existing
// registration instead, because replacing the client would end them.
func (m *Manager) RetryRegistration(name string) error {
	if !m.recovery.TryLock() {
		return errors.New("registration recovery is already in progress")
	}
	defer m.recovery.Unlock()
	m.mu.Lock()
	a := m.accounts[name]
	busy := a != nil && m.hasCallsLocked(a)
	m.mu.Unlock()
	if a == nil {
		return errors.New("account is not enabled")
	}
	if !busy {
		return m.renewAccount(a, 0)
	}
	select {
	case <-a.registrationDone:
	default:
		return errors.New("registration is already in progress")
	}
	m.mu.Lock()
	initialError := a.registrationError
	m.mu.Unlock()
	if initialError != nil {
		return m.registerAccount(a)
	}
	ctx, cancel := context.WithTimeout(a.ctx, 15*time.Second)
	defer cancel()
	return a.client.RefreshRegistration(ctx)
}

// RecoverRegistrations runs after resume or a network change. Accounts without
// calls get a new client when they failed or their local address moved; the
// others renew their registration in place.
func (m *Manager) RecoverRegistrations() {
	m.recovery.Lock()
	defer m.recovery.Unlock()
	m.mu.Lock()
	accounts := make([]*account, 0, len(m.accounts))
	for _, a := range m.accounts {
		accounts = append(accounts, a)
	}
	m.mu.Unlock()
	for _, a := range accounts {
		if m.ctx.Err() != nil {
			return
		}
		if a.registrationDone != nil {
			select {
			case <-a.registrationDone:
			case <-a.ctx.Done():
				continue
			}
		}
		m.mu.Lock()
		initialError := a.registrationError
		failed := a.state.State == "failed" && a.registrationRetryable
		busy := m.hasCallsLocked(a)
		m.mu.Unlock()
		if !busy && (failed || !a.client.LocalAddressCurrent()) {
			m.report(m.renewAccount(a, 0))
			continue
		}
		if initialError != nil {
			if sip.IsTransientRegistrationError(initialError) {
				m.registerAccount(a)
			}
			continue
		}
		ctx, cancel := context.WithTimeout(a.ctx, 15*time.Second)
		err := a.client.RefreshRegistration(ctx)
		cancel()
		if err != nil {
			m.report(err)
		} else {
			m.report(a.client.RefreshSubscriptions(a.ctx))
			select {
			case a.publicationWake <- struct{}{}:
			default:
			}
		}
	}
}

// Variables so that tests can shorten them.
var registrationCheckInterval, registrationRetryDelay = 5 * time.Second, 10 * time.Second

func recoveryDelay(attempt int) time.Duration {
	return min(2*time.Minute, registrationRetryDelay<<min(attempt, 4))
}

// superviseRegistration renews the account's SIP client while it has no calls
// and either its registration keeps failing for reasons other than a refusal,
// or the system no longer routes to the registrar from its local address. The
// client binds the address it found when it was created, so after a network
// change only a new client can register again. The renewal ends this worker.
func (m *Manager) superviseRegistration(a *account) {
	ticker := time.NewTicker(registrationCheckInterval)
	defer ticker.Stop()
	attempt := a.recoveryAttempt
	var failingSince, retryAt time.Time
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-ticker.C:
		}
		m.mu.Lock()
		current := m.accounts[a.state.Name] == a
		state, retryable, busy := a.state.State, a.registrationRetryable, m.hasCallsLocked(a)
		m.mu.Unlock()
		if !current {
			return
		}
		if state == "registered" {
			attempt = 0
		}
		if state != "failed" || !retryable {
			failingSince = time.Time{}
		} else if failingSince.IsZero() {
			failingSince = time.Now()
		}
		if busy || time.Now().Before(retryAt) {
			continue
		}
		failing := !failingSince.IsZero() && time.Since(failingSince) >= recoveryDelay(attempt)
		if !failing && (state != "registered" || a.client.LocalAddressCurrent()) {
			continue
		}
		if !m.recovery.TryLock() {
			continue
		}
		attempt++
		err := m.renewAccount(a, attempt)
		m.recovery.Unlock()
		if err != nil {
			retryAt = time.Now().Add(recoveryDelay(attempt))
			m.mu.Lock()
			if m.accounts[a.state.Name] == a {
				a.state.Error = "Could not reconnect: " + err.Error()
			}
			m.mu.Unlock()
			m.notify()
		}
	}
}

// renewAccount replaces the account's SIP client and keeps the status the user
// chose and the Swyx status Voiper wrote. The old account stays when no new
// client can be created, for example while the network is down, so that it can
// be renewed again later.
func (m *Manager) renewAccount(a *account, attempt int) error {
	m.mu.Lock()
	name, cfg := a.state.Name, a.config
	current, busy := m.accounts[name] == a, m.hasCallsLocked(a)
	m.mu.Unlock()
	if !current {
		return errors.New("account is not enabled")
	}
	if busy {
		return errors.New("account has calls")
	}
	// A configured fixed port stays bound until the old client is closed.
	if _, port, _ := net.SplitHostPort(cfg.LocalAddress); port != "" && port != "0" {
		m.Disable(name)
	}
	return m.enable(name, cfg, a, attempt)
}

// Called with Manager.mu held.
func (m *Manager) hasCallsLocked(a *account) bool {
	for _, c := range m.calls {
		if c.owner == a {
			return true
		}
	}
	return false
}

func (m *Manager) RetryAudio(id string) error {
	m.mu.Lock()
	c := m.calls[id]
	if c == nil || c.media == nil {
		m.mu.Unlock()
		return errors.New("call media is not connected")
	}
	stream := c.media
	m.mu.Unlock()
	return stream.RetryAudio()
}

func (m *Manager) RestartMediaPath(id string) error {
	m.mu.Lock()
	c := m.calls[id]
	if c == nil || c.state.State != "connected" || !c.mediaConnected || c.media == nil {
		m.mu.Unlock()
		return errors.New("call media is not connected")
	}
	stream := c.media
	m.mu.Unlock()
	ctx, cancel := context.WithTimeout(m.ctx, 15*time.Second)
	defer cancel()
	var offer []byte
	err := c.owner.client.ReinviteWithOffer(ctx, id, func() ([]byte, error) {
		next, err := stream.RestartOffer(ctx, advertisedIP(c.owner.config))
		if err == nil {
			offer = next
		}
		return next, err
	})
	if err != nil && len(offer) > 0 {
		stream.CancelRestart(offer)
	}
	m.notify()
	return err
}
