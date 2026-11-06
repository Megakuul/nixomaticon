// Package sip provides account registration and audio-call signaling. Audio and
// vendor-specific protocols belong to callers of this package.
package sip

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/emiago/sipgo"
	wire "github.com/emiago/sipgo/sip"
	"github.com/google/uuid"
)

type Config struct {
	Resolver                                                                                      *net.Resolver
	KeepAliveInterval                                                                             time.Duration
	MaxRedirects                                                                                  int
	SessionExpires, MinSessionExpires                                                             time.Duration
	Server                                                                                        string
	Port                                                                                          int
	Domain, Username, AuthUsername, Password, DisplayName, Transport, LocalAddress, OutboundProxy string
	Expires                                                                                       time.Duration
	TLSConfig                                                                                     *tls.Config
}

type Event struct {
	Type, CallID, RemoteURI, DisplayName, State, Message, Server string
	StatusCode                                                   int
	TerminationReason                                            string
	AnswerTo                                                     []byte
	SDP, Body                                                    []byte
	ContentType                                                  string
	DelayedOffer                                                 bool
	ReplacesCallID                                               string
}

type Client struct {
	registrarTargets    []string
	selectedTarget      string
	resolvedTargetsAt   time.Time
	inviteMu            sync.Mutex
	invites             map[inviteKey]*inviteSnapshot
	extensionHandler    func(Request) (Response, bool)
	subscriptions       map[string]*subscription
	config              Config
	ua                  *sipgo.UserAgent
	client              *sipgo.Client
	server              *sipgo.Server
	contact             wire.ContactHeader
	registrar           wire.Uri
	callback            func(Event)
	ctx                 context.Context
	cancel              context.CancelFunc
	listener            io.Closer
	workers             sync.WaitGroup
	closeOnce           sync.Once
	mu                  sync.Mutex
	calls               map[string]*call
	registered          bool
	closed              bool
	registerMu          operationLock
	registrationWake    chan time.Duration
	registerID          wire.CallIDHeader
	registerSeq         uint32
	offerHandler        func(string, []byte) ([]byte, error)
	earlyOfferHandler   func(string, []byte) ([]byte, error)
	answerHandler       func(string, []byte) error
	answerCommitHandler func(string, []byte, []byte) error
	transferHandler     func(context.Context, string, string) error
}

// NewClient opens a local signaling endpoint. The callback may be called from
// multiple goroutines; it must return promptly and must not call Close inline.
func NewClient(config Config, callback func(Event)) (*Client, error) {
	return newClient(config, callback, nil)
}

