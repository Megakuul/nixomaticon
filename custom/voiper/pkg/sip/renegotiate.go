package sip

import (
	"context"
	"errors"
	"mime"
	"time"

	wire "github.com/emiago/sipgo/sip"
)

type inviteAcknowledgment struct {
	body        []byte
	contentType string
}

type inviteExchange struct {
	sequence     uint32
	acknowledged bool
	ack          chan inviteAcknowledgment
}

// SetAnswerHandler validates an answer without changing media or blocking on
// devices/network. Validated ACK answers pass through the optional synchronous
// commit handler before delivery in updated events.
func (c *Client) SetAnswerHandler(handler func(string, []byte) error) {
	c.mu.Lock()
	c.answerHandler = handler
	c.mu.Unlock()
}

// SetAnswerCommitHandler applies a negotiated answer while the call operation
// remains locked. It must return promptly and must not call SIP methods inline.
func (c *Client) SetAnswerCommitHandler(handler func(string, []byte, []byte) error) {
	c.mu.Lock()
	c.answerCommitHandler = handler
	c.mu.Unlock()
}

func (c *Client) commitAnswer(id string, offer, answer []byte) error {
	c.mu.Lock()
	handler := c.answerCommitHandler
	c.mu.Unlock()
	if handler == nil {
		return nil
	}
	return handler(id, append([]byte(nil), offer...), append([]byte(nil), answer...))
}

func (c *Client) onACK(req *wire.Request, tx wire.ServerTransaction) {
	cl := c.match(req)
	if cl == nil || req.CSeq() == nil {
		return
	}
	cl.mu.Lock()
	pending := cl.pendingInvite
	if pending != nil && req.CSeq().SeqNo == pending.sequence {
		if !pending.acknowledged {
			pending.acknowledged = true
			ack := inviteAcknowledgment{}
			if len(req.Body()) <= 65536 {
				ack.body = append([]byte(nil), req.Body()...)
			}
			if req.ContentType() != nil {
				ack.contentType = req.ContentType().Value()
			}
			pending.ack <- ack
		}
		cl.mu.Unlock()
		return
	}
	incoming := cl.incoming
	if incoming != nil && cl.delayedOffer && req.CSeq().SeqNo == incoming.InviteRequest.CSeq().SeqNo && len(req.Body()) > 0 && len(req.Body()) <= 65536 && req.ContentType() != nil && req.ContentType().Value() == "application/sdp" {
		cl.ackSDP = append([]byte(nil), req.Body()...)
	}
	cl.mu.Unlock()
	if incoming != nil {
		_ = incoming.ReadAck(req, tx)
	}
}

func (c *Client) onReinvite(req *wire.Request, tx wire.ServerTransaction, headers []wire.Header) {
	cl := c.match(req)
	if cl == nil {
		respond(req, tx, 481, "Call Does Not Exist")
		return
	}
	cl.mu.Lock()
	ready := cl.ready
	cl.mu.Unlock()
	if !ready || !cl.operation.TryLock() {
		respond(req, tx, 491, "Request Pending")
		return
	}
	defer cl.operation.Unlock()
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
	c.mu.Lock()
	offerHandler, answerHandler := c.offerHandler, c.answerHandler
	c.mu.Unlock()
	delayed := len(req.Body()) == 0
	if offerHandler == nil || (delayed && answerHandler == nil) {
		respond(req, tx, 488, "Renegotiation Unavailable")
		return
	}
	localSDP, err := offerHandler(cl.id, append([]byte(nil), req.Body()...))
	if err != nil || len(localSDP) == 0 || len(localSDP) > 65536 {
		respond(req, tx, 488, "Not Acceptable Here")
		return
	}
	response := wire.NewResponseFromRequest(req, 200, "OK", localSDP)
	response.AppendHeader(wire.NewHeader("Content-Type", "application/sdp"))
	for _, header := range headers {
		response.AppendHeader(header)
	}
	c.mu.Lock()
	response.AppendHeader(wire.HeaderClone(&c.contact))
	c.mu.Unlock()
	pending := &inviteExchange{sequence: req.CSeq().SeqNo, ack: make(chan inviteAcknowledgment, 1)}
	cl.mu.Lock()
	cl.pendingInvite = pending
	if !delayed {
		cl.localSDP = append([]byte(nil), localSDP...)
		cl.negotiationRevision++
	}
	cl.mu.Unlock()
	defer func() {
		cl.mu.Lock()
		if cl.pendingInvite == pending {
			cl.pendingInvite = nil
		}
		cl.mu.Unlock()
	}()
	if err := tx.Respond(response); err != nil {
		return
	}
	c.updateRemoteTarget(cl, req.Contact())
	_ = c.acceptSession(cl, response, false)
	c.startSession(cl)
	if !delayed {
		c.emit(Event{Type: "updated", CallID: cl.id, SDP: append([]byte(nil), req.Body()...)})
	}
	ack, err := waitInviteAcknowledgment(cl.ctx, tx, response, pending.ack)
	if err != nil {
		if errors.Is(err, errInviteACKTimeout) {
			cleanupCtx, cancel := context.WithTimeout(c.ctx, 5*time.Second)
			_ = c.byeDialog(cleanupCtx, cl)
			cancel()
			c.finish(cl, "renegotiation ACK timed out")
		}
		return
	}
	if !delayed {
		return
	}
	mediaType, _, parseErr := mime.ParseMediaType(ack.contentType)
	if len(ack.body) == 0 || parseErr != nil || mediaType != "application/sdp" {
		err = errors.New("ACK did not contain an SDP answer")
	} else {
		err = answerHandler(cl.id, append([]byte(nil), ack.body...))
	}
	if err == nil {
		err = c.commitAnswer(cl.id, localSDP, ack.body)
	}
	if err != nil {
		c.emit(Event{Type: "renegotiation-failed", CallID: cl.id, State: "failed", Message: err.Error()})
		return
	}
	cl.mu.Lock()
	if cl.done {
		cl.mu.Unlock()
		return
	}
	cl.localSDP = append([]byte(nil), localSDP...)
	cl.negotiationRevision++
	cl.mu.Unlock()
	c.emit(Event{Type: "updated", CallID: cl.id, SDP: append([]byte(nil), ack.body...)})
}

var errInviteACKTimeout = errors.New("INVITE acknowledgment timed out")

func waitInviteAcknowledgment(ctx context.Context, tx wire.ServerTransaction, response *wire.Response, ack <-chan inviteAcknowledgment) (inviteAcknowledgment, error) {
	interval := 500 * time.Millisecond
	retry := time.NewTimer(interval)
	defer retry.Stop()
	deadline := time.NewTimer(32 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case answer := <-ack:
			return answer, nil
		case <-ctx.Done():
			return inviteAcknowledgment{}, ctx.Err()
		case <-tx.Done():
			if err := ctx.Err(); err != nil {
				return inviteAcknowledgment{}, err
			}
			return inviteAcknowledgment{}, errInviteACKTimeout
		case <-deadline.C:
			return inviteAcknowledgment{}, errInviteACKTimeout
		case <-retry.C:
			if err := tx.Respond(response); err != nil {
				return inviteAcknowledgment{}, err
			}
			interval = min(interval*2, 4*time.Second)
			retry.Reset(interval)
		}
	}
}
