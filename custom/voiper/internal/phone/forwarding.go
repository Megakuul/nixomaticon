package phone

import (
	"context"
	"errors"
	"strings"
	"time"
)

func (m *Manager) forwardCall(c *call, target string) {
	m.mu.Lock()
	if m.calls[c.state.ID] != c || c.state.State != "ringing" {
		m.mu.Unlock()
		return
	}
	c.state.State = "redirecting"
	id := c.state.ID
	m.mu.Unlock()
	m.stopRinging(c)
	m.notify()
	ctx, cancel := context.WithTimeout(m.ctx, 5*time.Second)
	err := c.owner.client.Redirect(ctx, id, normalizeDialTarget(target))
	cancel()
	if err != nil {
		m.restoreRinging(c, err)
		m.report(err)
	}
}
func (m *Manager) DialFeature(accountName, id, name, number string) error {
	a, err := m.getAccount(accountName)
	if err != nil {
		return err
	}
	for _, feature := range a.config.Features {
		if feature.Name != name {
			continue
		}
		target := feature.Target
		if strings.Contains(target, "{number}") {
			if strings.TrimSpace(number) == "" {
				return errors.New("enter the number for this feature")
			}
			target = strings.ReplaceAll(target, "{number}", normalizeDialTarget(number))
		}
		if feature.Action == "transfer" {
			c, err := m.getCall(id)
			if err != nil {
				return err
			}
			if c.owner != a {
				return errors.New("select a call from this account")
			}
			return m.Transfer(id, target)
		}
		return m.Dial(accountName, target)
	}
	return errors.New("configure this PBX feature in account settings first")
}
