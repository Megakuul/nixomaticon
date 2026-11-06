package sip

import (
	"context"
	"errors"
	"mime"
	"strings"
	"sync/atomic"
	"unicode/utf8"

	wire "github.com/emiago/sipgo/sip"
)

func respond(req *wire.Request, tx wire.ServerTransaction, code int, reason string) {
	_ = tx.Respond(wire.NewResponseFromRequest(req, code, reason, nil))
}

func (c *Client) installHandlers() {
	c.server.OnInvite(c.onInvite)
	c.server.OnPrack(c.onPRACK)
	c.server.OnUpdate(c.onUpdate)
	c.server.OnCancel(func(req *wire.Request, tx wire.ServerTransaction) { respond(req, tx, 200, "OK") })
	c.server.OnAck(c.onACK)
	c.server.OnBye(func(req *wire.Request, tx wire.ServerTransaction) {
		cl := c.match(req)
		if cl == nil {
			respond(req, tx, 481, "Call Does Not Exist")
			return
		}
		cl.mu.Lock()
		incoming, outgoing, ready := cl.incoming, cl.outgoing, cl.ready
		cl.mu.Unlock()
		if !ready {
			respond(req, tx, 481, "Call Not Established")
			return
		}
		if req.CSeq() == nil {
			respond(req, tx, 400, "Missing CSeq")
			return
		}
		var err error
		if incoming != nil {
			err = incoming.ReadRequest(req, tx)
		} else {
			err = outgoing.ReadRequest(req, tx)
		}
		if err != nil {
			respond(req, tx, 500, "Invalid Sequence")
			return
		}
		if completedElsewhere(req) {
			cl.termination.Store(true)
		}
		if incoming != nil {
			err = incoming.ReadBye(req, tx)
		} else {
			err = outgoing.ReadBye(req, tx)
		}
		if err == nil {
			c.finish(cl, "remote hangup")
		} else {
			respond(req, tx, 500, "Invalid Dialog Request")
		}
	})
	c.server.OnOptions(func(req *wire.Request, tx wire.ServerTransaction) {
		res := wire.NewResponseFromRequest(req, 200, "OK", nil)
		res.AppendHeader(wire.NewHeader("Allow", allowedMethods))
		res.AppendHeader(wire.NewHeader("Accept", "application/sdp, text/plain, application/pidf+xml, application/dialog-info+xml, application/simple-message-summary"))
		_ = tx.Respond(res)
	})
	c.server.OnMessage(func(req *wire.Request, tx wire.ServerTransaction) {
		if req.From() == nil {
			respond(req, tx, 400, "Missing From")
			return
		}
		if len(req.Body()) > 65536 {
			respond(req, tx, 413, "Content Too Large")
			return
		}
		contentType := ""
		if req.ContentType() != nil {
			contentType = req.ContentType().Value()
		}
		mediaType, params, parseErr := mime.ParseMediaType(contentType)
		if parseErr != nil || mediaType != "text/plain" || (params["charset"] != "" && !strings.EqualFold(params["charset"], "utf-8")) {
			if !c.handleExtension(req, tx) {
				respond(req, tx, 415, "Unsupported Media Type")
			}
			return
		}
		if !utf8.Valid(req.Body()) {
			respond(req, tx, 400, "Invalid UTF-8")
			return
		}
		respond(req, tx, 200, "OK")
		c.emit(Event{Type: "message", RemoteURI: req.From().Address.String(), DisplayName: req.From().DisplayName, Body: append([]byte(nil), req.Body()...), ContentType: contentType, Message: string(req.Body())})
	})
	c.server.OnNotify(c.onNotify)
	c.server.OnRefer(c.onRefer)
	c.server.OnInfo(func(req *wire.Request, tx wire.ServerTransaction) {
		if c.match(req) == nil {
			respond(req, tx, 481, "Call Does Not Exist")
			return
		}
		if len(req.Body()) > 1024 {
			respond(req, tx, 413, "Content Too Large")
			return
		}
		if req.ContentType() == nil || req.ContentType().Value() != "application/dtmf-relay" {
			respond(req, tx, 415, "Unsupported Media Type")
			return
		}
		respond(req, tx, 200, "OK")
		c.emit(Event{Type: "dtmf", CallID: string(*req.CallID()), Body: append([]byte(nil), req.Body()...), ContentType: "application/dtmf-relay"})
	})
}

func (c *Client) match(req *wire.Request) *call {
	if req.CallID() == nil || req.From() == nil || req.To() == nil {
		return nil
	}
	cl, err := c.getCall(string(*req.CallID()))
	if err != nil {
		return nil
	}
	cl.mu.Lock()
	defer cl.mu.Unlock()
	if cl.done {
		return nil
	}
	// Match both tags; a Call-ID alone is not a dialog identifier.
	if cl.incoming != nil {
		remote, _ := req.From().Params.Get("tag")
		expectedRemote, _ := cl.incoming.InviteRequest.From().Params.Get("tag")
		local, _ := req.To().Params.Get("tag")
		expectedLocal, _ := cl.incoming.InviteRequest.To().Params.Get("tag")
		if remote != expectedRemote || local != expectedLocal {
			return nil
		}
	} else {
		if !cl.ready {
			return nil
		}
		remote, _ := req.From().Params.Get("tag")
		expectedRemote, _ := cl.outgoing.InviteResponse.To().Params.Get("tag")
		local, _ := req.To().Params.Get("tag")
		expectedLocal, _ := cl.outgoing.InviteRequest.From().Params.Get("tag")
		if remote != expectedRemote || local != expectedLocal {
			return nil
		}
	}
	return cl
}

