package phone

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/megakuul/voiper/internal/compat/swyx"
	"github.com/megakuul/voiper/internal/compat/swyx/cds"
	"github.com/megakuul/voiper/internal/config"
	"github.com/megakuul/voiper/internal/store"
	"github.com/megakuul/voiper/pkg/media"
	"github.com/megakuul/voiper/pkg/sip"
)

type Notice struct{ ID, Account, Remote string }
type AccountState struct {
	PresenceState, PresenceNote, PresenceError string
	Features                                   []config.FeatureCode
	Forwarding                                 bool
	Name, State, Error                         string
	Capabilities                               swyx.Capabilities
	// SwyxAPI is "", connecting, connected or failed; see swyx.go.
	SwyxAPI, SwyxAPIError string
}
type CallState struct {
	ID, Account, Remote, Direction, State, Error string
	TransferStatus                               string
	// ConsultationFor is the held call this call may be transferred to.
	ConsultationFor string
	Muted, Held     bool
	Started         time.Time
	Connected       time.Time
	Stats           media.Stats
}
type Snapshot struct {
	UnreadMessages int
	Accounts       []AccountState
	Calls          []CallState
	DND            bool
	ScreenLocked   bool
	Audio          media.Settings
	Presence       []Presence
}
type account struct {
	ctx               context.Context
	registrationDone  chan struct{}
	registrationError error
	// Guarded by Manager.mu. A failure is retryable unless the registrar refused
	// the account; recoveryAttempt counts client renewals that led to this one.
	registrationRetryable bool
	recoveryAttempt       int
	// swyxApplied is what Voiper last wrote to the Swyx status, kept across
	// client renewals so that an away set for the screen lock is still cleared.
	swyxApplied       swyxStatus
	publications      chan publicationRequest
	publicationDone   chan struct{}
	publicationCancel context.CancelFunc
	publicationWake   chan struct{}
	client            *sip.Client
	config            config.Config
	cancel            context.CancelFunc
	state             AccountState
	// Guarded by Manager.mu. allow is nil until the registrar answered OPTIONS.
	allow        []string
	probed       bool
	status       ownStatus
	swyx         *swyxSession
	swyxPresence map[string]cds.Presence
}
type call struct {
	connected, ended chan struct{}
	mediaConnected   bool
	replaces         string
	restoreMute      *call
	finished         bool
	forwardTimer     *time.Timer
	outgoingTransfer *outgoingTransfer
	earlyDone        chan struct{}
	state            CallState
	media            *media.Call
	offer            []byte
	cancel           context.CancelFunc
	owner            *account
	ringCancel       context.CancelFunc
	ringDone         chan struct{}
}
type event struct {
	account string
	owner   *account
	sip     sip.Event
}
type Manager struct {
	transferWorkers sync.WaitGroup
	recovery        sync.Mutex
	activation      sync.Mutex
	ctx             context.Context
	cancel          context.CancelFunc
	mu              sync.Mutex
	presence        map[string]Presence
	accounts        map[string]*account
	calls           map[string]*call
	settings        media.Settings
	dnd             bool
	screenLocked    bool
	store           *store.Store
	emit            func(string, any)
	events          chan event
	done            chan struct{}
}

func New(ctx context.Context, s *store.Store, emit func(string, any)) *Manager {
	ctx, cancel := context.WithCancel(ctx)
	m := &Manager{ctx: ctx, cancel: cancel, accounts: map[string]*account{}, presence: map[string]Presence{}, calls: map[string]*call{}, store: s, emit: emit, events: make(chan event, 128), done: make(chan struct{})}
	go func() {
		defer close(m.done)
		for {
			select {
			case <-ctx.Done():
				return
			case e := <-m.events:
				m.handle(e)
			}
		}
	}()
	return m
}
func (m *Manager) notify() {
	if m.emit != nil {
		m.emit("phone-changed", nil)
	}
}
func (m *Manager) report(err error) {
	if err != nil && m.emit != nil {
		m.emit("phone-error", err.Error())
	}
}
func (m *Manager) Snapshot() Snapshot {
	m.mu.Lock()
	s := Snapshot{Accounts: []AccountState{}, Calls: []CallState{}, DND: m.dnd, ScreenLocked: m.screenLocked, Audio: m.settings}
	streams := map[string]*media.Call{}
	for _, a := range m.accounts {
		s.Accounts = append(s.Accounts, a.state)
	}
	for _, c := range m.calls {
		s.Calls = append(s.Calls, c.state)
		streams[c.state.ID] = c.media
	}
	for _, p := range m.presence {
		if a := m.accounts[p.Account]; a != nil && a.swyxPresence != nil && p.Voicemail == nil {
			p = withSwyxPresence(p, a.swyxPresence)
		}
		s.Presence = append(s.Presence, p)
	}
	m.mu.Unlock()
	for i := range s.Calls {
		if stream := streams[s.Calls[i].ID]; stream != nil {
			s.Calls[i].Stats = stream.Stats()
		}
	}
	sort.Slice(s.Accounts, func(i, j int) bool { return s.Accounts[i].Name < s.Accounts[j].Name })
	sort.Slice(s.Calls, func(i, j int) bool { return s.Calls[i].Started.Before(s.Calls[j].Started) })
	return s
}
func (m *Manager) Enable(name string, cfg config.Config) error {
	return m.enable(name, cfg, nil, 0)
}

