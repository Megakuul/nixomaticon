package sip

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	wire "github.com/emiago/sipgo/sip"
	"github.com/google/uuid"
)

type subscription struct {
	id, target, event, accept string
	operation                 operationLock
	mu                        sync.Mutex
	wireID                    string
	address, recipient        wire.Uri
	from                      *wire.FromHeader
	to                        *wire.ToHeader
	routes                    []wire.Header
	sequence, remoteSequence  uint32
	ctx                       context.Context
	cancel                    context.CancelFunc
	wake                      chan struct{}
	refreshAt, expiresAt      time.Time
	notifyBy, retryAfter      time.Time
	recreate, stopped         bool
	backoff                   time.Duration
}

// Subscribe creates a watch whose logical ID remains stable across recovered
// dialogs. Presence events contain the event package, original body and type.
func (c *Client) Subscribe(ctx context.Context, target, event, accept string) (string, error) {
	uri, err := c.recipient(target)
	if err != nil {
		return "", err
	}
	if event == "" || accept == "" || strings.ContainsAny(event+accept, "\r\n") {
		return "", errors.New("invalid subscription event or content type")
	}
	ctx, cancel := context.WithCancel(ctx)
	sub := &subscription{id: uuid.NewString(), target: uri.String(), address: uri, event: event, accept: accept, ctx: ctx, cancel: cancel, operation: make(operationLock, 1), wake: make(chan struct{}, 1), recreate: true, backoff: time.Second}
	c.mu.Lock()
	if c.closed || len(c.subscriptions) >= 256 {
		c.mu.Unlock()
		cancel()
		return "", errors.New("SIP client is closed or subscription limit reached")
	}
	if c.subscriptions == nil {
		c.subscriptions = make(map[string]*subscription)
	}
	c.subscriptions[sub.id] = sub
	c.mu.Unlock()
	if _, err := c.refreshSubscription(ctx, sub, time.Hour); err != nil {
		c.removeSubscription(sub)
		return "", err
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		c.removeSubscription(sub)
		return "", errors.New("SIP client is closed")
	}
	c.workers.Add(1)
	c.mu.Unlock()
	go c.runSubscription(sub)
	return sub.id, nil
}

// RefreshSubscriptions schedules refreshes after a network change. Network
// results arrive as presence events; server Retry-After delays remain binding.
func (c *Client) RefreshSubscriptions(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return errors.New("SIP client is closed")
	}
	for _, sub := range c.subscriptions {
		sub.mu.Lock()
		sub.refreshAt = time.Now()
		if sub.retryAfter.After(sub.refreshAt) {
			sub.refreshAt = sub.retryAfter
		}
		sub.mu.Unlock()
		sub.signal()
	}
	return nil
}

func (sub *subscription) signal() {
	select {
	case sub.wake <- struct{}{}:
	default:
	}
}

