package phone

import (
	"context"
	"time"

	"github.com/megakuul/voiper/pkg/media"
)

func (m *Manager) startRingingLocked(c *call, outgoing bool) {
	if c.ringDone != nil {
		select {
		case <-c.ringDone:
		default:
			return
		}
	}
	ctx, cancel := context.WithCancel(m.ctx)
	done := make(chan struct{})
	c.ringCancel, c.ringDone = cancel, done
	settings := m.settings
	go func() {
		defer close(done)
		var stop func()
		var err error
		if outgoing {
			stop, err = media.StartRingback(ctx, settings)
		} else {
			stop, err = media.StartRinging(ctx, settings)
		}
		if err != nil {
			if ctx.Err() == nil {
				m.report(err)
			}
			return
		}
		defer stop()
		<-ctx.Done()
	}()
}
func (m *Manager) stopRinging(c *call) {
	m.mu.Lock()
	cancel, done := c.ringCancel, c.ringDone
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		timer := time.NewTimer(2 * time.Second)
		defer timer.Stop()
		select {
		case <-done:
		case <-timer.C:
		}
	}
}
func (m *Manager) restoreRinging(c *call, err error) {
	m.mu.Lock()
	if m.calls[c.state.ID] == c {
		c.state.State = "ringing"
		c.state.Error = err.Error()
		m.startRingingLocked(c, false)
	}
	m.mu.Unlock()
	m.notify()
}