func newClient(config Config, callback func(Event), configure func(*Client)) (*Client, error) {
	if config.Server == "" || config.Username == "" {
		return nil, errors.New("SIP server and username are required")
	}
	for _, value := range []string{config.Server, config.Domain, config.Username, config.AuthUsername, config.DisplayName, config.OutboundProxy} {
		if strings.ContainsAny(value, "\r\n") {
			return nil, errors.New("SIP settings contain a newline")
		}
	}
	config.Transport = strings.ToLower(config.Transport)
	if config.Transport == "" {
		config.Transport = "udp"
	}
	if config.Transport != "udp" && config.Transport != "tcp" && config.Transport != "tls" {
		return nil, errors.New("SIP transport must be udp, tcp or tls")
	}
	if config.OutboundProxy != "" {
		host, portText, err := net.SplitHostPort(config.OutboundProxy)
		if err != nil {
			host = strings.Trim(config.OutboundProxy, "[]")
			portText = strconv.Itoa(int(wire.DefaultPort(config.Transport)))
		}
		port, err := strconv.Atoi(portText)
		if host == "" || strings.ContainsAny(host, " \t\x00/?#@;[]") || (strings.Contains(host, ":") && net.ParseIP(host) == nil) || err != nil || port < 1 || port > 65535 {
			return nil, errors.New("invalid outbound proxy address")
		}
		config.OutboundProxy = net.JoinHostPort(host, strconv.Itoa(port))
	}
	defaultPort := config.Port == 0
	if config.Port == 0 {
		config.Port = 5060
		if config.Transport == "tls" {
			config.Port = 5061
		}
	}
	if config.Port < 1 || config.Port > 65535 {
		return nil, errors.New("invalid SIP port")
	}
	if config.Domain == "" {
		config.Domain = config.Server
	}
	if config.AuthUsername == "" {
		config.AuthUsername = config.Username
	}
	if config.MinSessionExpires == 0 {
		config.MinSessionExpires = 90 * time.Second
	}
	if config.SessionExpires == 0 {
		config.SessionExpires = 30 * time.Minute
	}
	if config.MinSessionExpires < 90*time.Second || config.SessionExpires < config.MinSessionExpires || config.SessionExpires > 24*time.Hour {
		return nil, errors.New("session interval must be between MinSessionExpires (at least 90s) and 24h")
	}
	if config.KeepAliveInterval == 0 {
		config.KeepAliveInterval = 25 * time.Second
	}
	if config.KeepAliveInterval != -1 && (config.KeepAliveInterval < time.Second || config.KeepAliveInterval > 10*time.Minute) {
		return nil, errors.New("KeepAliveInterval must be between 1s and 10m, or -1 to disable")
	}
	if config.MaxRedirects < 0 || config.MaxRedirects > 5 {
		return nil, errors.New("MaxRedirects must be between zero and five")
	}
	if config.Expires <= 0 {
		config.Expires = time.Hour
	}
	if config.Expires < time.Second {
		return nil, errors.New("registration expiry must be at least one second")
	}
	if config.Resolver == nil {
		config.Resolver = net.DefaultResolver
	}
	address := config.LocalAddress
	var initialTargets []string
	if address == "" {
		lookupCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		var err error
		initialTargets, err = resolveRegistrarTargets(lookupCtx, config, defaultPort)
		cancel()
		if err != nil {
			return nil, fmt.Errorf("resolve SIP server: %w", err)
		}
		remote, err := net.ResolveUDPAddr("udp", initialTargets[0])
		if err != nil {
			return nil, fmt.Errorf("resolve SIP server: %w", err)
		}
		route, err := net.DialUDP("udp", nil, remote)
		if err != nil {
			return nil, fmt.Errorf("find signaling address: %w", err)
		}
		address = net.JoinHostPort(route.LocalAddr().(*net.UDPAddr).IP.String(), "0")
		route.Close()
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("local SIP address must include a port: %w", err)
	}
	if ip := net.ParseIP(host); ip == nil || ip.IsUnspecified() {
		return nil, errors.New("local SIP address must be a concrete IP, not a wildcard")
	}
	tlsConfig := config.TLSConfig
	if tlsConfig == nil {
		tlsConfig = &tls.Config{}
	} else {
		tlsConfig = tlsConfig.Clone()
	}
	if tlsConfig.MinVersion < tls.VersionTLS12 {
		tlsConfig.MinVersion = tls.VersionTLS12
	}
	if tlsConfig.ServerName == "" {
		tlsConfig.ServerName = config.Server
		if config.OutboundProxy != "" {
			tlsConfig.ServerName, _, _ = net.SplitHostPort(config.OutboundProxy)
		}
	}
	ua, err := sipgo.NewUA(sipgo.WithUserAgent("Voiper"), sipgo.WithUserAgentHostname(config.Domain), sipgo.WithUserAgenTLSConfig(tlsConfig), sipgo.WithUserAgentDNSResolver(config.Resolver))
	if err != nil {
		return nil, err
	}
	c := &Client{invites: make(map[inviteKey]*inviteSnapshot), config: config, ua: ua, callback: callback, calls: make(map[string]*call), registerID: wire.CallIDHeader(uuid.NewString()), registrationWake: make(chan time.Duration, 1), registerMu: make(operationLock, 1)}
	if len(initialTargets) > 0 {
		c.registrarTargets = initialTargets
		c.resolvedTargetsAt = time.Now()
	}
	c.ctx, c.cancel = context.WithCancel(context.Background())
	c.registrar = wire.Uri{Scheme: "sip", Host: config.Server, Port: config.Port}
	if defaultPort {
		c.registrar.Port = 0
	}
	if config.Transport == "tls" {
		c.registrar.Scheme = "sips"
	}
	var serve func() error
	var ready chan struct{}
	if config.Transport == "udp" {
		socket, err := net.ListenPacket("udp", address)
		if err != nil {
			c.Close()
			return nil, err
		}
		ready = make(chan struct{})
		conn := &readyPacketConn{PacketConn: socket, ready: ready}
		c.listener = socket
		address = socket.LocalAddr().String()
		serve = func() error { return c.server.ServeUDP(conn) }
	} else if config.Transport == "tcp" {
		listener, err := net.Listen("tcp", address)
		if err != nil {
			c.Close()
			return nil, err
		}
		c.listener = listener
		address = listener.Addr().String()
		serve = func() error { return c.server.ServeTCP(listener) }
	}
	clientOptions := []sipgo.ClientOption{sipgo.WithClientAddr(address), sipgo.WithClientNAT()}
	if config.Transport == "udp" {
		clientOptions = append(clientOptions, sipgo.WithClientConnectionAddr(address))
	}
	c.client, err = sipgo.NewClient(ua, clientOptions...)
	if err != nil {
		c.Close()
		return nil, err
	}
	c.client.TxRequester = inviteRequester{client: c}
	c.server, err = sipgo.NewServer(ua)
	if err != nil {
		c.Close()
		return nil, err
	}
	host, portText, _ := net.SplitHostPort(address)
	port, _ := strconv.Atoi(portText)
	c.contact = wire.ContactHeader{Address: wire.Uri{Scheme: c.registrar.Scheme, User: config.Username, Host: host, Port: port, UriParams: wire.NewParams()}}
	c.contact.Address.UriParams.Add("transport", config.Transport)
	c.installHandlers()
	if configure != nil {
		configure(c)
	}
	c.ua.TransportLayer().OnMessage(c.onWireMessage)
	c.workers.Add(1)
	go c.expireInvites()
	if serve != nil {
		c.workers.Add(1)
		go func() {
			defer c.workers.Done()
			if err := serve(); err != nil && c.ctx.Err() == nil {
				c.emit(Event{Type: "registration", State: "failed", Message: err.Error()})
			}
		}()
		if ready != nil {
			<-ready
		}
	}
	return c, nil
}