// enable keeps the user's status and Voiper's Swyx writes of a previous account.
func (m *Manager) enable(name string, cfg config.Config, previous *account, recoveryAttempt int) error {
	if err := config.Validate(cfg); err != nil {
		return err
	}
	tlsConfig, err := accountTLS(cfg.TLSCAFile)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(m.ctx)
	a := &account{publications: make(chan publicationRequest), publicationDone: make(chan struct{}), publicationWake: make(chan struct{}, 1), registrationDone: make(chan struct{}), recoveryAttempt: recoveryAttempt, ctx: ctx, config: cfg, cancel: cancel, state: AccountState{Name: name, State: "registering", Features: cfg.Features, Forwarding: cfg.ForwardAlways != "" || cfg.ForwardBusy != "" || cfg.ForwardNoAnswer != "", Capabilities: swyx.Detect("", nil, cfg.PresenceMode, cfg.MessagingMode)}}
	if previous != nil {
		m.mu.Lock()
		a.status, a.swyxApplied = previous.status, previous.swyxApplied
		m.mu.Unlock()
	}
	client, err := sip.NewClient(sip.Config{Server: cfg.Server, Port: cfg.Port, Domain: cfg.Domain, Username: cfg.Username, AuthUsername: cfg.AuthUsername, Password: cfg.Password, DisplayName: cfg.DisplayName, Transport: cfg.Transport, LocalAddress: cfg.LocalAddress, OutboundProxy: cfg.OutboundProxy, Expires: time.Hour, MaxRedirects: cfg.MaxRedirects, TLSConfig: tlsConfig}, func(e sip.Event) {
		select {
		case m.events <- event{name, a, e}:
		case <-ctx.Done():
		}
	})
	if err != nil {
		cancel()
		return err
	}
	publicationContext, publicationCancel := context.WithCancel(ctx)
	a.publicationCancel = publicationCancel
	a.client = client
	client.SetTransferHandler(func(ctx context.Context, id, target string) error { return m.receiveTransfer(ctx, a, id, target) })
	client.SetOfferHandler(func(id string, offer []byte) ([]byte, error) {
		m.mu.Lock()
		c := m.calls[id]
		var stream *media.Call
		if c != nil && c.owner == a && c.mediaConnected {
			stream = c.media
		}
		m.mu.Unlock()
		if stream == nil {
			return nil, errors.New("media session is not ready")
		}
		var answer []byte
		var err error
		if len(offer) == 0 {
			answer = stream.CurrentOffer(advertisedIP(cfg))
		} else {
			answer, err = stream.AnswerOffer(offer, advertisedIP(cfg))
		}
		if err != nil {
			return nil, err
		}
		m.mu.Lock()
		if m.calls[id] == c {
			c.offer = append([]byte(nil), answer...)
		}
		m.mu.Unlock()
		m.notify()
		return answer, nil
	})
	client.SetEarlyOfferHandler(func(id string, offer []byte) ([]byte, error) {
		m.mu.Lock()
		c := m.calls[id]
		var stream *media.Call
		if c != nil && c.owner == a && c.earlyDone != nil && !c.mediaConnected && (c.state.State == "calling" || c.state.State == "ringback") {
			stream = c.media
		}
		m.mu.Unlock()
		if stream == nil {
			return nil, errors.New("early media session is not ready")
		}
		return stream.AnswerEarlyOffer(offer, advertisedIP(cfg))
	})
	client.SetAnswerHandler(func(id string, answer []byte) error {
		m.mu.Lock()
		c := m.calls[id]
		var stream *media.Call
		if c != nil && c.owner == a && c.mediaConnected {
			stream = c.media
		}
		m.mu.Unlock()
		if stream == nil {
			return errors.New("media session is not ready")
		}
		return stream.ValidateAnswer(answer)
	})
	client.SetAnswerCommitHandler(func(id string, offer, answer []byte) error {
		m.mu.Lock()
		c := m.calls[id]
		var stream *media.Call
		if c != nil && c.owner == a && c.mediaConnected {
			stream = c.media
		}
		m.mu.Unlock()
		if stream == nil {
			return errors.New("media session is not ready")
		}
		err := stream.AcceptAnswer(offer, answer)
		stats := stream.Stats()
		m.mu.Lock()
		if m.calls[id] == c {
			c.state.Error = ""
			if err != nil {
				c.state.Error = err.Error()
			} else {
				c.state.Held = stats.Held
				c.offer = append([]byte(nil), offer...)
			}
		}
		m.mu.Unlock()
		m.notify()
		return err
	})
	m.mu.Lock()
	if m.ctx.Err() != nil {
		m.mu.Unlock()
		cancel()
		client.Close()
		return m.ctx.Err()
	}
	old := m.accounts[name]
	m.accounts[name] = a
	for key, p := range m.presence {
		if p.Account == name {
			delete(m.presence, key)
		}
	}
	replaced := []*call{}
	if old != nil {
		for id, c := range m.calls {
			if c.owner == old {
				delete(m.calls, id)
				replaced = append(replaced, c)
			}
		}
	}
	m.mu.Unlock()
	for _, c := range replaced {
		m.finish(c, "account replaced")
	}
	if old != nil {
		if old.publicationCancel != nil {
			old.publicationCancel()
			<-old.publicationDone
		}
		old.cancel()
		old.client.Close()
	}
	go m.maintainPublication(a, publicationContext)
	m.notify()
	go func() {
		defer close(a.registrationDone)
		m.registerAccount(a)
	}()
	go m.superviseRegistration(a)
	return nil
}
func (m *Manager) Disable(name string) {
	m.mu.Lock()
	a := m.accounts[name]
	delete(m.accounts, name)
	for key, p := range m.presence {
		if p.Account == name {
			delete(m.presence, key)
		}
	}
	closed := []*call{}
	for id, c := range m.calls {
		if c.state.Account == name {
			delete(m.calls, id)
			closed = append(closed, c)
		}
	}
	m.mu.Unlock()
	for _, c := range closed {
		m.finish(c, "account disabled")
	}
	if a != nil {
		if a.publicationCancel != nil {
			a.publicationCancel()
			<-a.publicationDone
		}
		a.cancel()
		a.client.Close()
	}
	m.notify()
}
func (m *Manager) Close() {
	m.cancel()
	m.mu.Lock()
	names := []string{}
	for name := range m.accounts {
		names = append(names, name)
	}
	m.mu.Unlock()
	for _, name := range names {
		m.Disable(name)
	}
	<-m.done
	m.transferWorkers.Wait()
}
func (m *Manager) SetAudio(s media.Settings) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.calls) > 0 {
		return errors.New("change audio devices when all calls have ended")
	}
	m.settings = s
	return nil
}

