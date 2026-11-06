package sip

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	wire "github.com/emiago/sipgo/sip"
)

const allowedMethods = "INVITE, ACK, CANCEL, BYE, OPTIONS, INFO, MESSAGE, NOTIFY, PRACK, UPDATE"

type sessionTimer struct {
	interval                        time.Duration
	deadline                        time.Time
	localRefresher, update, started bool
	wake                            chan struct{}
}

func parseSession(header wire.Header) (time.Duration, string, error) {
	if header == nil {
		return 0, "", nil
	}
	fields := strings.Split(header.Value(), ";")
	seconds, err := strconv.ParseUint(strings.TrimSpace(fields[0]), 10, 32)
	if err != nil || seconds == 0 || seconds > 86400 {
		return 0, "", errors.New("invalid Session-Expires")
	}
	refresher := ""
	for _, field := range fields[1:] {
		name, value, ok := strings.Cut(strings.TrimSpace(field), "=")
		if ok && strings.EqualFold(name, "refresher") {
			value = strings.ToLower(strings.TrimSpace(value))
			if value != "uac" && value != "uas" {
				return 0, "", errors.New("invalid session refresher")
			}
			if refresher != "" {
				return 0, "", errors.New("duplicate session refresher")
			}
			refresher = value
		}
	}
	return time.Duration(seconds) * time.Second, refresher, nil
}

func (c *Client) sessionResponse(req *wire.Request, tx wire.ServerTransaction) ([]wire.Header, bool) {
	headers := []wire.Header{wire.NewHeader("Supported", "100rel, timer"), wire.NewHeader("Allow", allowedMethods)}
	interval, refresher, err := parseSession(req.GetHeader("Session-Expires"))
	if err != nil {
		respond(req, tx, 400, "Invalid Session-Expires")
		return nil, false
	}
	supported := hasToken(req.GetHeaders("Supported"), "timer") || hasToken(req.GetHeaders("Require"), "timer")
	if interval == 0 && !supported {
		return headers, true
	}
	minimum := c.config.MinSessionExpires
	if header := req.GetHeader("Min-SE"); header != nil {
		value, _, err := parseSession(header)
		if err != nil {
			respond(req, tx, 400, "Invalid Min-SE")
			return nil, false
		}
		minimum = max(minimum, value)
	}
	if interval != 0 && interval < minimum {
		res := wire.NewResponseFromRequest(req, 422, "Session Interval Too Small", nil)
		res.AppendHeader(wire.NewHeader("Min-SE", strconv.Itoa(int(minimum/time.Second))))
		_ = tx.Respond(res)
		return nil, false
	}
	if interval == 0 {
		interval = max(c.config.SessionExpires, minimum)
	} else {
		interval = max(minimum, min(interval, c.config.SessionExpires))
	}
	if !supported {
		refresher = "uas"
	} else if refresher == "" {
		refresher = "uac"
	}
	headers = append(headers, wire.NewHeader("Session-Expires", fmt.Sprintf("%d;refresher=%s", int(interval/time.Second), refresher)))
	if supported {
		headers = append(headers, wire.NewHeader("Require", "timer"))
	}
	return headers, true
}

func (c *Client) acceptSession(cl *call, response *wire.Response, uac bool) error {
	interval, refresher, err := parseSession(response.GetHeader("Session-Expires"))
	if err != nil {
		return err
	}
	if interval != 0 && (interval < 90*time.Second || refresher == "") {
		return errors.New("invalid negotiated session timer")
	}
	if interval == 0 && hasToken(response.GetHeaders("Require"), "timer") {
		return errors.New("required session timer is missing")
	}
	cl.mu.Lock()
	cl.session.interval = interval
	cl.session.deadline = time.Now().Add(interval)
	cl.session.localRefresher = (uac && refresher == "uac") || (!uac && refresher == "uas")
	if uac && len(response.GetHeaders("Allow")) > 0 {
		cl.session.update = hasToken(response.GetHeaders("Allow"), "UPDATE")
	}
	if cl.session.wake == nil {
		cl.session.wake = make(chan struct{}, 1)
	}
	select {
	case cl.session.wake <- struct{}{}:
	default:
	}
	cl.mu.Unlock()
	return nil
}

func (c *Client) startSession(cl *call) {
	cl.mu.Lock()
	if cl.session.started || cl.session.interval == 0 {
		cl.mu.Unlock()
		return
	}
	cl.session.started = true
	if cl.session.wake == nil {
		cl.session.wake = make(chan struct{}, 1)
	}
	cl.mu.Unlock()
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.workers.Add(1)
	c.mu.Unlock()
	go c.refreshSession(cl)
}

