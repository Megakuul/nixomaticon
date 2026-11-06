package sip

import (
	"bytes"
	"mime"

	wire "github.com/emiago/sipgo/sip"
)

type earlyDialogID struct{ local, remote string }

type earlyDialog struct {
	invite   inviteKey
	response *wire.Response
	request  *wire.Request
	reliable bool
	offer    []byte
	answer   []byte
	localSDP []byte
}

func (c *Client) rememberEarlyDialog(cl *call, response *wire.Response) {
	key, valid := initialInviteKey(response)
	if !valid || response.To() == nil || response.Contact() == nil {
		return
	}
	tag, _ := response.To().Params.Get("tag")
	if tag == "" {
		return
	}
	id := earlyDialogID{key.localTag, tag}
	cl.mu.Lock()
	defer cl.mu.Unlock()
	if cl.earlyDialogs == nil {
		cl.earlyDialogs = make(map[earlyDialogID]*earlyDialog)
	}
	if old := cl.earlyDialogs[id]; old != nil && old.invite == key {
		old.response = response.Clone()
	} else if len(cl.earlyDialogs) < 16 {
		cl.earlyDialogs[id] = &earlyDialog{invite: key, response: response.Clone()}
	}
}

// Incoming sipgo ACK validation still expects the initial INVITE CSeq. Keep
// early remote requests separately until that ACK has confirmed the dialog.
func (c *Client) restoreEarlySequence(cl *call, response *wire.Response) {
	cl.mu.Lock()
	request := cl.earlyRequest
	if response != nil && response.From() != nil && response.To() != nil {
		local, _ := response.From().Params.Get("tag")
		remote, _ := response.To().Params.Get("tag")
		if early := cl.earlyDialogs[earlyDialogID{local, remote}]; early != nil {
			request = early.request
		}
	}
	cl.mu.Unlock()
	if request == nil {
		return
	}
	if cl.incoming != nil {
		_ = cl.incoming.ReadRequest(request, nil)
	} else {
		_ = cl.outgoing.ReadRequest(request, nil)
	}
}

