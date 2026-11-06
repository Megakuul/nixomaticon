package phone

import (
	"context"
	"errors"
	"net"
	"time"

	wire "github.com/emiago/sipgo/sip"
	"github.com/megakuul/voiper/pkg/sip"
)

type publicationRequest struct {
	ctx       context.Context
	available bool
	note      string
	remove    bool
	result    chan error
}

// PublishStatus sets the own status of an account. Swyx accounts write it
// through the client API in the background; other accounts publish PIDF.
// While DND is on or the screen is locked, the status is published as away.
func (m *Manager) PublishStatus(accountName string, available bool, note string) error {
	if len(note) > 512 {
		return errors.New("status note is too long")
	}
	a, err := m.getAccount(accountName)
	if err != nil {
		return err
	}
	m.mu.Lock()
	a.status = ownStatus{set: true, away: !available, note: note}
	session := a.swyx
	m.mu.Unlock()
	if session != nil {
		select {
		case session.wake <- struct{}{}:
		default:
		}
		m.notify()
		return nil
	}
	return m.publishOwnStatus(accountName)
}

func (m *Manager) publishOwnStatus(accountName string) error {
	m.mu.Lock()
	a := m.accounts[accountName]
	if a == nil {
		m.mu.Unlock()
		return errors.New("account is not enabled")
	}
	available := !a.status.away && !m.screenLocked && !m.dnd
	note := a.status.note
	m.mu.Unlock()
	return m.requestPublication(accountName, publicationRequest{available: available, note: note})
}

func (m *Manager) UnpublishStatus(accountName string) error {
	a, err := m.getAccount(accountName)
	if err != nil {
		return err
	}
	m.mu.Lock()
	session := a.swyx
	if session != nil {
		// Swyx keeps a status while registered; reset it instead.
		a.status = ownStatus{set: true}
	} else {
		a.status = ownStatus{}
	}
	m.mu.Unlock()
	if session != nil {
		select {
		case session.wake <- struct{}{}:
		default:
		}
		return nil
	}
	return m.requestPublication(accountName, publicationRequest{remove: true})
}

func (m *Manager) requestPublication(accountName string, request publicationRequest) error {
	a, err := m.getAccount(accountName)
	if err != nil {
		return err
	}
	if a.config.PresenceMode == "disabled" {
		return errors.New("presence is disabled for this account")
	}
	ctx, cancel := context.WithTimeout(a.ctx, 15*time.Second)
	defer cancel()
	request.ctx, request.result = ctx, make(chan error, 1)
	select {
	case a.publications <- request:
	case <-ctx.Done():
		return ctx.Err()
	case <-a.publicationDone:
		return context.Canceled
	}
	select {
	case err := <-request.result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-a.publicationDone:
		return context.Canceled
	}
}

// One worker owns the tag, desired state and timer for each account. User edits
// and refreshes cannot race and overwrite the server's latest entity tag.
func (m *Manager) maintainPublication(a *account, workerContext context.Context) {
	defer close(a.publicationDone)
	var lease sip.Publication
	var expires time.Time
	var retryAfter time.Time
	var desired publicationRequest
	var needsBody bool
	var failures int
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	defer timer.Stop()
	var refresh <-chan time.Time
	defer func() {
		if lease.EntityTag != "" {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_, _ = a.client.RefreshPresence(ctx, lease.EntityTag, 0)
		}
	}()
	for {
		var request *publicationRequest
		select {
		case <-workerContext.Done():
			return
		case next := <-a.publications:
			if err := next.ctx.Err(); err != nil {
				next.result <- err
				continue
			}
			desired = next
			request = &next
			needsBody = true
			failures = 0
		case <-refresh:
		case <-a.publicationWake:
			if refresh == nil || time.Now().Before(retryAfter) {
				continue
			}
		}
		timer.Stop()
		refresh = nil
		if !expires.IsZero() && !time.Now().Before(expires) {
			lease = sip.Publication{}
		}
		ctx := workerContext
		if request != nil {
			ctx = request.ctx
		}
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		stopCancellation := context.AfterFunc(workerContext, cancel)
		started := time.Now()
		var updated sip.Publication
		var err error
		switch {
		case desired.remove:
			if lease.EntityTag != "" {
				updated, err = a.client.RefreshPresence(ctx, lease.EntityTag, 0)
			}
		case needsBody || lease.EntityTag == "":
			updated, err = a.client.PublishPresenceLease(ctx, desired.available, desired.note, lease.EntityTag, time.Hour)
		default:
			updated, err = a.client.RefreshPresence(ctx, lease.EntityTag, time.Hour)
		}
		var response *sip.ResponseError
		if errors.As(err, &response) && response.Status == 412 {
			lease = sip.Publication{}
			if desired.remove {
				err = nil
			} else {
				updated, err = a.client.PublishPresenceLease(ctx, desired.available, desired.note, "", time.Hour)
			}
		}
		stopCancellation()
		cancel()
		state, detail := "", ""
		if err == nil {
			lease = updated
			// Wall time includes time spent suspended; Linux's monotonic clock does not.
			expires = started.Add(lease.Expires).Round(0)
			needsBody = false
			failures = 0
			retryAfter = time.Time{}
			if !desired.remove {
				state = "unavailable"
				if desired.available {
					state = "available"
				}
				timer.Reset(max(100*time.Millisecond, time.Until(started.Add(lease.Expires*4/5))))
				refresh = timer.C
			}
		} else {
			state, detail = "unknown", err.Error()
			if retryPublication(err) {
				failures++
				delay := time.Second * time.Duration(1<<min(failures, 6))
				if errors.As(err, &response) {
					delay = max(delay, response.RetryAfter)
				}
				retryAfter = time.Now().Add(delay)
				timer.Reset(delay)
				refresh = timer.C
			}
		}
		m.mu.Lock()
		if m.accounts[a.state.Name] == a {
			a.state.PresenceState, a.state.PresenceError = state, detail
			a.state.PresenceNote = desired.note
		}
		m.mu.Unlock()
		m.notify()
		if request != nil {
			request.result <- err
		}
	}
}

func retryPublication(err error) bool {
	var response *sip.ResponseError
	if errors.As(err, &response) {
		switch response.Status {
		case 408, 500, 502, 503, 504:
			return true
		}
		return false
	}
	var network net.Error
	return errors.As(err, &network) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, wire.ErrTransactionTransport) || errors.Is(err, wire.ErrTransactionTimeout)
}
