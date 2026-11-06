package sip

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	wire "github.com/emiago/sipgo/sip"
)

func parseReplaces(value string) (id, localTag, remoteTag string, earlyOnly bool, err error) {
	parts := strings.Split(value, ";")
	id = strings.TrimSpace(parts[0])
	if id == "" || len(value) > 2048 || strings.ContainsAny(value, "\r\n\t ") {
		err = errors.New("invalid Replaces")
		return
	}
	for _, part := range parts[1:] {
		name, value, hasValue := strings.Cut(part, "=")
		switch strings.ToLower(name) {
		case "to-tag":
			if !hasValue || value == "" || localTag != "" {
				err = errors.New("invalid Replaces to-tag")
				return
			}
			localTag = value
		case "from-tag":
			if !hasValue || value == "" || remoteTag != "" {
				err = errors.New("invalid Replaces from-tag")
				return
			}
			remoteTag = value
		case "early-only":
			if hasValue || earlyOnly {
				err = errors.New("invalid Replaces early-only")
				return
			}
			earlyOnly = true
		}
	}
	if localTag == "" || remoteTag == "" {
		err = errors.New("missing required Replaces dialog tags")
	}
	return
}

func (c *Client) replacedCall(req *wire.Request) (string, int) {
	headers := req.GetHeaders("Replaces")
	if len(headers) == 0 {
		return "", 0
	}
	if len(headers) != 1 {
		return "", 400
	}
	id, local, remote, earlyOnly, err := parseReplaces(headers[0].Value())
	if err != nil {
		return "", 400
	}
	cl, err := c.getCall(id)
	if err != nil {
		return "", 481
	}
	cl.mu.Lock()
	defer cl.mu.Unlock()
	if cl.done || !cl.ready {
		return "", 481
	}
	if earlyOnly {
		return "", 486
	}
	var expectedLocal, expectedRemote string
	if cl.incoming != nil {
		expectedLocal, _ = cl.incoming.InviteRequest.To().Params.Get("tag")
		expectedRemote, _ = cl.incoming.InviteRequest.From().Params.Get("tag")
	} else {
		expectedLocal, _ = cl.outgoing.InviteRequest.From().Params.Get("tag")
		expectedRemote, _ = cl.outgoing.InviteResponse.To().Params.Get("tag")
	}
	if local != expectedLocal || remote != expectedRemote {
		return "", 481
	}
	return id, 0
}

// inviteTarget promotes only the Replaces URI header used for attended transfer.
// Other URI-supplied headers must never override account or dialog identity.
func (c *Client) inviteTarget(target string) (wire.Uri, string, error) {
	uri, err := c.recipient(target)
	if err != nil {
		return uri, "", err
	}
	replaces := ""
	for _, header := range uri.Headers {
		if !strings.EqualFold(header.K, "Replaces") || replaces != "" {
			return uri, "", errors.New("unsupported SIP URI header")
		}
		replaces, err = url.QueryUnescape(header.V)
		if err != nil {
			return uri, "", err
		}
		if _, _, _, _, err = parseReplaces(replaces); err != nil {
			return uri, "", err
		}
	}
	uri.Headers = nil
	return uri, replaces, nil
}

// SetTransferHandler authorizes inbound REFER execution for established calls.
// Return only when the replacement call connects or fails. Cancellation must
// stop a pending replacement. The original call remains until transfer-complete.
func (c *Client) SetTransferHandler(handler func(context.Context, string, string) error) {
	c.mu.Lock()
	c.transferHandler = handler
	c.mu.Unlock()
}

