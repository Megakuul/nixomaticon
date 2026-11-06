package phone

import (
	"context"
	"errors"
	"time"
)

type outgoingTransfer struct {
	consultation *call
}

func (m *Manager) sendTransfer(first, consultation *call, send func(context.Context) error) error {
	m.mu.Lock()
	if m.calls[first.state.ID] != first || first.state.State != "connected" || first.outgoingTransfer != nil {
		m.mu.Unlock()
		return errors.New("transfer requires a connected call without a pending transfer")
	}
	if consultation != nil && (consultation == first || m.calls[consultation.state.ID] != consultation || consultation.state.State != "connected") {
		m.mu.Unlock()
		return errors.New("consultation call is no longer connected")
	}
	transfer := &outgoingTransfer{consultation: consultation}
	first.outgoingTransfer = transfer
	first.state.TransferStatus = "pending"
	m.mu.Unlock()
	m.notify()
	ctx, cancel := context.WithTimeout(m.ctx, 15*time.Second)
	defer cancel()
	err := send(ctx)
	if err != nil {
		m.mu.Lock()
		if first.outgoingTransfer == transfer {
			first.outgoingTransfer = nil
			first.state.TransferStatus = "failed"
		}
		m.mu.Unlock()
		m.notify()
	}
	return err
}

// REFER acceptance alone does not end a call. The SIP subscription must report
// that the replacement INVITE succeeded before releasing either original leg.
func (m *Manager) finishTransferred(c *call) {
	m.mu.Lock()
	if m.calls[c.state.ID] != c {
		m.mu.Unlock()
		return
	}
	delete(m.calls, c.state.ID)
	m.mu.Unlock()
	ctx, cancel := context.WithTimeout(m.ctx, 5*time.Second)
	defer cancel()
	err := c.owner.client.Hangup(ctx, c.state.ID)
	m.finish(c, "transferred")
	m.report(err)
	m.notify()
}

func (m *Manager) receiveTransfer(ctx context.Context, owner *account, id, target string) (result error) {
	m.mu.Lock()
	original := m.calls[id]
	if original == nil || original.owner != owner {
		m.mu.Unlock()
		return errors.New("transfer call is no longer active")
	}
	wasHeld := original.state.Held
	name := original.state.Account
	m.mu.Unlock()
	defer func() {
		if result != nil && !wasHeld {
			m.Hold(id, false)
		}
	}()
	replacementID, err := m.dial(name, target, "")
	if err != nil {
		return err
	}
	m.mu.Lock()
	replacement := m.calls[replacementID]
	m.mu.Unlock()
	if replacement == nil {
		return errors.New("replacement call ended")
	}
	select {
	case <-replacement.connected:
		m.mu.Lock()
		active := m.calls[replacementID] == replacement && !replacement.finished && replacement.state.State == "connected"
		m.mu.Unlock()
		if active {
			return nil
		}
		err = errors.New("replacement call ended before transfer completed")
	case <-replacement.ended:
		err = errors.New("replacement call failed")
	case <-ctx.Done():
		m.Hangup(replacementID)
		err = ctx.Err()
	}
	return err
}

func (m *Manager) restoreReplacedMute(c *call) {
	m.mu.Lock()
	original := c.restoreMute
	c.restoreMute = nil
	if c.replaces == "" || original == nil || m.calls[original.state.ID] != original {
		m.mu.Unlock()
		return
	}
	original.state.Muted = false
	if original.media != nil {
		original.media.SetMuted(false)
	}
	m.mu.Unlock()
}