func (c *Client) runSubscription(sub *subscription) {
	defer c.workers.Done()
	defer c.removeSubscription(sub)
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	for {
		sub.mu.Lock()
		if sub.stopped {
			sub.mu.Unlock()
			return
		}
		next := sub.refreshAt
		for _, deadline := range []time.Time{sub.notifyBy, sub.expiresAt} {
			if !deadline.IsZero() && deadline.Before(next) {
				next = deadline
			}
		}
		sub.mu.Unlock()
		timer.Reset(max(time.Until(next), 0))
		select {
		case <-sub.ctx.Done():
			return
		case <-c.ctx.Done():
			return
		case <-sub.wake:
		case <-timer.C:
		}
		sub.mu.Lock()
		now := time.Now()
		expired := (!sub.notifyBy.IsZero() && !now.Before(sub.notifyBy)) || (!sub.expiresAt.IsZero() && !now.Before(sub.expiresAt))
		if expired {
			sub.recreate = true
			sub.notifyBy, sub.expiresAt = time.Time{}, time.Time{}
			sub.refreshAt = now.Add(sub.backoff)
			if sub.retryAfter.After(sub.refreshAt) {
				sub.refreshAt = sub.retryAfter
			}
			sub.backoff = min(sub.backoff*2, time.Minute)
		}
		due := !now.Before(sub.refreshAt)
		sub.mu.Unlock()
		if expired {
			c.subscriptionUnknown(sub, "subscription expired or notifier did not confirm refresh")
		}
		if !due {
			continue
		}
		if _, err := c.refreshSubscription(sub.ctx, sub, time.Hour); err != nil {
			if sub.ctx.Err() != nil || c.ctx.Err() != nil {
				return
			}
			c.subscriptionUnknown(sub, err.Error())
			var response *ResponseError
			lost := errors.Is(err, context.DeadlineExceeded) || errors.Is(err, wire.ErrTransactionTimeout) || errors.Is(err, wire.ErrTransactionTransport) || (errors.As(err, &response) && (response.Status == 408 || response.Status == 481))
			if !lost && !IsTransientRegistrationError(err) {
				return
			}
			sub.mu.Lock()
			if lost || sub.expiresAt.IsZero() || time.Now().After(sub.expiresAt) {
				sub.recreate = true
				sub.notifyBy, sub.expiresAt = time.Time{}, time.Time{}
			}
			sub.refreshAt = time.Now().Add(sub.backoff)
			if sub.retryAfter.After(sub.refreshAt) {
				sub.refreshAt = sub.retryAfter
			}
			sub.backoff = min(sub.backoff*2, time.Minute)
			sub.mu.Unlock()
		}
	}
}

func (c *Client) subscriptionUnknown(sub *subscription, message string) {
	c.emit(Event{Type: "presence", CallID: sub.id, RemoteURI: sub.target, State: "unknown", Message: message})
}

func (c *Client) resetSubscription(sub *subscription) {
	params := wire.NewParams()
	params.Add("tag", uuid.NewString())
	sub.wireID = uuid.NewString()
	sub.recipient = sub.address
	sub.from = &wire.FromHeader{DisplayName: c.config.DisplayName, Address: wire.Uri{Scheme: c.registrar.Scheme, User: c.config.Username, Host: c.config.Domain}, Params: params}
	sub.to = &wire.ToHeader{Address: sub.address, Params: wire.NewParams()}
	sub.routes = nil
	sub.sequence, sub.remoteSequence = 0, 0
	sub.notifyBy, sub.expiresAt = time.Time{}, time.Time{}
	sub.recreate = false
}