func (c *Client) onInvite(req *wire.Request, tx wire.ServerTransaction) {
	for _, header := range req.GetHeaders("Require") {
		for _, token := range strings.Split(header.Value(), ",") {
			if !strings.EqualFold(strings.TrimSpace(token), "100rel") && !strings.EqualFold(strings.TrimSpace(token), "timer") && !strings.EqualFold(strings.TrimSpace(token), "replaces") {
				res := wire.NewResponseFromRequest(req, 420, "Bad Extension", nil)
				res.AppendHeader(wire.NewHeader("Unsupported", token))
				_ = tx.Respond(res)
				return
			}
		}
	}
	if req.CallID() == nil || req.From() == nil || req.To() == nil || req.CSeq() == nil {
		respond(req, tx, 400, "Missing Dialog Headers")
		return
	}
	if len(req.Body()) > 65536 || (len(req.Body()) > 0 && (req.ContentType() == nil || req.ContentType().Value() != "application/sdp")) {
		respond(req, tx, 488, "SDP Required")
		return
	}
	timerHeaders, ok := c.sessionResponse(req, tx)
	if !ok {
		return
	}
	if tag, _ := req.To().Params.Get("tag"); tag != "" {
		c.onReinvite(req, tx, timerHeaders)
		return
	}
	replacedID, failure := c.replacedCall(req)
	if failure != 0 {
		respond(req, tx, failure, "Replaces Dialog Unavailable")
		return
	}
	termination := new(atomic.Bool)
	original := req.Clone()
	tx.OnCancel(func(cancel *wire.Request) {
		if sameCanceledInvite(original, cancel) && completedElsewhere(cancel) {
			termination.Store(true)
		}
	})
	dlg, err := c.dialogUA().ReadInvite(req, tx)
	if err != nil {
		respond(req, tx, 400, "Invalid Invite")
		return
	}
	ctx, cancel := context.WithCancel(c.ctx)
	cl := &call{termination: termination, authOrigin: dlg.InviteRequest.Contact().Address, allowAuth: true, operation: make(operationLock, 1), id: string(*req.CallID()), incoming: dlg, transaction: tx, answerHeaders: timerHeaders, delayedOffer: len(req.Body()) == 0, session: sessionTimer{update: hasToken(req.GetHeaders("Allow"), "UPDATE")}, remote: dlg.InviteRequest.Contact().Address, ctx: ctx, cancel: cancel}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		cancel()
		respond(req, tx, 480, "Unavailable")
		return
	}
	if _, exists := c.calls[cl.id]; exists {
		c.mu.Unlock()
		cancel()
		respond(req, tx, 482, "Call Already Exists")
		return
	}
	c.calls[cl.id] = cl
	c.workers.Add(1)
	c.mu.Unlock()
	defer c.workers.Done()
	defer dlg.Close()
	var ringErr error
	if cl.delayedOffer && hasToken(req.GetHeaders("Require"), "100rel") {
		ringErr = dlg.Respond(100, "Trying", nil)
	} else if hasToken(req.GetHeaders("Require"), "100rel") {
		ringErr = c.reliableRinging(cl)
	} else {
		ringErr = dlg.Respond(180, "Ringing", nil)
	}
	if ringErr != nil {
		c.finish(cl, ringErr.Error())
		return
	}
	c.emit(Event{Type: "incoming", CallID: cl.id, DelayedOffer: cl.delayedOffer, ReplacesCallID: replacedID, RemoteURI: req.From().Address.String(), DisplayName: req.From().DisplayName, State: "ringing", SDP: append([]byte(nil), req.Body()...)})
	c.awaitIncoming(cl)
}

// SetOfferHandler negotiates remote offers. A nil offer requests a fresh local
// offer for a bodyless re-INVITE, without changing the active media session.
// The handler must be fast and may be called concurrently for different calls.
func (c *Client) SetOfferHandler(handler func(string, []byte) ([]byte, error)) {
	c.mu.Lock()
	c.offerHandler = handler
	c.mu.Unlock()
}

func (c *Client) SendMessage(ctx context.Context, target, body string) error {
	if len(body) == 0 || len(body) > 65536 || !utf8.ValidString(body) {
		return errors.New("message must contain 1–65536 bytes")
	}
	uri, err := c.recipient(target)
	if err != nil {
		return err
	}
	req := c.request(wire.MESSAGE, uri, []byte(body))
	req.AppendHeader(wire.NewHeader("Content-Type", "text/plain; charset=utf-8"))
	_, err = c.do(ctx, req)
	return err
}