func (c *Client) onEarlyUpdate(req *wire.Request, tx wire.ServerTransaction) bool {
	if req.CallID() == nil || req.From() == nil || req.To() == nil {
		return false
	}
	cl, err := c.getCall(string(*req.CallID()))
	if err != nil {
		return false
	}
	cl.mu.Lock()
	ready := cl.ready
	cl.mu.Unlock()
	if ready {
		return false
	}
	if !cl.operation.TryLock() {
		respond(req, tx, 491, "Request Pending")
		return true
	}
	defer cl.operation.Unlock()
	cl.mu.Lock()
	if cl.ready {
		cl.mu.Unlock()
		return false
	}
	local, _ := req.To().Params.Get("tag")
	remote, _ := req.From().Params.Get("tag")
	early := cl.earlyDialogs[earlyDialogID{local, remote}]
	valid := early != nil
	previous := uint32(0)
	if cl.incoming != nil {
		invite := cl.incoming.InviteRequest
		expectedLocal, _ := invite.To().Params.Get("tag")
		expectedRemote, _ := invite.From().Params.Get("tag")
		valid = local == expectedLocal && remote == expectedRemote
		previous = invite.CSeq().SeqNo
		if cl.earlyRequest != nil {
			previous = cl.earlyRequest.CSeq().SeqNo
		}
	} else if early != nil && early.request != nil {
		previous = early.request.CSeq().SeqNo
	}
	if !valid || cl.done {
		cl.mu.Unlock()
		respond(req, tx, 481, "Dialog Does Not Exist")
		return true
	}
	if req.CSeq() == nil || req.CSeq().SeqNo <= previous {
		cl.mu.Unlock()
		respond(req, tx, 500, "Invalid Sequence")
		return true
	}
	if cl.incoming != nil {
		cl.earlyRequest = req.Clone()
	} else {
		early.request = req.Clone()
	}
	cl.mu.Unlock()
	headers, ok := c.sessionResponse(req, tx)
	if !ok {
		return true
	}
	var candidate *delayedBranch
	var key delayedBranchKey
	var old *delayedBranch
	var earlyAnswer []byte
	if len(req.Body()) > 0 {
		mediaType := ""
		if req.ContentType() != nil {
			mediaType, _, _ = mime.ParseMediaType(req.ContentType().Value())
		}
		if len(req.Body()) > 65536 || mediaType != "application/sdp" {
			respond(req, tx, 415, "Unsupported Media Type")
			return true
		}
		if early == nil || !early.reliable {
			respond(req, tx, 491, "Offer Negotiation Pending")
			return true
		}
		if cl.delayed == nil {
			c.mu.Lock()
			handler := c.earlyOfferHandler
			c.mu.Unlock()
			if handler == nil || cl.earlyMedia != (earlyDialogID{local, remote}) {
				respond(req, tx, 488, "Early Media Negotiation Unavailable")
				return true
			}
			earlyAnswer, err = handler(cl.id, append([]byte(nil), req.Body()...))
			if err != nil || len(earlyAnswer) == 0 || len(earlyAnswer) > 65536 || cl.ctx.Err() != nil {
				respond(req, tx, 488, "Not Acceptable Here")
				return true
			}
		} else {
			key = delayedBranchKey{early.invite, remote}
			old = cl.delayed.branches[key]
			if old == nil || !old.reliable || old.err != nil {
				respond(req, tx, 491, "Offer Negotiation Pending")
				return true
			}
			if bytes.Equal(bytes.TrimSpace(old.offer), bytes.TrimSpace(req.Body())) {
				candidate = old
			} else {
				response := early.response.Clone()
				response.SetBody(append([]byte(nil), req.Body()...))
				response.ReplaceHeader(wire.NewHeader("Content-Type", "application/sdp"))
				candidate = cl.delayed.prepareBranch(response)
				if candidate.err != nil {
					respond(req, tx, 488, "Not Acceptable Here")
					return true
				}
				candidate.initialOffer = old.initialOffer
				candidate.reliable = true
			}
		}
		headers = append(headers, wire.NewHeader("Content-Type", "application/sdp"))
	}
	body := earlyAnswer
	if candidate != nil {
		body = candidate.answer.SDP
	}
	res := wire.NewResponseFromRequest(req, 200, "OK", body)
	for _, header := range headers {
		res.AppendHeader(header)
	}
	c.mu.Lock()
	res.AppendHeader(wire.HeaderClone(&c.contact))
	c.mu.Unlock()
	if err := tx.Respond(res); err != nil {
		if len(earlyAnswer) > 0 {
			c.finish(cl, "early UPDATE response failed")
		}
		if candidate != nil && candidate != old {
			candidate.cancel()
		}
		return true
	}
	if cl.incoming != nil {
		c.updateRemoteTarget(cl, req.Contact())
	}
	if len(earlyAnswer) > 0 {
		early.offer = append([]byte(nil), req.Body()...)
		early.localSDP = append([]byte(nil), earlyAnswer...)
	}
	if candidate != nil && candidate != old {
		cl.delayed.branches[key] = candidate
		old.cancel()
	}
	return true
}

// SetEarlyOfferHandler negotiates UPDATE only for the early dialog currently
// providing playback. It must preserve playback-only media until connected.
func (c *Client) SetEarlyOfferHandler(handler func(string, []byte) ([]byte, error)) {
	c.mu.Lock()
	c.earlyOfferHandler = handler
	c.mu.Unlock()
}

func (c *Client) earlyMediaSDP(cl *call, response *wire.Response) []byte {
	if len(response.Body()) == 0 || response.From() == nil || response.To() == nil {
		return nil
	}
	local, _ := response.From().Params.Get("tag")
	remote, _ := response.To().Params.Get("tag")
	id := earlyDialogID{local, remote}
	if cl.earlyMedia == (earlyDialogID{}) {
		cl.earlyMedia = id
	}
	if cl.earlyMedia != id {
		return nil
	}
	return append([]byte(nil), response.Body()...)
}

func (c *Client) finalEarlySDP(cl *call, response *wire.Response, initialOffer []byte) (remoteSDP, originalOffer, localSDP []byte) {
	if response.From() != nil && response.To() != nil {
		local, _ := response.From().Params.Get("tag")
		remote, _ := response.To().Params.Get("tag")
		if early := cl.earlyDialogs[earlyDialogID{local, remote}]; early != nil {
			if len(early.offer) > 0 {
				return append([]byte(nil), early.offer...), nil, append([]byte(nil), early.localSDP...)
			}
			if len(response.Body()) == 0 && early.reliable {
				return append([]byte(nil), early.answer...), append([]byte(nil), initialOffer...), nil
			}
		}
	}
	return append([]byte(nil), response.Body()...), append([]byte(nil), initialOffer...), nil
}
