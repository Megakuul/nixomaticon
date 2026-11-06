package phone

import (
	"context"
	"errors"

	"github.com/megakuul/voiper/pkg/media"
	"github.com/megakuul/voiper/pkg/sip"
)

// Each fork owns a prepared transport until SIP selects its final dialog.
// NewCall closes unselected transports when SIP cancels their branch contexts.
func (m *Manager) prepareDelayedAnswer(ctx context.Context, c *call, settings media.Settings, offer []byte) (sip.DelayedAnswer, error) {
	stream, err := media.NewCall(ctx, c.state.ID, settings, offer)
	if err != nil {
		return sip.DelayedAnswer{}, err
	}
	answer := stream.LocalSDP(advertisedIP(c.owner.config))
	return sip.DelayedAnswer{SDP: answer, Select: func() error {
		m.mu.Lock()
		if ctx.Err() != nil || m.calls[c.state.ID] != c || m.accounts[c.state.Account] != c.owner {
			m.mu.Unlock()
			stream.Close()
			return errors.New("call ended while selecting the SDP answer")
		}
		c.media = stream
		stream.SetMuted(c.state.Muted)
		c.offer = answer
		m.mu.Unlock()
		return nil
	}}, nil
}