// SetDND rejects incoming calls locally and sets do not disturb on Swyx.
func (m *Manager) SetDND(v bool) {
	m.mu.Lock()
	m.dnd = v
	m.mu.Unlock()
	m.wakeStatus()
	m.notify()
}

// SetScreenLocked marks the user away while the desktop is locked.
func (m *Manager) SetScreenLocked(v bool) {
	m.mu.Lock()
	changed := m.screenLocked != v
	m.screenLocked = v
	m.mu.Unlock()
	if changed {
		m.wakeStatus()
		m.notify()
	}
}
func (m *Manager) getAccount(name string) (*account, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a := m.accounts[name]
	if a == nil || a.state.State != "registered" {
		return nil, errors.New("enable and register this account first")
	}
	return a, nil
}
func (m *Manager) getCall(id string) (*call, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := m.calls[id]
	if c == nil {
		return nil, errors.New("call no longer exists")
	}
	return c, nil
}

func (m *Manager) Dial(accountName, target string) error {
	_, err := m.dial(accountName, target, "")
	return err
}

// Consult calls a transfer target while the call id waits on hold, so the user
// can talk to the target before completing an attended transfer.
func (m *Manager) Consult(id, target string) error {
	original, err := m.getCall(id)
	if err != nil {
		return err
	}
	m.mu.Lock()
	state := original.state
	m.mu.Unlock()
	if state.State != "connected" {
		return errors.New("only a connected call can be transferred")
	}
	_, err = m.dial(state.Account, target, id)
	return err
}