type readyPacketConn struct {
	net.PacketConn
	ready chan struct{}
	once  sync.Once
}

func (c *readyPacketConn) ReadFrom(b []byte) (int, net.Addr, error) {
	c.once.Do(func() { close(c.ready) })
	return c.PacketConn.ReadFrom(b)
}

func (c *Client) emit(event Event) {
	if c.callback != nil {
		c.callback(event)
	}
}

func (c *Client) request(method wire.RequestMethod, recipient wire.Uri, body []byte) *wire.Request {
	req := wire.NewRequest(method, recipient)
	req.SetTransport(c.config.Transport)
	params := wire.NewParams()
	params.Add("tag", uuid.NewString())
	req.AppendHeader(&wire.FromHeader{DisplayName: c.config.DisplayName, Address: wire.Uri{Scheme: c.registrar.Scheme, User: c.config.Username, Host: c.config.Domain}, Params: params})
	req.AppendHeader(&wire.ToHeader{Address: recipient})
	c.mu.Lock()
	req.AppendHeader(wire.HeaderClone(&c.contact))
	c.mu.Unlock()
	c.routeInitialRequest(req)
	req.SetBody(body)
	return req
}

// Account addresses use the configured PBX, while an explicit foreign authority
// remains directly routable. Dialog requests instead follow Contact/Record-Route.
func (c *Client) routeInitialRequest(req *wire.Request) {
	req.SetDestination("")
	if c.config.OutboundProxy != "" || c.accountAuthority(req.Recipient) {
		c.mu.Lock()
		selected := c.selectedTarget
		c.mu.Unlock()
		if selected != "" {
			req.SetDestination(selected)
		} else if c.config.OutboundProxy != "" {
			req.SetDestination(c.config.OutboundProxy)
		} else {
			req.SetDestination(net.JoinHostPort(c.registrar.Host, strconv.Itoa(c.registrar.Port)))
		}
	}
}