func (c *Client) refreshSubscription(ctx context.Context, sub *subscription, expiry time.Duration) (time.Duration, error) {
	if err := sub.operation.Lock(ctx); err != nil {
		return expiry, err
	}
	defer sub.operation.Unlock()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if expiry > 0 {
		stop := context.AfterFunc(sub.ctx, cancel)
		defer stop()
		if err := sub.ctx.Err(); err != nil {
			return expiry, err
		}
	}
	sub.mu.Lock()
	if sub.stopped && expiry > 0 {
		sub.mu.Unlock()
		return expiry, context.Canceled
	}
	if sub.recreate && expiry > 0 {
		c.resetSubscription(sub)
	}
	if sub.recreate && expiry == 0 {
		sub.mu.Unlock()
		return 0, nil
	}
	req := wire.NewRequest(wire.SUBSCRIBE, sub.recipient)
	req.SetTransport(c.config.Transport)
	if tag, _ := sub.to.Params.Get("tag"); tag == "" {
		c.routeInitialRequest(req)
	} else if c.config.OutboundProxy != "" {
		req.SetDestination(c.config.OutboundProxy)
	}
	req.AppendHeader(wire.HeaderClone(sub.from))
	req.AppendHeader(wire.HeaderClone(sub.to))
	callID := wire.CallIDHeader(sub.wireID)
	req.AppendHeader(&callID)
	sub.sequence++
	req.AppendHeader(&wire.CSeqHeader{SeqNo: sub.sequence, MethodName: wire.SUBSCRIBE})
	for _, route := range sub.routes {
		req.AppendHeader(wire.HeaderClone(route))
	}
	notifySequence := sub.remoteSequence
	if expiry > 0 {
		sub.notifyBy = time.Now().Add(32 * time.Second)
	}
	sub.mu.Unlock()
	c.mu.Lock()
	req.AppendHeader(wire.HeaderClone(&c.contact))
	c.mu.Unlock()
	req.AppendHeader(wire.NewHeader("Event", sub.event))
	req.AppendHeader(wire.NewHeader("Accept", sub.accept))
	req.AppendHeader(wire.NewHeader("Expires", strconv.Itoa(int(expiry/time.Second))))
	res, err := c.do(ctx, req)
	sub.mu.Lock()
	defer sub.mu.Unlock()
	sub.sequence = req.CSeq().SeqNo
	if err != nil {
		if res != nil && res.GetHeader("Retry-After") != nil {
			if delay, ok := subscriptionSeconds(strings.Split(res.GetHeader("Retry-After").Value(), ";")[0]); ok {
				sub.retryAfter = time.Now().Add(delay)
			}
		}
		return expiry, err
	}
	if expiry == 0 || sub.recreate || sub.stopped {
		return 0, nil
	}
	remoteTag, _ := sub.to.Params.Get("tag")
	responseTag, _ := res.To().Params.Get("tag")
	if remoteTag == "" || remoteTag == responseTag {
		sub.to = wire.HeaderClone(res.To()).(*wire.ToHeader)
		if contact := res.Contact(); contact != nil {
			sub.recipient = contact.Address
		}
		if len(sub.routes) == 0 {
			routes := res.GetHeaders("Record-Route")
			for i := len(routes) - 1; i >= 0; i-- {
				sub.routes = append(sub.routes, wire.NewHeader("Route", routes[i].Value()))
			}
		}
	}
	if header := res.GetHeader("Expires"); header != nil {
		granted, ok := subscriptionSeconds(header.Value())
		if !ok || granted < time.Second {
			return expiry, errors.New("invalid subscription expiry")
		}
		expiry = min(expiry, granted)
	}
	if sub.remoteSequence == notifySequence || sub.expiresAt.IsZero() {
		sub.expiresAt = time.Now().Add(expiry)
		sub.refreshAt = time.Now().Add(expiry * 4 / 5)
	}
	sub.retryAfter = time.Time{}
	return expiry, nil
}

func (c *Client) removeSubscription(sub *subscription) {
	sub.cancel()
	c.mu.Lock()
	if c.subscriptions[sub.id] == sub {
		delete(c.subscriptions, sub.id)
	}
	c.mu.Unlock()
}

// Unsubscribe stops recovery before asking the notifier to end the dialog.
// Already removed watches are harmless, including those rejected by a server.
func (c *Client) Unsubscribe(ctx context.Context, id string) error {
	c.mu.Lock()
	sub := c.subscriptions[id]
	c.mu.Unlock()
	if sub == nil {
		return nil
	}
	sub.mu.Lock()
	sub.stopped = true
	sub.mu.Unlock()
	c.removeSubscription(sub)
	_, err := c.refreshSubscription(ctx, sub, 0)
	return err
}

func subscriptionSeconds(value string) (time.Duration, bool) {
	n, err := strconv.ParseUint(strings.TrimSpace(value), 10, 31)
	return time.Duration(n) * time.Second, err == nil
}