func (m *Manager) dial(accountName, target, consultationFor string) (string, error) {
	target = normalizeDialTarget(target)
	if !m.activation.TryLock() {
		return "", errors.New("finish or cancel the pending call first")
	}
	defer m.activation.Unlock()
	if strings.TrimSpace(target) == "" {
		return "", errors.New("enter a number or SIP address")
	}
	a, err := m.getAccount(accountName)
	if err != nil {
		return "", err
	}
	if err := a.client.ValidateTarget(target); err != nil {
		return "", err
	}
	if err := m.holdOtherCalls(""); err != nil {
		return "", err
	}
	ctx, cancel := context.WithCancel(m.ctx)
	id := uuid.NewString()
	m.mu.Lock()
	settings := m.settings
	settings.MediaSecurity = a.config.MediaSecurity
	settings.SymmetricRTP = a.config.SymmetricRTP
	settings.ICEPolicy = a.config.ICEPolicy
	settings.ICEServers = nil
	for _, server := range a.config.ICEServers {
		settings.ICEServers = append(settings.ICEServers, media.ICEServer{URLs: server.URLs, Username: server.Username, Credential: server.Credential})
	}
	if len(a.config.Codecs) > 0 {
		settings.Codecs = append([]string(nil), a.config.Codecs...)
	}
	settings.SecureSignaling = strings.EqualFold(a.config.Transport, "tls")
	m.mu.Unlock()
	c := &call{connected: make(chan struct{}), ended: make(chan struct{}), state: CallState{ID: id, Account: accountName, Remote: target, Direction: "outgoing", State: "preparing", Started: time.Now(), ConsultationFor: consultationFor}, cancel: cancel, owner: a}
	m.mu.Lock()
	if m.accounts[accountName] != a {
		m.mu.Unlock()
		cancel()
		return "", errors.New("account changed")
	}
	m.calls[id] = c
	m.mu.Unlock()
	m.notify()
	if a.config.DelayedOffer {
		m.mu.Lock()
		c.state.State = "calling"
		m.mu.Unlock()
		m.notify()
		err = a.client.DialDelayedOptionsID(ctx, id, target, sip.DelayedOfferOptions{Prepare: func(answerCtx context.Context, offer []byte) (sip.DelayedAnswer, error) {
			return m.prepareDelayedAnswer(answerCtx, c, settings, offer)
		}})
		if err != nil {
			m.end(id, err.Error())
			return "", err
		}
		return id, nil
	}
	stream, err := media.NewCall(ctx, id, settings, nil)
	if err != nil {
		m.end(id, err.Error())
		return "", err
	}
	offer := stream.LocalSDP(advertisedIP(a.config))
	m.mu.Lock()
	if m.calls[id] != c {
		m.mu.Unlock()
		stream.Close()
		return "", errors.New("call canceled during media setup")
	}
	c.media = stream
	stream.SetMuted(c.state.Muted)
	c.offer = offer
	c.state.State = "calling"
	m.mu.Unlock()
	m.notify()
	if err = a.client.DialID(ctx, id, target, offer); err != nil {
		m.end(id, err.Error())
		return "", err
	}
	return id, nil
}
func (m *Manager) Answer(id string) (result error) {
	if !m.activation.TryLock() {
		return errors.New("finish or cancel the pending call first")
	}
	defer m.activation.Unlock()
	c, err := m.getCall(id)
	if err != nil {
		return err
	}
	m.mu.Lock()
	if c.state.State != "ringing" {
		m.mu.Unlock()
		return errors.New("call is not ringing")
	}
	replaced := c.replaces
	if original := m.calls[replaced]; original != nil && !original.state.Muted {
		c.restoreMute = original
	}
	m.mu.Unlock()
	if replaced != "" {
		m.Mute(replaced, true)
		defer func() {
			if result != nil {
				m.restoreReplacedMute(c)
			}
		}()
	}
	if err := m.holdOtherCalls(id); err != nil {
		return err
	}
	m.mu.Lock()
	if m.calls[id] != c || c.state.State != "ringing" {
		m.mu.Unlock()
		return errors.New("call is no longer ringing")
	}
	c.state.State = "answering"
	ctx, cancel := context.WithCancel(m.ctx)
	oldCancel := c.cancel
	c.cancel = cancel
	settings := m.settings
	settings.MediaSecurity = c.owner.config.MediaSecurity
	settings.SymmetricRTP = c.owner.config.SymmetricRTP
	settings.ICEPolicy = c.owner.config.ICEPolicy
	settings.ICEServers = nil
	for _, server := range c.owner.config.ICEServers {
		settings.ICEServers = append(settings.ICEServers, media.ICEServer{URLs: server.URLs, Username: server.Username, Credential: server.Credential})
	}
	if len(c.owner.config.Codecs) > 0 {
		settings.Codecs = append([]string(nil), c.owner.config.Codecs...)
	}
	settings.SecureSignaling = strings.EqualFold(c.owner.config.Transport, "tls")
	offer := append([]byte(nil), c.offer...)
	m.mu.Unlock()
	m.notify()
	m.stopRinging(c)
	if oldCancel != nil {
		oldCancel()
	}
	stream, err := media.NewCall(ctx, id, settings, offer)
	if err != nil {
		cancel()
		m.restoreRinging(c, err)
		return err
	}
	m.mu.Lock()
	if m.calls[id] != c {
		m.mu.Unlock()
		cancel()
		stream.Close()
		return errors.New("call ended while answering")
	}
	c.media = stream
	stream.SetMuted(c.state.Muted)
	c.mediaConnected = false
	m.mu.Unlock()
	answer := stream.LocalSDP(advertisedIP(c.owner.config))
	answerCtx, done := context.WithTimeout(ctx, 15*time.Second)
	defer done()
	if err = c.owner.client.Answer(answerCtx, id, answer); err != nil {
		m.end(id, err.Error())
		return err
	}
	m.mu.Lock()
	if m.calls[id] == c {
		c.offer = answer
		if c.mediaConnected {
			c.state.State = "connected"
			m.signalConnectedLocked(c)
		}
	}
	m.mu.Unlock()
	m.notify()
	return nil
}
func (m *Manager) Hangup(id string) error {
	c, err := m.getCall(id)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(m.ctx, 5*time.Second)
	defer cancel()
	m.mu.Lock()
	preparing := c.state.State == "preparing"
	ringing := c.state.State == "ringing"
	if ringing {
		c.state.State = "declined"
	}
	m.mu.Unlock()
	if preparing {
		m.end(id, "canceled")
		return nil
	}
	if ringing {
		err = c.owner.client.Reject(ctx, id)
	} else {
		err = c.owner.client.Hangup(ctx, id)
	}
	m.end(id, "ended")
	return err
}
func (m *Manager) Mute(id string, v bool) error {
	m.mu.Lock()
	c := m.calls[id]
	if c == nil {
		m.mu.Unlock()
		return errors.New("call no longer exists")
	}
	c.state.Muted = v
	if c.media != nil {
		c.media.SetMuted(v)
	}
	m.mu.Unlock()
	m.notify()
	return nil
}

