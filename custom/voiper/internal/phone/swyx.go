package phone

import (
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/megakuul/voiper/internal/compat/swyx"
	"github.com/megakuul/voiper/internal/compat/swyx/cds"
)

// ownStatus is what the user chose in Voiper. DND and the screen lock are
// global and are combined with it when the status is published.
type ownStatus struct {
	set  bool
	away bool
	note string
}

// swyxStatus is the state written to the Swyx presence configuration.
type swyxStatus struct {
	away, dnd bool
	note      string
	noteSet   bool
}

// swyxSession is the account's connection to the Swyx client API. One
// goroutine owns the API client; the manager signals status changes via wake.
type swyxSession struct {
	wake chan struct{}
}

const (
	swyxPollInterval      = 5 * time.Second
	swyxDirectoryInterval = 10 * time.Minute
	swyxStatusInterval    = time.Minute
)

// probeServer asks the registrar which methods it allows. SwyxWare answers
// MESSAGE and PUBLISH with 405, so Allow decides between the standard SIP
// features and the Swyx client API.
func (m *Manager) probeServer(a *account) {
	target := "sip:" + a.config.Server
	if a.config.Port != 0 && a.config.Port != 5060 {
		target = "sip:" + net.JoinHostPort(a.config.Server, strconv.Itoa(a.config.Port))
	}
	ctx, cancel := context.WithTimeout(a.ctx, 10*time.Second)
	response, err := a.client.SendRequest(ctx, "OPTIONS", target, "", nil, nil)
	cancel()
	var allow []string
	identification := ""
	if err == nil {
		allow = []string{}
		for name, value := range response.Headers {
			switch strings.ToLower(name) {
			case "allow":
				for _, method := range strings.Split(value, ",") {
					if method = strings.TrimSpace(method); method != "" {
						allow = append(allow, strings.ToUpper(method))
					}
				}
			case "server", "user-agent":
				identification = value
			}
		}
	}
	m.mu.Lock()
	if m.accounts[a.state.Name] != a {
		m.mu.Unlock()
		return
	}
	a.allow = allow
	server := a.state.Capabilities.Server
	if server == "" {
		server = identification
	}
	a.state.Capabilities = swyx.Detect(server, allow, a.config.PresenceMode, a.config.MessagingMode)
	var session *swyxSession
	if a.state.Capabilities.Status == "swyx" && a.swyx == nil {
		session = &swyxSession{wake: make(chan struct{}, 1)}
		a.swyx = session
		a.state.SwyxAPI = "connecting"
	}
	m.mu.Unlock()
	if session != nil {
		go m.runSwyx(a, session)
	}
	m.notify()
}

func (m *Manager) newSwyxClient(a *account) (*cds.Client, error) {
	// The Swyx client API uses its own login (often the Windows/AD account),
	// which is not the SIP user name. Prefer it, and only fall back to the SIP
	// credentials for installations where they happen to match.
	cfg := cds.Config{Server: a.config.Server, Username: a.config.SwyxUsername, Password: a.config.SwyxPassword}
	if cfg.Username == "" {
		cfg.Username = a.config.AuthUsername
		if cfg.Username == "" {
			cfg.Username = a.config.Username
		}
		cfg.Password = a.config.Password
	}
	tlsConfig, err := accountTLS(a.config.TLSCAFile)
	if err != nil {
		return nil, err
	}
	if tlsConfig != nil {
		cfg.RootCAs = tlsConfig.RootCAs
	} else {
		// SwyxWare ships a self-signed certificate; trust the first one seen.
		cfg.Trust = m.store.TrustCertificate
	}
	return cds.New(cfg)
}

// wakeStatus republishes the own status of every account after a change.
func (m *Manager) wakeStatus() {
	m.mu.Lock()
	var publish []string
	for name, a := range m.accounts {
		if a.swyx != nil {
			select {
			case a.swyx.wake <- struct{}{}:
			default:
			}
		} else if a.status.set && a.state.Capabilities.Status == "sip" {
			publish = append(publish, name)
		}
	}
	m.mu.Unlock()
	// Failures are shown on the account by the publication worker.
	for _, name := range publish {
		go func() { _ = m.publishOwnStatus(name) }()
	}
}

// TrustNewSwyxCertificate forgets the pinned API certificate of the account's
// server, after the user confirmed that the server was replaced.
func (m *Manager) TrustNewSwyxCertificate(accountName string) error {
	a, err := m.getAccount(accountName)
	if err != nil {
		return err
	}
	if err := m.store.ForgetCertificate(cds.Address(a.config.Server)); err != nil {
		return err
	}
	m.mu.Lock()
	if a.swyx != nil {
		select {
		case a.swyx.wake <- struct{}{}:
		default:
		}
	}
	m.mu.Unlock()
	return nil
}

func (m *Manager) desiredSwyxStatus(a *account) swyxStatus {
	return swyxStatus{away: a.status.away || m.screenLocked, dnd: m.dnd, note: a.status.note, noteSet: a.status.set}
}