func (c *Client) recipient(target string) (wire.Uri, error) {
	if target == "" || len(target) > 2048 || strings.ContainsAny(target, "\r\n\t \x00") {
		return wire.Uri{}, errors.New("invalid SIP destination")
	}
	if !strings.Contains(target, ":") {
		if !strings.Contains(target, "@") {
			target += "@" + c.config.Domain
		}
		target = "sip:" + target
	}
	var uri wire.Uri
	if err := wire.ParseUri(target, &uri); err != nil {
		return uri, err
	}
	if uri.Scheme != "sip" && uri.Scheme != "sips" {
		return uri, errors.New("destination must use sip or sips")
	}
	if uri.Scheme == "sips" && c.config.Transport != "tls" {
		return uri, errors.New("sips destinations require a TLS account")
	}
	if transport, ok := uri.UriParams.Get("transport"); ok && !strings.EqualFold(transport, c.config.Transport) {
		return uri, errors.New("destination transport differs from account transport")
	}
	return uri, nil
}

func (c *Client) do(ctx context.Context, req *wire.Request) (*wire.Response, error) {
	if err := c.ctx.Err(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	stop := context.AfterFunc(c.ctx, cancel)
	defer stop()
	res, err := c.client.Do(ctx, req)
	for attempt := 0; err == nil && res != nil && (res.StatusCode == 401 || res.StatusCode == 407) && attempt < 2; attempt++ {
		if !c.credentialsAllowed(req, res, req.Recipient) {
			return res, &ResponseError{Method: string(req.Method), Status: res.StatusCode, Reason: "authentication refused outside account scope"}
		}
		res, err = c.client.DoDigestAuth(ctx, req, res, sipgo.DigestAuth{Username: c.config.AuthUsername, Password: c.config.Password})
	}
	if err != nil {
		return nil, err
	}
	if res == nil {
		return nil, errors.New("SIP transaction ended without a response")
	}
	if !res.IsSuccess() {
		return res, &ResponseError{Method: string(req.Method), Status: res.StatusCode, Reason: res.Reason}
	}
	return res, nil
}

// Register performs the initial exchange and refreshes until ctx is canceled or
// Close is called. Canceling ctx closes the account's signaling resources.
func (c *Client) Register(ctx context.Context) error {
	if err := c.registerMu.Lock(ctx); err != nil {
		return err
	}
	defer c.registerMu.Unlock()
	c.mu.Lock()
	closed, registered := c.closed, c.registered
	c.mu.Unlock()
	if closed {
		return errors.New("SIP client is closed")
	}
	if registered {
		return nil
	}
	c.emit(Event{Type: "registration", State: "registering"})
	expiry, err := c.registration(ctx, c.config.Expires, true)
	for attempt := 0; err != nil && attempt < 2 && ctx.Err() == nil && c.ctx.Err() == nil; attempt++ {
		if !IsTransientRegistrationError(err) {
			break
		}
		timer := time.NewTimer(time.Second << attempt)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-c.ctx.Done():
			timer.Stop()
			return c.ctx.Err()
		}
		expiry, err = c.registration(ctx, c.config.Expires, true)
	}
	if err != nil {
		c.emit(registrationFailure(err))
		return err
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return errors.New("SIP client is closed")
	}
	c.registered = true
	c.workers.Add(1)
	if c.config.KeepAliveInterval > 0 {
		c.workers.Add(1)
		go c.keepalive(ctx)
	}
	c.mu.Unlock()
	go func() {
		defer c.workers.Done()
		backoff := time.Second
		timer := time.NewTimer(expiry * 4 / 5)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				go c.Close()
				return
			case <-c.ctx.Done():
				return
			case granted := <-c.registrationWake:
				expiry = granted
				backoff = time.Second
				timer.Reset(expiry * 4 / 5)
			case <-timer.C:
				if err := c.registerMu.Lock(ctx); err != nil {
					go c.Close()
					return
				}
				granted, err := c.registration(ctx, expiry, true)
				c.registerMu.Unlock()
				if err != nil {
					if ctx.Err() != nil || c.ctx.Err() != nil {
						continue
					}
					c.emit(registrationFailure(err))
					if !IsTransientRegistrationError(err) {
						c.mu.Lock()
						c.registered = false
						c.mu.Unlock()
						select {
						case <-ctx.Done():
							go c.Close()
						case <-c.ctx.Done():
						}
						return
					}
					timer.Reset(backoff)
					backoff = min(backoff*2, time.Minute)
					continue
				}
				expiry = granted
				backoff = time.Second
				timer.Reset(expiry * 4 / 5)
			}
		}
	}()
	return nil
}

var ErrRegistrationNotRunning = errors.New("registration is not running; call Register first")