func (m *Manager) ToggleActiveMute() error {
	m.mu.Lock()
	var active *call
	for _, c := range m.calls {
		if c.state.State == "connected" && !c.state.Held {
			if active != nil {
				m.mu.Unlock()
				return errors.New("multiple calls are active; choose a call to mute")
			}
			active = c
		}
	}
	if active == nil {
		m.mu.Unlock()
		return errors.New("no active call to mute")
	}
	active.state.Muted = !active.state.Muted
	if active.media != nil {
		active.media.SetMuted(active.state.Muted)
	}
	m.mu.Unlock()
	m.notify()
	return nil
}

func (m *Manager) Hold(id string, v bool) error {
	c, err := m.getCall(id)
	if err != nil {
		return err
	}
	m.mu.Lock()
	if c.media == nil || c.state.State != "connected" {
		m.mu.Unlock()
		return errors.New("call is not connected")
	}
	stream := c.media
	m.mu.Unlock()
	ctx, cancel := context.WithTimeout(m.ctx, 15*time.Second)
	defer cancel()
	return c.owner.client.ReinviteWithOffer(ctx, id, func() ([]byte, error) {
		return stream.LocalOffer(advertisedIP(c.owner.config), v), nil
	})
}
func (m *Manager) DTMF(id, digit string) error {
	if len(digit) != 1 || !strings.Contains("0123456789*#ABCD", digit) {
		return errors.New("invalid DTMF digit")
	}
	c, err := m.getCall(id)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(m.ctx, 5*time.Second)
	defer cancel()
	m.mu.Lock()
	caps := c.owner.state.Capabilities
	stream := c.media
	m.mu.Unlock()
	if c.owner.config.DTMFMode == "info" || (c.owner.config.DTMFMode != "rtp" && caps.SwyxDetected) {
		return c.owner.client.SendDTMF(ctx, id, digit)
	}
	if stream == nil {
		return errors.New("media not connected")
	}
	err = stream.SendDTMF(digit)
	if (c.owner.config.DTMFMode == "" || c.owner.config.DTMFMode == "auto") && errors.Is(err, media.ErrTelephoneEventsNotNegotiated) {
		return c.owner.client.SendDTMF(ctx, id, digit)
	}
	return err
}
func (m *Manager) Transfer(id, target string) error {
	target = normalizeDialTarget(target)
	c, err := m.getCall(id)
	if err != nil {
		return err
	}
	return m.sendTransfer(c, nil, func(ctx context.Context) error {
		return c.owner.client.Transfer(ctx, id, target)
	})
}
func (m *Manager) Message(accountName, target, body string) error {
	target = normalizeDialTarget(target)
	a, err := m.getAccount(accountName)
	if err != nil {
		return err
	}
	m.mu.Lock()
	mode := a.state.Capabilities.Messaging
	m.mu.Unlock()
	switch mode {
	case "disabled":
		return errors.New("messaging is disabled for this account")
	case "unavailable":
		return errors.New("this server does not accept chat messages over SIP")
	}
	if err := a.client.ValidateTarget(target); err != nil {
		return err
	}
	remote := normalizeWatchTarget(target, a.config)
	id, err := m.store.AddMessage(store.Message{Account: accountName, Remote: remote, Body: body, Direction: "outgoing", Status: "pending"})
	if err != nil {
		return err
	}
	m.notify()
	ctx, cancel := context.WithTimeout(m.ctx, 15*time.Second)
	defer cancel()
	err = a.client.SendMessage(ctx, target, body)
	status := "accepted by server"
	if err != nil {
		status = "failed"
	}
	m.report(m.store.MessageStatus(id, status))
	m.notify()
	return err
}
func (m *Manager) handle(ev event) {
	m.mu.Lock()
	if m.accounts[ev.account] != ev.owner {
		m.mu.Unlock()
		return
	}
	e := ev.sip
	if c := m.calls[e.CallID]; c != nil && c.owner != ev.owner {
		m.mu.Unlock()
		if e.Type == "incoming" {
			go func() {
				ctx, cancel := context.WithTimeout(m.ctx, 5*time.Second)
				defer cancel()
				m.report(ev.owner.client.Reject(ctx, e.CallID))
			}()
		}
		return
	}
	switch e.Type {
	case "registration":
		ev.owner.state.State = e.State
		ev.owner.state.Error = e.Message
		if e.State == "failed" {
			ev.owner.registrationRetryable = e.StatusCode == 0 || e.StatusCode == 408 || e.StatusCode >= 500
		}
		if e.Server != "" {
			ev.owner.state.Capabilities = swyx.Detect(e.Server, ev.owner.allow, ev.owner.config.PresenceMode, ev.owner.config.MessagingMode)
		}
		if e.State == "registered" && !ev.owner.probed {
			ev.owner.probed = true
			go m.probeServer(ev.owner)
		}
	case "incoming":
		forward := ev.owner.config.ForwardAlways
		busy := m.dnd
		for _, active := range m.calls {
			if active.state.State == "connected" || active.state.State == "answering" {
				busy = true
			}
		}
		if forward == "" && busy {
			forward = ev.owner.config.ForwardBusy
		}
		original := m.calls[e.ReplacesCallID]
		replacing := original != nil && original.owner == ev.owner && original.state.State == "connected"
		if m.dnd && forward == "" && !replacing {
			m.mu.Unlock()
			go func() {
				ctx, cancel := context.WithTimeout(m.ctx, 5*time.Second)
				defer cancel()
				m.report(ev.owner.client.Reject(ctx, e.CallID))
			}()
			return
		}
		if _, ok := m.calls[e.CallID]; !ok {
			m.calls[e.CallID] = &call{connected: make(chan struct{}), ended: make(chan struct{}), replaces: e.ReplacesCallID, state: CallState{ID: e.CallID, Account: ev.account, Remote: e.RemoteURI, Direction: "incoming", State: "ringing", Started: time.Now()}, offer: append([]byte(nil), e.SDP...), owner: ev.owner}
			c := m.calls[e.CallID]
			if c.replaces != "" {
				original := m.calls[c.replaces]
				if original != nil && original.owner == ev.owner && original.state.State == "connected" {
					m.mu.Unlock()
					go func() { m.report(m.Answer(c.state.ID)) }()
					m.notify()
					return
				}
			}
			if forward != "" {
				m.mu.Unlock()
				go m.forwardCall(c, forward)
				m.notify()
				return
			}
			if target := ev.owner.config.ForwardNoAnswer; target != "" {
				delay := ev.owner.config.NoAnswerSeconds
				if delay < 5 {
					delay = 20
				}
				c.forwardTimer = time.AfterFunc(time.Duration(delay)*time.Second, func() { m.forwardCall(c, target) })
			}
			m.startRingingLocked(c, false)

		}
	case "redirect":
		if c := m.calls[e.CallID]; c != nil {
			c.state.Remote = e.RemoteURI
		}
	case "ringing":
		if c := m.calls[e.CallID]; c != nil && (c.state.State == "calling" || c.state.State == "ringback") {
			c.state.State = "ringback"
			if len(e.SDP) > 0 && c.earlyDone == nil && c.media != nil {
				c.earlyDone = make(chan struct{})
				stream := c.media
				go func() {
					defer close(c.earlyDone)
					m.stopRinging(c)
					if err := stream.ConnectEarly(e.SDP); err != nil {
						m.report(err)
					}
				}()
			} else if len(e.SDP) == 0 && c.earlyDone == nil && c.ringDone == nil {
				m.startRingingLocked(c, true)
			}
		}
	case "updated":
		if c := m.calls[e.CallID]; c != nil {
			c.state.Error = ""
		}
	case "renegotiation-failed":
		if c := m.calls[e.CallID]; c != nil {
			c.state.Error = e.Message
		}
	case "connected":
		c := m.calls[e.CallID]
		if c != nil {
			if c.state.Connected.IsZero() {
				c.state.Connected = time.Now()
			}
			if c.mediaConnected {
				c.state.State = "connected"
				m.signalConnectedLocked(c)
			} else if c.state.State != "connecting" && c.media != nil {
				c.state.State = "connecting"
				stream, earlyDone := c.media, c.earlyDone
				go func() {
					if earlyDone != nil {
						<-earlyDone
					}
					m.stopRinging(c)
					var err error
					if len(e.AnswerTo) > 0 {
						err = stream.AcceptAnswer(e.AnswerTo, e.SDP)
					} else {
						err = stream.Connect(e.SDP)
					}
					if err != nil {
						m.report(err)
						m.mu.Lock()
						current := m.calls[e.CallID] == c
						m.mu.Unlock()
						if current {
							m.Hangup(e.CallID)
						}
						return
					}
					m.mu.Lock()
					if m.calls[e.CallID] == c {
						c.mediaConnected = true
						c.state.State = "connected"
						m.signalConnectedLocked(c)
					}
					m.mu.Unlock()
					m.notify()
				}()
			}
		}
	case "ended":
		c := m.calls[e.CallID]
		if c != nil && e.TerminationReason == "answered-elsewhere" && c.state.Direction == "incoming" && c.state.Connected.IsZero() {
			c.state.State = "answered elsewhere"
		}
		delete(m.calls, e.CallID)
		m.mu.Unlock()
		if c != nil {
			m.finish(c, e.Message)
		}
		m.notify()
		return
	case "transfer-complete":
		if c := m.calls[e.CallID]; c != nil {
			c.state.TransferStatus = e.State
			if e.State == "success" {
				m.transferWorkers.Add(1)
				go func() {
					defer m.transferWorkers.Done()
					m.report(m.Hangup(e.CallID))
				}()
			}
		}
	case "transfer":
		if c := m.calls[e.CallID]; c != nil && c.outgoingTransfer != nil {
			transfer := c.outgoingTransfer
			c.state.TransferStatus = e.State
			if e.StatusCode != 0 {
				c.state.TransferStatus = fmt.Sprintf("%s (%d)", e.State, e.StatusCode)
			}
			if e.State == "success" || e.State == "failed" {
				c.outgoingTransfer = nil
			}
			if e.State == "success" {
				m.transferWorkers.Add(1)
				go func() {
					defer m.transferWorkers.Done()
					m.finishTransferred(c)
					if transfer.consultation != nil {
						m.finishTransferred(transfer.consultation)
					}
				}()
			}
		}
	case "presence":
		m.handlePresenceLocked(ev)
	case "message":
		m.mu.Unlock()
		_, err := m.store.AddMessage(store.Message{Account: ev.account, Remote: normalizeWatchTarget(e.RemoteURI, ev.owner.config), Body: string(e.Body), Direction: "incoming", Status: "received"})
		m.report(err)
		m.notify()
		if m.emit != nil {
			m.emit("incoming-message", Notice{Account: ev.account, Remote: normalizeWatchTarget(e.RemoteURI, ev.owner.config)})
		}
		return
	}
	m.mu.Unlock()
	m.notify()
	if e.Type == "incoming" && m.emit != nil {
		m.emit("incoming-call", Notice{ID: e.CallID, Account: ev.account, Remote: e.RemoteURI})
	}
}
func (m *Manager) end(id, reason string) {
	m.mu.Lock()
	c := m.calls[id]
	delete(m.calls, id)
	m.mu.Unlock()
	if c != nil {
		m.finish(c, reason)
	}
	m.notify()
}
func (m *Manager) finish(c *call, reason string) {
	m.restoreReplacedMute(c)
	m.stopRinging(c)
	m.mu.Lock()
	if c.finished {
		m.mu.Unlock()
		return
	}
	c.finished = true
	if c.ended != nil {
		close(c.ended)
	}
	state := c.state
	cancel := c.cancel
	stream := c.media
	forwardTimer := c.forwardTimer
	m.mu.Unlock()
	if forwardTimer != nil {
		forwardTimer.Stop()
	}
	if cancel != nil {
		cancel()
	}
	if stream != nil {
		m.report(stream.Close())
	}
	status := reason
	if status == "" {
		status = "ended"
	}
	if state.Direction == "incoming" && state.Connected.IsZero() {
		status = "missed"
		if state.State == "declined" {
			status = "declined"
		}
		if state.State == "answered elsewhere" {
			status = "answered elsewhere"
		}
		if reason == "redirected" {
			status = "redirected"
		}
	}
	m.report(m.store.AddHistory(store.History{Account: state.Account, Remote: state.Remote, Direction: state.Direction, Status: status, Started: state.Started, Ended: time.Now()}))
}
func advertisedIP(cfg config.Config) string {
	if cfg.MediaAddress != "" {
		return cfg.MediaAddress
	}
	host := cfg.Server
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	port := cfg.Port
	if port == 0 {
		port = 5060
	}
	conn, err := net.DialTimeout("udp", net.JoinHostPort(host, fmt.Sprint(port)), 2*time.Second)
	if err == nil {
		defer conn.Close()
		if a, ok := conn.LocalAddr().(*net.UDPAddr); ok {
			return a.IP.String()
		}
	}
	return "127.0.0.1"
}