func (c *Client) onRefer(req *wire.Request, tx wire.ServerTransaction) {
	cl := c.match(req)
	if cl == nil {
		respond(req, tx, 481, "Dialog Does Not Exist")
		return
	}
	c.mu.Lock()
	handler := c.transferHandler
	c.mu.Unlock()
	if handler == nil {
		if !c.handleExtension(req, tx) {
			respond(req, tx, 501, "Transfer Reception Unavailable")
		}
		return
	}
	headers := req.GetHeaders("Refer-To")
	if len(headers) != 1 || len(headers[0].Value()) > 2048 {
		respond(req, tx, 400, "Invalid Refer-To")
		return
	}
	var uri wire.Uri
	params := wire.NewParams()
	if _, err := wire.ParseAddressValue(headers[0].Value(), &uri, &params); err != nil {
		respond(req, tx, 400, "Invalid Refer-To")
		return
	}
	if _, _, err := c.inviteTarget(uri.String()); err != nil {
		respond(req, tx, 400, "Invalid Refer-To")
		return
	}
	if method, ok := uri.UriParams.Get("method"); ok && !strings.EqualFold(method, "INVITE") {
		respond(req, tx, 501, "Unsupported Refer Method")
		return
	}
	if req.CSeq() == nil {
		respond(req, tx, 400, "Missing CSeq")
		return
	}
	cl.mu.Lock()
	if !cl.ready || cl.done || cl.transferring {
		cl.mu.Unlock()
		respond(req, tx, 491, "Transfer Pending")
		return
	}
	cl.transferring = true
	cl.mu.Unlock()
	var err error
	if cl.incoming != nil {
		err = cl.incoming.ReadRequest(req, tx)
	} else {
		err = cl.outgoing.ReadRequest(req, tx)
	}
	if err != nil {
		cl.mu.Lock()
		cl.transferring = false
		cl.mu.Unlock()
		respond(req, tx, 500, "Invalid Sequence")
		return
	}
	noSubscription := false
	if header := req.GetHeader("Refer-Sub"); header != nil {
		if !strings.EqualFold(header.Value(), "false") && !strings.EqualFold(header.Value(), "true") {
			cl.mu.Lock()
			cl.transferring = false
			cl.mu.Unlock()
			respond(req, tx, 400, "Invalid Refer-Sub")
			return
		}
		noSubscription = strings.EqualFold(header.Value(), "false")
	}
	response := wire.NewResponseFromRequest(req, 202, "Accepted", nil)
	if noSubscription {
		response.StatusCode = 200
		response.Reason = "OK"
		response.AppendHeader(wire.NewHeader("Refer-Sub", "false"))
	}
	if err := tx.Respond(response); err != nil {
		cl.mu.Lock()
		cl.transferring = false
		cl.mu.Unlock()
		return
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.workers.Add(1)
	c.mu.Unlock()
	go func() {
		defer c.workers.Done()
		defer func() { cl.mu.Lock(); cl.transferring = false; cl.mu.Unlock() }()
		ctx, cancel := context.WithTimeout(cl.ctx, 2*time.Minute)
		defer cancel()
		notify := func(code int, reason, state string) error {
			if noSubscription {
				return nil
			}
			_, err := c.inDialog(ctx, cl.id, wire.NOTIFY, []byte(fmt.Sprintf("SIP/2.0 %d %s\r\n", code, reason)), wire.NewHeader("Event", fmt.Sprintf("refer;id=%d", req.CSeq().SeqNo)), wire.NewHeader("Subscription-State", state), wire.NewHeader("Content-Type", "message/sipfrag;version=2.0"))
			return err
		}
		err := notify(100, "Trying", "active;expires=120")
		if err == nil {
			err = handler(ctx, cl.id, uri.String())
		}
		code, reason, state := 200, "OK", "success"
		message := ""
		if err != nil {
			code, reason, state = 503, "Service Unavailable", "failed"
			message = err.Error()
			var response *ResponseError
			if errors.As(err, &response) && response.Status >= 300 && response.Status <= 699 {
				code = response.Status
				reason = response.Reason
			}
			if errors.Is(err, context.DeadlineExceeded) {
				code, reason = 408, "Request Timeout"
			}
		}
		if notifyErr := notify(code, reason, "terminated;reason=noresource"); notifyErr != nil && message == "" {
			message = notifyErr.Error()
		}
		c.emit(Event{Type: "transfer-complete", CallID: cl.id, State: state, Message: message})
	}()
}
