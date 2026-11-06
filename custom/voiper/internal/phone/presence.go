package phone

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	wire "github.com/emiago/sipgo/sip"
	"github.com/megakuul/voiper/internal/compat/swyx"
	"github.com/megakuul/voiper/internal/config"
)

type Presence struct {
	Target                                               string
	Account, Remote, State, Note, Source, SubscriptionID string
	Updated                                              time.Time
	Voicemail                                            *Voicemail `json:",omitempty"`
	dialogs                                              map[string]string
	dialogVersion                                        int
	hasDialogState                                       bool
	watchContext                                         context.Context
	cancelWatch                                          context.CancelFunc
	// automatic watches follow the contacts being browsed; lastWanted orders their eviction.
	automatic  bool
	lastWanted time.Time
}

// automaticWatchLimit leaves room for explicit and voicemail watches below the overall limit.
const automaticWatchLimit = 100

type Voicemail struct {
	Account, State                 string
	Waiting                        bool
	New, Old, UrgentNew, UrgentOld int
	Updated                        time.Time
}

func normalizeWatchTarget(target string, cfg config.Config) string {
	target = strings.TrimSpace(target)
	if !strings.Contains(target, ":") {
		if !strings.Contains(target, "@") {
			domain := cfg.Domain
			if domain == "" {
				domain = cfg.Server
			}
			target += "@" + domain
		}
		target = "sip:" + target
	}
	var uri wire.Uri
	if err := wire.ParseUri(target, &uri); err != nil {
		return target
	}
	if (uri.Scheme == "sip" && uri.Port == 5060) || (uri.Scheme == "sips" && uri.Port == 5061) {
		uri.Port = 0
	}
	return (&wire.Uri{Scheme: strings.ToLower(uri.Scheme), User: uri.User, Host: strings.ToLower(uri.Host), Port: uri.Port}).String()
}

func watchKey(accountName, target, eventPackage string) string {
	if eventPackage == "message-summary" {
		return accountName + "\x00message-summary\x00" + target
	}
	return accountName + "\x00" + target
}

func (m *Manager) Watch(accountName, target string) error {
	a, err := m.getAccount(accountName)
	if err != nil {
		return err
	}
	if a.config.PresenceMode == "disabled" {
		return errors.New("presence is disabled for this account")
	}
	eventPackage, accept := "presence", "application/pidf+xml"
	if a.config.PresenceMode == "dialog" {
		eventPackage, accept = "dialog", "application/dialog-info+xml"
	}
	return m.watch(accountName, a, target, eventPackage, accept, false)
}

// WatchVisible subscribes to the status of contacts currently shown to the user.
// Earlier automatic watches remain until newer ones exceed automaticWatchLimit,
// so returning to a previous search shows status immediately. Subscriptions
// run in the background; failures are recorded on the entry instead of reported.
func (m *Manager) WatchVisible(accountName string, targets []string) error {
	a, err := m.getAccount(accountName)
	if err != nil {
		return err
	}
	if a.config.PresenceMode == "disabled" {
		return nil
	}
	eventPackage, accept := "presence", "application/pidf+xml"
	if a.config.PresenceMode == "dialog" {
		eventPackage, accept = "dialog", "application/dialog-info+xml"
	}
	now := time.Now()
	wanted := map[string]bool{}
	var pending []string
	var evicted []Presence
	m.mu.Lock()
	if m.accounts[accountName] != a {
		m.mu.Unlock()
		return errors.New("account changed")
	}
	for _, target := range targets {
		target = strings.TrimSpace(target)
		key := watchKey(accountName, normalizeWatchTarget(target, a.config), eventPackage)
		if target == "" || wanted[key] || len(wanted) >= automaticWatchLimit {
			continue
		}
		wanted[key] = true
		if p, exists := m.presence[key]; exists {
			if p.automatic {
				p.lastWanted = now
				m.presence[key] = p
			}
			continue
		}
		pending = append(pending, target)
	}
	var candidates []string
	automatic := 0
	for key, p := range m.presence {
		if p.Account != accountName || !p.automatic {
			continue
		}
		automatic++
		if !wanted[key] {
			candidates = append(candidates, key)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		return m.presence[candidates[i]].lastWanted.Before(m.presence[candidates[j]].lastWanted)
	})
	for _, key := range candidates {
		if automatic+len(pending) <= automaticWatchLimit {
			break
		}
		evicted = append(evicted, m.presence[key])
		delete(m.presence, key)
		automatic--
	}
	m.mu.Unlock()
	if len(evicted) == 0 && len(pending) == 0 {
		return nil
	}
	m.notify()
	go func() {
		for _, p := range evicted {
			_ = m.endWatch(a, p)
		}
		// A few parallel subscriptions keep a page of contacts quick without flooding the server.
		queue := make(chan string)
		var workers sync.WaitGroup
		for range 4 {
			workers.Add(1)
			go func() {
				defer workers.Done()
				for target := range queue {
					_ = m.watch(accountName, a, target, eventPackage, accept, true)
				}
			}()
		}
		for _, target := range pending {
			if a.ctx.Err() != nil {
				break
			}
			queue <- target
		}
		close(queue)
		workers.Wait()
	}()
	return nil
}

