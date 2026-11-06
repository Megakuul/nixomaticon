package phone

import (
	"strings"
)

// Telephone display separators carry no dialing meaning. SIP user names and
// feature codes retain their original spelling.
func normalizeDialTarget(target string) string {
	target = strings.TrimSpace(target)
	if strings.ContainsAny(target, "@:") {
		return target
	}
	telephone := true
	for _, r := range target {
		if !strings.ContainsRune("0123456789+*#ABCD().- ", r) {
			telephone = false
			break
		}
	}
	if telephone {
		return strings.NewReplacer("(", "", ")", "", ".", "", "-", "", " ", "").Replace(target)
	}
	return target
}
func (m *Manager) ConversationAddress(accountName, target string) string {
	m.mu.Lock()
	a := m.accounts[accountName]
	m.mu.Unlock()
	target = normalizeDialTarget(target)
	if a == nil || target == "" {
		return target
	}
	return normalizeWatchTarget(target, a.config)
}
