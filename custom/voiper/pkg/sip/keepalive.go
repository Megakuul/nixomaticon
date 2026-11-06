package sip

import (
	"context"
	"errors"
	"time"

	wire "github.com/emiago/sipgo/sip"
)

func (c *Client) keepalive(ctx context.Context) {
	defer c.workers.Done()
	ticker := time.NewTicker(c.config.KeepAliveInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.ctx.Done():
			return
		case <-ticker.C:
		}
		c.mu.Lock()
		active := c.registered && !c.closed
		c.mu.Unlock()
		if !active {
			return
		}
		probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		previous := c.LocalAddr()
		var err error
		if c.config.Transport != "udp" {
			err = c.connectFlow(probeCtx, "")
		}
		if err == nil {
			_, err = c.do(probeCtx, c.request(wire.OPTIONS, c.registrar, nil))
			// A SIP rejection still proves that the remote flow is reachable.
			var response *ResponseError
			if errors.As(err, &response) {
				err = nil
			}
		}
		if (err != nil || previous != c.LocalAddr()) && ctx.Err() == nil && c.ctx.Err() == nil {
			cancel()
			probeCtx, cancel = context.WithTimeout(ctx, 15*time.Second)
			if err = c.RefreshRegistration(probeCtx); err != nil && !errors.Is(err, ErrRegistrationNotRunning) {
				c.emit(registrationFailure(err))
			}
		}
		cancel()
	}
}