func (m *Manager) WatchVoicemail(accountName string) error {
	a, err := m.getAccount(accountName)
	if err != nil {
		return err
	}
	return m.watch(accountName, a, a.config.Username, "message-summary", "application/simple-message-summary", false)
}

func (m *Manager) watch(accountName string, a *account, target, eventPackage, accept string, automatic bool) error {
	originalTarget := strings.TrimSpace(target)
	target = normalizeWatchTarget(target, a.config)
	key := watchKey(accountName, target, eventPackage)
	m.mu.Lock()
	if m.accounts[accountName] != a {
		m.mu.Unlock()
		return errors.New("account changed")
	}
	existing, exists := m.presence[key]
	if exists && (existing.State != "unknown" || existing.SubscriptionID != "") {
		if !automatic && existing.automatic {
			existing.automatic = false
			m.presence[key] = existing
		}
		m.mu.Unlock()
		return nil
	}
	if !exists && len(m.presence) >= 128 {
		m.mu.Unlock()
		return errors.New("presence watch limit reached")
	}
	if existing.cancelWatch != nil {
		existing.cancelWatch()
	}
	ctx, cancel := context.WithCancel(a.ctx)
	entry := Presence{Account: accountName, Remote: target, Target: originalTarget, State: "subscribing", Source: eventPackage, Updated: time.Now(), watchContext: ctx, cancelWatch: cancel, automatic: automatic, lastWanted: time.Now()}
	if eventPackage == "message-summary" {
		entry.Voicemail = &Voicemail{Account: accountName, State: "subscribing"}
	}
	m.presence[key] = entry
	m.mu.Unlock()
	id, err := a.client.Subscribe(ctx, target, eventPackage, accept)
	m.mu.Lock()
	current, exists := m.presence[key]
	if m.accounts[accountName] == a && exists && current.watchContext == ctx {
		if err != nil && automatic {
			// Keep the failure visible and avoid resubscribing on every page load.
			current.State = "unavailable"
			current.Note = err.Error()
			current.Updated = time.Now()
			m.presence[key] = current
		} else if err != nil {
			delete(m.presence, key)
		} else {
			entry := m.presence[key]
			entry.SubscriptionID = id
			m.presence[key] = entry
		}
	} else {
		cancel()
	}
	m.mu.Unlock()
	if err != nil {
		cancel()
		if automatic {
			m.notify()
		}
		return err
	}
	m.notify()
	return nil
}

func (m *Manager) Unwatch(accountName, target string) error {
	m.mu.Lock()
	a := m.accounts[accountName]
	if a == nil {
		m.mu.Unlock()
		return nil
	}
	key := watchKey(accountName, normalizeWatchTarget(target, a.config), "presence")
	p := m.presence[key]
	delete(m.presence, key)
	m.mu.Unlock()
	m.notify()
	return m.endWatch(a, p)
}

// endWatch stops a subscription already removed from the presence map.
func (m *Manager) endWatch(a *account, p Presence) error {
	if p.cancelWatch != nil {
		defer p.cancelWatch()
	}
	if p.SubscriptionID == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(a.ctx, 5*time.Second)
	defer cancel()
	return a.client.Unsubscribe(ctx, p.SubscriptionID)
}