// RefreshRegistration renews a running registration after a network change.
// Its context bounds this exchange; Register's context still owns the account.
func (c *Client) RefreshRegistration(ctx context.Context) error {
	if err := c.registerMu.Lock(ctx); err != nil {
		return err
	}
	defer c.registerMu.Unlock()
	c.mu.Lock()
	active := c.registered && !c.closed
	c.mu.Unlock()
	if !active {
		return ErrRegistrationNotRunning
	}
	expiry, err := c.registration(ctx, c.config.Expires, true)
	if err != nil {
		return err
	}
	select {
	case <-c.registrationWake:
	default:
	}
	c.registrationWake <- expiry
	return nil
}

func (c *Client) registrationAt(ctx context.Context, expiry time.Duration, retry bool, destination string) (time.Duration, error) {
	if c.config.Transport != "udp" {
		if err := c.connectFlow(ctx, destination); err != nil {
			return expiry, err
		}
	}
	req := c.request(wire.REGISTER, c.registrar, nil)
	req.SetDestination(destination)
	req.To().Address = wire.Uri{Scheme: c.registrar.Scheme, User: c.config.Username, Host: c.config.Domain}
	c.registerSeq++
	req.AppendHeader(&c.registerID)
	req.AppendHeader(&wire.CSeqHeader{SeqNo: c.registerSeq, MethodName: wire.REGISTER})
	req.AppendHeader(wire.NewHeader("Expires", strconv.Itoa(int(expiry/time.Second))))
	res, err := c.do(ctx, req)
	c.registerSeq = req.CSeq().SeqNo
	if retry && err != nil && res != nil && res.StatusCode == 423 {
		if h := res.GetHeader("Min-Expires"); h != nil {
			n, e := strconv.Atoi(h.Value())
			if e == nil && n > 0 && n <= 86400 {
				return c.registrationAt(ctx, time.Duration(n)*time.Second, false, destination)
			}
		}
	}
	if err != nil {
		return expiry, err
	}
	if h := res.GetHeader("Expires"); h != nil {
		if n, e := strconv.Atoi(h.Value()); e == nil {
			expiry = time.Duration(n) * time.Second
		}
	}
	for _, h := range res.GetHeaders("Contact") {
		if contact, ok := h.(*wire.ContactHeader); ok && contact.Address.User == c.config.Username {
			if v, ok := contact.Params.Get("expires"); ok {
				if n, e := strconv.Atoi(v); e == nil {
					expiry = time.Duration(n) * time.Second
				}
			}
		}
	}
	if expiry < time.Second {
		return expiry, errors.New("registrar returned an expired registration")
	}
	// Some PBXs, SwyxWare among them, identify themselves with User-Agent in responses.
	server := ""
	if h := res.GetHeader("Server"); h != nil {
		server = h.Value()
	} else if h := res.GetHeader("User-Agent"); h != nil {
		server = h.Value()
	}
	c.mu.Lock()
	c.selectedTarget = destination
	c.mu.Unlock()
	c.emit(Event{Type: "registration", State: "registered", Server: server})
	return expiry, nil
}

func (c *Client) Close() error {
	var err error
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.closed = true
		calls := make([]*call, 0, len(c.calls))
		for _, call := range c.calls {
			calls = append(calls, call)
		}
		c.mu.Unlock()
		c.cancel()
		unregisterCtx, unregisterCancel := context.WithTimeout(context.Background(), time.Second)
		if c.registerMu.Lock(unregisterCtx) == nil {
			c.mu.Lock()
			registered := c.registered
			c.registered = false
			c.mu.Unlock()
			if registered {
				c.unregister()
			}
			c.registerMu.Unlock()
		}
		unregisterCancel()
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer closeCancel()
		for _, call := range calls {
			call.cancel()
			call.mu.Lock()
			ready := call.ready
			call.mu.Unlock()
			if ready && closeCtx.Err() == nil && call.operation.TryLock() {
				if call.incoming != nil {
					_ = c.byeDialog(closeCtx, call)
				} else {
					_ = c.byeDialog(closeCtx, call)
				}
				call.operation.Unlock()
			}
			c.finish(call, "account closed")
		}
		if c.listener != nil {
			c.listener.Close()
		}
		if c.ua != nil {
			err = c.ua.Close()
		}
		c.workers.Wait()
	})
	return err
}