// runSwyx keeps the own status in sync and polls the colleagues' status until
// the account is disabled. Failures are shown on the account and retried.
func (m *Manager) runSwyx(a *account, session *swyxSession) {
	client, err := m.newSwyxClient(a)
	if err != nil {
		m.setSwyxState(a, "failed", err)
		return
	}
	defer client.Close()
	// Voiper writes nothing until the user, DND or the screen lock asks for
	// something, so a status set in SwyxIt! survives starting Voiper.
	m.mu.Lock()
	applied := a.swyxApplied
	m.mu.Unlock()
	var since int64
	var directoryAt, statusAt time.Time
	numbers := map[[2]int][]string{}
	failures := 0
	connected := false
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-session.wake:
			timer.Stop()
		case <-timer.C:
		}
		ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
		err := func() error {
			m.mu.Lock()
			desired := m.desiredSwyxStatus(a)
			m.mu.Unlock()
			if desired != applied || time.Since(statusAt) > swyxStatusInterval {
				if err := m.syncSwyxStatus(ctx, a, client, desired, applied); err != nil {
					return err
				}
				applied, statusAt = desired, time.Now()
				m.mu.Lock()
				a.swyxApplied = applied
				m.mu.Unlock()
			}
			if time.Since(directoryAt) > swyxDirectoryInterval {
				employees, err := swyxEmployees(ctx, client)
				if err != nil {
					return err
				}
				numbers = map[[2]int][]string{}
				presence := map[string]cds.Presence{}
				for _, e := range employees {
					key := [2]int{e.SiteId, e.EmployeeId}
					for _, number := range employeeNumbers(e) {
						numbers[key] = append(numbers[key], number)
						presence[number] = e.Presence
					}
				}
				m.mu.Lock()
				a.swyxPresence = presence
				m.mu.Unlock()
				directoryAt, since = time.Now(), 0
			}
			changes, next, err := client.PresenceChanges(ctx, since)
			if err != nil {
				return err
			}
			since = next
			if len(changes) > 0 {
				m.mu.Lock()
				for _, change := range changes {
					for _, number := range numbers[[2]int{change.SiteId, change.EmployeeId}] {
						a.swyxPresence[number] = change.Presence
					}
				}
				m.mu.Unlock()
				m.notify()
			}
			return nil
		}()
		cancel()
		if a.ctx.Err() != nil {
			return
		}
		if err != nil {
			failures++
			connected = false
			m.setSwyxState(a, "failed", err)
			timer.Reset(min(5*time.Minute, swyxPollInterval*time.Duration(1<<min(failures, 6))))
			continue
		}
		if !connected {
			connected = true
			m.setSwyxState(a, "connected", nil)
		}
		failures = 0
		timer.Reset(swyxPollInterval)
	}
}

// syncSwyxStatus writes the fields Voiper changed since the last sync and
// shows the server's current status on the account.
func (m *Manager) syncSwyxStatus(ctx context.Context, a *account, client *cds.Client, desired, applied swyxStatus) error {
	cfg, err := client.PresenceConfiguration(ctx)
	if err != nil {
		return err
	}
	changed := false
	if desired.away != applied.away {
		cfg.IsAway, cfg.Expiration, changed = desired.away, nil, true
	}
	if desired.dnd != applied.dnd {
		cfg.DoNotDisturb, cfg.Expiration, changed = desired.dnd, nil, true
	}
	if desired.noteSet && (desired.note != applied.note || !applied.noteSet) {
		cfg.AwayText, changed = desired.note, true
	}
	if changed {
		if err := client.SetPresenceConfiguration(ctx, cfg); err != nil {
			return err
		}
	}
	state := "available"
	if cfg.DoNotDisturb {
		state = "dnd"
	} else if cfg.IsAway {
		state = "away"
	}
	m.mu.Lock()
	if m.accounts[a.state.Name] == a {
		a.state.PresenceState, a.state.PresenceNote, a.state.PresenceError = state, cfg.AwayText, ""
	}
	m.mu.Unlock()
	m.notify()
	return nil
}

func swyxEmployees(ctx context.Context, client *cds.Client) ([]cds.Employee, error) {
	const pageSize = 500
	var employees []cds.Employee
	for page := 0; page < 40; page++ {
		batch, err := client.Employees(ctx, page, pageSize)
		if err != nil {
			return nil, err
		}
		employees = append(employees, batch...)
		if len(batch) < pageSize {
			break
		}
	}
	return employees, nil
}

func employeeNumbers(e cds.Employee) []string {
	numbers := []string{}
	if e.Number != "" {
		numbers = append(numbers, e.Number)
	}
	for _, n := range e.AllEmployeeNumbers {
		if n.NumberType == cds.InternalNumber && n.Number != "" && n.Number != e.Number {
			numbers = append(numbers, n.Number)
		}
	}
	return numbers
}

func (m *Manager) setSwyxState(a *account, state string, err error) {
	detail := ""
	if err != nil {
		detail = err.Error()
		var apiError *cds.Error
		if errors.As(err, &apiError) && apiError.Status == 401 {
			detail = "Swyx client API rejected the SIP credentials: " + apiError.Details
		}
	}
	m.mu.Lock()
	if m.accounts[a.state.Name] == a {
		a.state.SwyxAPI, a.state.SwyxAPIError = state, detail
	}
	m.mu.Unlock()
	m.notify()
}

// withSwyxPresence completes a SIP presence entry with the status text, and
// with the status when SIP has none, from the Swyx client API.
func withSwyxPresence(p Presence, statuses map[string]cds.Presence) Presence {
	user := strings.TrimPrefix(strings.TrimPrefix(p.Target, "sips:"), "sip:")
	user, _, _ = strings.Cut(user, "@")
	status, ok := statuses[user]
	if !ok {
		return p
	}
	if status.StatusText != "" {
		p.Note = status.StatusText
	}
	switch p.State {
	case "available", "offline", "in-call", "away", "dnd":
		return p
	}
	switch status.Status {
	case cds.StatusLoggedOff:
		p.State = "offline"
	case cds.StatusLoggedOn:
		p.State = "available"
	case cds.StatusActive:
		p.State = "in-call"
	case cds.StatusAway:
		p.State = "away"
	case cds.StatusDoNotDisturb:
		p.State = "dnd"
	default:
		return p
	}
	p.Source = "swyx-api"
	return p
}