func (c *Client) refreshSession(cl *call) {
	defer c.workers.Done()
	retryAt := time.Time{}
	for {
		cl.mu.Lock()
		session := cl.session
		cl.mu.Unlock()
		due := session.deadline.Add(-min(32*time.Second, session.interval/3))
		if session.localRefresher {
			due = session.deadline.Add(-session.interval / 2)
			if retryAt.After(due) {
				due = retryAt
			}
		}
		timer := time.NewTimer(max(0, time.Until(due)))
		var timerC <-chan time.Time
		if session.interval > 0 {
			timerC = timer.C
		}
		select {
		case <-cl.ctx.Done():
			timer.Stop()
			return
		case <-c.ctx.Done():
			timer.Stop()
			return
		case <-session.wake:
			timer.Stop()
			retryAt = time.Time{}
			continue
		case <-timerC:
		}
		if !session.localRefresher || !time.Now().Before(session.deadline) {
			c.expireSession(cl, "session refresh timed out")
			return
		}
		ctx, cancel := context.WithDeadline(cl.ctx, session.deadline)
		var err error
		if session.update {
			err = c.Update(ctx, cl.id, nil)
		} else {
			err = c.ReinviteWithOffer(ctx, cl.id, func() ([]byte, error) {
				// Offer the session as negotiated now. For an outgoing call localSDP
				// is still the initial multi-codec offer, and an answer to it may
				// move this side to a codec the peer does not send.
				c.mu.Lock()
				handler := c.offerHandler
				c.mu.Unlock()
				if handler != nil {
					return handler(cl.id, nil)
				}
				cl.mu.Lock()
				defer cl.mu.Unlock()
				return append([]byte(nil), cl.localSDP...), nil
			})
		}
		cancel()
		if err == nil {
			retryAt = time.Time{}
			continue
		}
		var response *ResponseError
		if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &response) && (response.Status == 408 || response.Status == 481)) {
			c.expireSession(cl, "session refresh failed: "+err.Error())
			return
		}
		retryAt = minTime(time.Now().Add(5*time.Second), session.deadline)
	}
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func (c *Client) expireSession(cl *call, reason string) {
	ctx, cancel := context.WithTimeout(c.ctx, 5*time.Second)
	defer cancel()
	if cl.operation.TryLock() {
		_ = c.byeDialog(ctx, cl)
		cl.operation.Unlock()
	}
	c.finish(cl, reason)
}

// Update sends an in-dialog UPDATE. A nil body refreshes the session without
// changing media; an SDP body is an offer and produces an updated event.
func (c *Client) Update(ctx context.Context, id string, offer []byte) error {
	var headers []wire.Header
	if len(offer) > 65536 {
		return errors.New("SDP offer exceeds 64 KiB")
	}
	if len(offer) > 0 {
		headers = append(headers, wire.NewHeader("Content-Type", "application/sdp"))
	}
	_, err := c.inDialog(ctx, id, wire.UPDATE, offer, headers...)
	return err
}

func (c *Client) onUpdate(req *wire.Request, tx wire.ServerTransaction) {
	if c.onEarlyUpdate(req, tx) {
		return
	}
	cl := c.match(req)
	if cl == nil {
		respond(req, tx, 481, "Dialog Does Not Exist")
		return
	}
	cl.mu.Lock()
	ready := cl.ready
	if len(req.GetHeaders("Allow")) > 0 {
		cl.session.update = hasToken(req.GetHeaders("Allow"), "UPDATE")
	}
	cl.mu.Unlock()
	if !ready || !cl.operation.TryLock() {
		respond(req, tx, 491, "Request Pending")
		return
	}
	defer cl.operation.Unlock()
	if req.CSeq() == nil {
		respond(req, tx, 400, "Missing CSeq")
		return
	}
	var err error
	if cl.incoming != nil {
		err = cl.incoming.ReadRequest(req, tx)
	} else {
		err = cl.outgoing.ReadRequest(req, tx)
	}
	if err != nil {
		respond(req, tx, 500, "Invalid Sequence")
		return
	}
	headers, ok := c.sessionResponse(req, tx)
	if !ok {
		return
	}
	var answer []byte
	if len(req.Body()) > 0 {
		if len(req.Body()) > 65536 || req.ContentType() == nil || req.ContentType().Value() != "application/sdp" {
			respond(req, tx, 415, "Unsupported Media Type")
			return
		}
		c.mu.Lock()
		handler := c.offerHandler
		c.mu.Unlock()
		if handler == nil {
			respond(req, tx, 488, "Media Negotiation Unavailable")
			return
		}
		answer, err = handler(cl.id, append([]byte(nil), req.Body()...))
		if err != nil || len(answer) == 0 || len(answer) > 65536 {
			respond(req, tx, 488, "Not Acceptable Here")
			return
		}
		headers = append(headers, wire.NewHeader("Content-Type", "application/sdp"))
	}
	res := wire.NewResponseFromRequest(req, 200, "OK", answer)
	for _, header := range headers {
		res.AppendHeader(header)
	}
	c.mu.Lock()
	res.AppendHeader(wire.HeaderClone(&c.contact))
	c.mu.Unlock()
	if err := tx.Respond(res); err != nil {
		return
	}
	c.updateRemoteTarget(cl, req.Contact())
	_ = c.acceptSession(cl, res, false)
	c.startSession(cl)
	if len(answer) > 0 {
		cl.mu.Lock()
		cl.localSDP = append([]byte(nil), answer...)
		cl.negotiationRevision++
		cl.mu.Unlock()
		c.emit(Event{Type: "updated", CallID: cl.id, SDP: append([]byte(nil), req.Body()...)})
	}
}