func (c *Client) onNotify(req *wire.Request, tx wire.ServerTransaction) {
	if len(req.Body()) > 65536 {
		respond(req, tx, 413, "Content Too Large")
		return
	}
	if req.CallID() == nil || req.From() == nil || req.To() == nil || req.CSeq() == nil {
		respond(req, tx, 400, "Missing Dialog Headers")
		return
	}
	event := req.GetHeader("Event")
	state := req.GetHeader("Subscription-State")
	if event == nil || state == nil {
		respond(req, tx, 400, "Missing Event Headers")
		return
	}
	var sub *subscription
	c.mu.Lock()
	for _, candidate := range c.subscriptions {
		candidate.mu.Lock()
		matches := candidate.wireID == string(*req.CallID())
		candidate.mu.Unlock()
		if matches {
			sub = candidate
			break
		}
	}
	c.mu.Unlock()
	if sub == nil {
		if c.handleExtension(req, tx) {
			return
		}
		if strings.EqualFold(strings.TrimSpace(strings.Split(event.Value(), ";")[0]), "refer") {
			c.onTransferNotify(req, tx)
			return
		}
		respond(req, tx, 481, "Subscription Does Not Exist")
		return
	}
	parts := strings.Split(state.Value(), ";")
	status := strings.ToLower(strings.TrimSpace(parts[0]))
	if status != "active" && status != "pending" && status != "terminated" {
		respond(req, tx, 400, "Invalid Subscription State")
		return
	}
	params := make(map[string]string)
	for _, part := range parts[1:] {
		key, value, _ := strings.Cut(part, "=")
		params[strings.ToLower(strings.TrimSpace(key))] = strings.TrimSpace(value)
	}
	sub.mu.Lock()
	local, _ := req.To().Params.Get("tag")
	expectedLocal, _ := sub.from.Params.Get("tag")
	remote, _ := req.From().Params.Get("tag")
	expectedRemote, _ := sub.to.Params.Get("tag")
	valid := !sub.recreate && !sub.stopped && sub.ctx.Err() == nil && sub.wireID == string(*req.CallID()) && local == expectedLocal && remote != "" && (expectedRemote == "" || expectedRemote == remote) && strings.Split(event.Value(), ";")[0] == sub.event && req.CSeq().SeqNo > sub.remoteSequence
	if !valid {
		sub.mu.Unlock()
		respond(req, tx, 481, "Subscription Does Not Match")
		return
	}
	sub.remoteSequence = req.CSeq().SeqNo
	sub.notifyBy = time.Time{}
	if expectedRemote == "" {
		to := req.From().AsTo()
		sub.to = &to
		for _, route := range req.GetHeaders("Record-Route") {
			sub.routes = append(sub.routes, wire.NewHeader("Route", route.Value()))
		}
	}
	if contact := req.Contact(); contact != nil {
		sub.recipient = contact.Address
	}
	if status == "terminated" {
		sub.recreate = true
		sub.expiresAt = time.Time{}
		reason := strings.ToLower(params["reason"])
		switch reason {
		case "timeout", "deactivated", "probation":
			delay := sub.backoff
			if reason == "probation" {
				if retry, ok := subscriptionSeconds(params["retry-after"]); ok {
					delay = max(delay, retry)
				}
			}
			sub.retryAfter = time.Now().Add(delay)
			sub.refreshAt = sub.retryAfter
			sub.backoff = min(sub.backoff*2, time.Minute)
		default:
			sub.stopped = true
		}
	} else {
		sub.backoff = time.Second
		if expiry, ok := subscriptionSeconds(params["expires"]); ok {
			sub.expiresAt = time.Now().Add(expiry)
			sub.refreshAt = time.Now().Add(expiry * 4 / 5)
		}
	}
	sub.mu.Unlock()
	contentType := ""
	if req.ContentType() != nil {
		contentType = req.ContentType().Value()
	}
	respond(req, tx, 200, "OK")
	if len(req.Body()) > 0 {
		c.emit(Event{Type: "presence", CallID: sub.id, RemoteURI: sub.target, State: sub.event, Body: append([]byte(nil), req.Body()...), ContentType: contentType})
	}
	if status == "terminated" {
		c.subscriptionUnknown(sub, "subscription terminated: "+params["reason"])
	}
	sub.signal()
}