func (c *Client) connectFlow(ctx context.Context, destination string) error {
	request := c.request(wire.OPTIONS, c.registrar, nil)
	if destination != "" {
		request.SetDestination(destination)
	}
	if err := sipgo.ClientRequestBuild(c.client, request); err != nil {
		return err
	}
	connectCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	stop := context.AfterFunc(c.ctx, cancel)
	defer stop()
	conn, err := c.ua.TransportLayer().ClientRequestConnection(connectCtx, request)
	cancel()
	if err != nil {
		return fmt.Errorf("connect SIP %s: %w", c.config.Transport, err)
	}
	host, portText, _ := net.SplitHostPort(conn.LocalAddr().String())
	port, _ := strconv.Atoi(portText)
	c.mu.Lock()
	c.contact.Address.Host = host
	c.contact.Address.Port = port

	c.mu.Unlock()
	conn.TryClose()
	return nil
}

func (c *Client) dialogUA() *sipgo.DialogUA {
	c.mu.Lock()
	defer c.mu.Unlock()
	return &sipgo.DialogUA{Client: c.client, ContactHDR: *wire.HeaderClone(&c.contact).(*wire.ContactHeader)}
}

type ResponseError struct {
	RetryAfter time.Duration
	Method     string
	Status     int
	Reason     string
}

func (e *ResponseError) Error() string { return fmt.Sprintf("%s: %d %s", e.Method, e.Status, e.Reason) }

func (c *Client) unregister() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	req := c.request(wire.REGISTER, c.registrar, nil)
	req.To().Address = wire.Uri{Scheme: c.registrar.Scheme, User: c.config.Username, Host: c.config.Domain}
	c.registerSeq++
	req.AppendHeader(&c.registerID)
	req.AppendHeader(&wire.CSeqHeader{SeqNo: c.registerSeq, MethodName: wire.REGISTER})
	req.AppendHeader(wire.NewHeader("Expires", "0"))
	res, err := c.client.Do(ctx, req)
	if err == nil && res != nil && (res.StatusCode == 401 || res.StatusCode == 407) {
		_, _ = c.client.DoDigestAuth(ctx, req, res, sipgo.DigestAuth{Username: c.config.AuthUsername, Password: c.config.Password})
	}
}

// LocalAddr returns the advertised SIP endpoint, including its assigned port.
// TCP and TLS use the client connection's endpoint after registration.
func (c *Client) LocalAddr() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.contact.Address.HostPort()
}

// LocalAddressCurrent reports whether the system still routes to the registrar
// from the address this client bound. A client never rebinds by itself, so after
// a network change a new client is needed. A configured address always counts.
func (c *Client) LocalAddressCurrent() bool {
	if c.config.LocalAddress != "" {
		return true
	}
	c.mu.Lock()
	target := c.selectedTarget
	if target == "" && len(c.registrarTargets) > 0 {
		target = c.registrarTargets[0]
	}
	host := c.contact.Address.Host
	c.mu.Unlock()
	remote, err := net.ResolveUDPAddr("udp", target)
	if err != nil {
		return false
	}
	// Connecting a UDP socket selects a route without sending anything.
	route, err := net.DialUDP("udp", nil, remote)
	if err != nil {
		return false
	}
	defer route.Close()
	return route.LocalAddr().(*net.UDPAddr).IP.Equal(net.ParseIP(host))
}

// ValidateTarget checks a destination before the application changes other calls.
func (c *Client) ValidateTarget(target string) error {
	_, _, err := c.inviteTarget(target)
	return err
}

// registrationFailure carries the SIP status of a rejection, so that callers
// can tell a refused account from an unreachable registrar.
func registrationFailure(err error) Event {
	event := Event{Type: "registration", State: "failed", Message: err.Error()}
	var response *ResponseError
	if errors.As(err, &response) {
		event.StatusCode = response.Status
	}
	return event
}

// IsTransientRegistrationError identifies failures suitable for a bounded retry.
func IsTransientRegistrationError(err error) bool {
	var response *ResponseError
	if errors.As(err, &response) {
		switch response.Status {
		case 408, 500, 502, 503, 504:
			return true
		default:
			return false
		}
	}
	var certificate *tls.CertificateVerificationError
	if errors.As(err, &certificate) {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	return true
}