// handlePresenceLocked applies a notification while the manager lock is held.
func (m *Manager) handlePresenceLocked(ev event) {
	e := ev.sip
	remote := normalizeWatchTarget(e.RemoteURI, ev.owner.config)
	key := watchKey(ev.account, remote, e.State)
	for existingKey, p := range m.presence {
		if p.Account == ev.account && e.CallID != "" && p.SubscriptionID == e.CallID {
			key = existingKey
			remote = p.Remote
			break
		}
	}
	old := m.presence[key]
	if old.Remote == "" || (old.SubscriptionID != "" && e.CallID != "" && old.SubscriptionID != e.CallID) {
		return
	}
	if e.State == "unknown" {
		if old.Remote == "" {
			return
		}
		old.State = "unknown"
		old.dialogs = nil
		old.dialogVersion = 0
		old.hasDialogState = false
		old.Note = e.Message
		old.Updated = time.Now()
		if old.Voicemail != nil {
			voicemail := *old.Voicemail
			voicemail.State = "unknown"
			voicemail.Updated = old.Updated
			old.Voicemail = &voicemail
		}
		m.presence[key] = old
		return
	}
	var p Presence
	var err error
	switch e.State {
	case "presence":
		if ev.owner.config.PresenceMode == "disabled" {
			return
		}
		p, err = parsePresence(e.Body, ev.owner.config.PresenceMode)
	case "dialog":
		if ev.owner.config.PresenceMode == "disabled" {
			return
		}
		p, err = applyDialogInfo(e.Body, old)
	case "message-summary":
		var voicemail Voicemail
		voicemail, err = parseMessageSummary(e.Body)
		voicemail.Account = ev.account
		p = Presence{Remote: remote, State: voicemail.State, Source: "message-summary", Updated: voicemail.Updated, Voicemail: &voicemail}
	default:
		return
	}
	if err != nil {
		if old.Remote != "" {
			old.State = "unknown"
			old.Note = err.Error()
			old.Updated = time.Now()
			m.presence[key] = old
		}
		return
	}
	p.Account = ev.account
	p.Target = old.Target
	p.watchContext, p.cancelWatch = old.watchContext, old.cancelWatch
	p.automatic, p.lastWanted = old.automatic, old.lastWanted
	p.SubscriptionID = e.CallID
	p.Remote = old.Remote
	m.presence[key] = p
}

func parsePresence(body []byte, mode string) (Presence, error) {
	if len(body) > 65536 {
		return Presence{}, errors.New("presence document exceeds 64 KiB")
	}
	if mode == "auto" || mode == "swyx" || mode == "" {
		p, err := swyx.ParsePresence(body)
		if err == nil {
			return Presence{Remote: p.Entity, State: p.State, Note: p.Note, Source: p.Source, Updated: time.Now()}, nil
		}
		if mode == "swyx" || !errors.Is(err, swyx.ErrUnsupportedPresence) {
			return Presence{}, err
		}
	}
	var doc struct {
		XMLName xml.Name `xml:"urn:ietf:params:xml:ns:pidf presence"`
		Entity  string   `xml:"entity,attr"`
		Tuples  []struct {
			Status struct {
				Basic string `xml:"urn:ietf:params:xml:ns:pidf basic"`
			} `xml:"urn:ietf:params:xml:ns:pidf status"`
			Note string `xml:"urn:ietf:params:xml:ns:pidf note"`
		} `xml:"urn:ietf:params:xml:ns:pidf tuple"`
		Note string `xml:"urn:ietf:params:xml:ns:pidf note"`
	}
	if err := decodePresenceXML(body, &doc); err != nil {
		return Presence{}, err
	}
	if strings.TrimSpace(doc.Entity) == "" {
		return Presence{}, errors.New("presence entity is missing")
	}
	p := Presence{Remote: doc.Entity, State: "unknown", Note: doc.Note, Source: "standard", Updated: time.Now()}
	for _, tuple := range doc.Tuples {
		switch strings.TrimSpace(tuple.Status.Basic) {
		case "open":
			p.State = "available"
		case "closed":
			if p.State != "available" {
				p.State = "offline"
			}
		}
		if p.Note == "" {
			p.Note = tuple.Note
		}
	}
	return p, nil
}

func decodePresenceXML(body []byte, value any) error {
	if len(body) > 65536 {
		return errors.New("presence document exceeds 64 KiB")
	}
	decoder := xml.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(value); err != nil {
		return err
	}
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if text, ok := token.(xml.CharData); !ok || strings.TrimSpace(string(text)) != "" {
			return errors.New("unexpected content after presence document")
		}
	}
}