func (m *Manager) AttendedTransfer(id, consultationID string) error {
	first, err := m.getCall(id)
	if err != nil {
		return err
	}
	second, err := m.getCall(consultationID)
	if err != nil {
		return err
	}
	if first.owner != second.owner {
		return errors.New("attended transfer requires calls on the same account")
	}
	return m.sendTransfer(first, second, func(ctx context.Context) error {
		return first.owner.client.TransferToCall(ctx, id, consultationID)
	})
}

// Conference mixes two or three connected calls locally. Held calls are
// resumed first, since dialing the next participant put them on hold.
func (m *Manager) Conference(ids []string) error {
	var held []string
	m.mu.Lock()
	for _, id := range ids {
		if c := m.calls[id]; c != nil && c.state.State == "connected" && c.state.Held {
			held = append(held, id)
		}
	}
	m.mu.Unlock()
	for _, id := range held {
		if err := m.Hold(id, false); err != nil {
			return fmt.Errorf("resume call before merging: %w", err)
		}
	}
	streams := []*media.Call{}
	seen := map[string]bool{}
	m.mu.Lock()
	for _, id := range ids {
		c := m.calls[id]
		if c == nil || c.media == nil || c.state.State != "connected" || c.state.Held || seen[id] {
			m.mu.Unlock()
			return errors.New("choose distinct connected calls to merge")
		}
		seen[id] = true
		streams = append(streams, c.media)
	}
	m.mu.Unlock()
	err := media.JoinConference(streams...)
	m.notify()
	return err
}
func (m *Manager) LeaveConference() error {
	m.mu.Lock()
	streams := []*media.Call{}
	for _, c := range m.calls {
		if c.media != nil {
			streams = append(streams, c.media)
		}
	}
	m.mu.Unlock()
	for _, stream := range streams {
		stream.LeaveConference()
	}
	m.notify()
	return nil
}
func (m *Manager) SetGain(id string, input, output float64) error {
	m.mu.Lock()
	c := m.calls[id]
	var stream *media.Call
	if c != nil {
		stream = c.media
	}
	m.mu.Unlock()
	if stream == nil {
		return errors.New("call media not connected")
	}
	return stream.SetGain(input, output)
}