func applyDialogInfo(body []byte, previous Presence) (Presence, error) {
	var doc struct {
		XMLName xml.Name `xml:"urn:ietf:params:xml:ns:dialog-info dialog-info"`
		Entity  string   `xml:"entity,attr"`
		State   string   `xml:"state,attr"`
		Version *int     `xml:"version,attr"`
		Dialogs []struct {
			ID    string `xml:"id,attr"`
			State string `xml:"urn:ietf:params:xml:ns:dialog-info state"`
		} `xml:"urn:ietf:params:xml:ns:dialog-info dialog"`
	}
	if err := decodePresenceXML(body, &doc); err != nil {
		return Presence{}, err
	}
	if doc.Entity == "" || doc.Version == nil || *doc.Version < 0 || (doc.State != "full" && doc.State != "partial") {
		return Presence{}, errors.New("invalid dialog-info identity, version or state")
	}
	if previous.dialogs != nil && *doc.Version <= previous.dialogVersion {
		return previous, nil
	}
	p := Presence{Remote: doc.Entity, Source: "dialog", State: "unknown", Updated: time.Now(), dialogVersion: *doc.Version, dialogs: make(map[string]string), hasDialogState: doc.State == "full"}
	if doc.State == "partial" {
		if previous.dialogs == nil || *doc.Version != previous.dialogVersion+1 {
			p.Note = "dialog updates are incomplete; waiting for a full update"
			return p, nil
		}
		p.hasDialogState = previous.hasDialogState
		for id, state := range previous.dialogs {
			p.dialogs[id] = state
		}
	}
	for _, dialog := range doc.Dialogs {
		if dialog.ID == "" {
			return Presence{}, errors.New("dialog ID is missing")
		}
		switch dialog.State {
		case "terminated":
			delete(p.dialogs, dialog.ID)
		case "trying", "proceeding", "early", "confirmed":
			if _, exists := p.dialogs[dialog.ID]; !exists && len(p.dialogs) >= 256 {
				return Presence{}, errors.New("dialog state exceeds 256 calls")
			}
			p.dialogs[dialog.ID] = dialog.State
		default:
			return Presence{}, fmt.Errorf("unsupported dialog state %q", dialog.State)
		}
	}
	if p.hasDialogState {
		p.State = "available"
	}
	for _, state := range p.dialogs {
		if state == "confirmed" {
			p.State = "in-call"
			break
		}
		p.State = "ringing"
	}
	return p, nil
}

func parseMessageSummary(body []byte) (Voicemail, error) {
	result := Voicemail{State: "unknown", Updated: time.Now()}
	if len(body) > 65536 {
		return result, errors.New("message summary exceeds 64 KiB")
	}
	waitingSeen := false
	for _, line := range strings.Split(string(body), "\n") {
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch strings.ToLower(strings.TrimSpace(name)) {
		case "messages-waiting":
			switch strings.ToLower(value) {
			case "yes":
				result.Waiting = true
				result.State = "messages-waiting"
			case "no":
				result.State = "no-messages"
			default:
				return result, errors.New("invalid Messages-Waiting value")
			}
			waitingSeen = true
		case "voice-message":
			fields := strings.Fields(value)
			if len(fields) < 1 || len(fields) > 2 {
				return result, errors.New("invalid voice message counts")
			}
			counts := []*int{&result.New, &result.Old, &result.UrgentNew, &result.UrgentOld}
			for i, field := range fields {
				if i == 1 {
					if !strings.HasPrefix(field, "(") || !strings.HasSuffix(field, ")") {
						return result, errors.New("invalid urgent message counts")
					}
					field = strings.Trim(field, "()")
				}
				pair := strings.Split(field, "/")
				if len(pair) != 2 {
					return result, errors.New("invalid voice message counts")
				}
				for j, number := range pair {
					n, err := strconv.Atoi(number)
					if err != nil || n < 0 {
						return result, errors.New("invalid voice message count")
					}
					*counts[i*2+j] = n
				}
			}
		}
	}
	if !waitingSeen {
		return result, errors.New("message summary has no Messages-Waiting field")
	}
	return result, nil
}