func (m *Manager) holdOtherCalls(except string) error {
	m.mu.Lock()
	ids := []string{}
	replaced := ""
	if c := m.calls[except]; c != nil {
		replaced = c.replaces
	}
	for id, c := range m.calls {
		if id == replaced {
			continue
		}
		if id != except && (c.state.State == "preparing" || c.state.State == "calling" || c.state.State == "ringback" || c.state.State == "answering" || c.state.State == "connecting") {
			m.mu.Unlock()
			return errors.New("finish or cancel the pending call first")
		}
		if id != except && c.state.State == "connected" && !c.state.Held {
			ids = append(ids, id)
		}
	}
	m.mu.Unlock()
	for _, id := range ids {
		if err := m.Hold(id, true); err != nil {
			return fmt.Errorf("hold existing call: %w", err)
		}
	}
	return nil
}

func (m *Manager) StartRecording(id, path string) error {
	m.mu.Lock()
	c := m.calls[id]
	var stream *media.Call
	if c != nil {
		stream = c.media
	}
	m.mu.Unlock()
	if stream == nil {
		return errors.New("call media not connected")
	}
	return stream.StartRecording(path)
}
func (m *Manager) StopRecording(id string) error {
	m.mu.Lock()
	c := m.calls[id]
	var stream *media.Call
	if c != nil {
		stream = c.media
	}
	m.mu.Unlock()
	if stream == nil {
		return errors.New("call media not connected")
	}
	return stream.StopRecording()
}

func (m *Manager) DialVoicemail(accountName string) error {
	a, err := m.getAccount(accountName)
	if err != nil {
		return err
	}
	if a.config.Voicemail == "" {
		return errors.New("configure the voicemail number in account settings")
	}
	return m.Dial(accountName, a.config.Voicemail)
}

func (m *Manager) Redirect(id, target string) error {
	target = normalizeDialTarget(target)
	c, err := m.getCall(id)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(m.ctx, 5*time.Second)
	defer cancel()
	return c.owner.client.Redirect(ctx, id, target)
}

func (m *Manager) signalConnectedLocked(c *call) {
	if c.connected != nil {
		select {
		case <-c.connected:
		default:
			close(c.connected)
		}
	}
	if c.replaces != "" {
		id := c.replaces
		c.replaces = ""
		go func() { m.report(m.Hangup(id)) }()
	}
}
