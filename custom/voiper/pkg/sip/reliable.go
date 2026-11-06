package sip

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"strconv"
	"strings"
	"time"

	wire "github.com/emiago/sipgo/sip"
)

type cachedACK struct {
	invite  inviteKey
	request *wire.Request
	expires time.Time
}

func hasToken(headers []wire.Header, token string) bool {
	for _, header := range headers {
		for _, value := range strings.Split(header.Value(), ",") {
			if strings.EqualFold(strings.TrimSpace(value), token) {
				return true
			}
		}
	}
	return false
}

func buildInviteACK(request *wire.Request, response *wire.Response, body []byte) (*wire.Request, error) {
	contact := response.Contact()
	if contact == nil {
		return nil, errors.New("INVITE answer has no Contact")
	}
	ack := wire.NewRequest(wire.ACK, contact.Address)
	ack.SetTransport(request.Transport())
	ack.AppendHeader(wire.HeaderClone(request.From()))
	ack.AppendHeader(wire.HeaderClone(response.To()))
	ack.AppendHeader(wire.HeaderClone(request.CallID()))
	ack.AppendHeader(&wire.CSeqHeader{SeqNo: request.CSeq().SeqNo, MethodName: wire.ACK})
	routes := response.GetHeaders("Record-Route")
	if len(routes) == 0 {
		routes = request.GetHeaders("Route")
		for _, route := range routes {
			ack.AppendHeader(wire.NewHeader("Route", route.Value()))
		}
	} else {
		for i := len(routes) - 1; i >= 0; i-- {
			ack.AppendHeader(wire.NewHeader("Route", routes[i].Value()))
		}
	}
	if route := ack.Route(); route != nil && !route.Address.UriParams.Has("lr") {
		ack.Recipient = route.Address
		ack.RemoveHeader("Route")
		ack.AppendHeader(&wire.RouteHeader{Address: contact.Address})
	}
	ack.SetBody(body)
	if len(body) > 0 {
		ack.AppendHeader(wire.NewHeader("Content-Type", "application/sdp"))
	}
	return ack, nil
}

func (c *Client) acknowledgeInvite(cl *call, request *wire.Request, response *wire.Response, body []byte) error {
	ack, err := buildInviteACK(request, response, body)
	if err != nil {
		return err
	}
	c.rememberInitialACK(request, response, ack)
	cl.mu.Lock()
	if cl.acks == nil {
		cl.acks = make(map[uint32]cachedACK)
	}
	for seq, old := range cl.acks {
		if time.Now().After(old.expires) {
			delete(cl.acks, seq)
		}
	}
	key, _ := initialInviteKey(request)
	cl.acks[request.CSeq().SeqNo] = cachedACK{invite: key, request: ack.Clone(), expires: time.Now().Add(32 * time.Second)}
	cl.mu.Unlock()
	return c.client.WriteRequest(ack)
}

func (c *Client) onWireMessage(message wire.Message) {
	response, ok := message.(*wire.Response)
	if !ok || !response.IsSuccess() || response.CSeq() == nil || response.CSeq().MethodName != wire.INVITE || response.CallID() == nil || response.To() == nil {
		return
	}
	if c.handleForkResponse(response) {
		return
	}
	cl, err := c.getCall(string(*response.CallID()))
	if err != nil {
		return
	}
	cl.mu.Lock()
	ack, ok := cl.acks[response.CSeq().SeqNo]
	key, valid := initialInviteKey(response)
	if !ok || !valid || key != ack.invite || time.Now().After(ack.expires) {
		cl.mu.Unlock()
		return
	}
	expected, _ := ack.request.To().Params.Get("tag")
	actual, _ := response.To().Params.Get("tag")
	request := ack.request.Clone()
	cl.mu.Unlock()
	if actual == expected {
		_ = c.client.WriteRequest(request)
	}
}

func (c *Client) sendPRACK(ctx context.Context, cl *call, response *wire.Response, prepare func(*wire.Response) ([]byte, error)) error {
	if response.StatusCode <= 100 || response.StatusCode >= 200 || !hasToken(response.GetHeaders("Require"), "100rel") {
		return nil
	}
	header := response.GetHeader("RSeq")
	if header == nil || response.CSeq() == nil || response.Contact() == nil || response.To() == nil {
		return errors.New("reliable provisional response is missing RSeq, CSeq or Contact")
	}
	sequence, err := strconv.ParseUint(strings.TrimSpace(header.Value()), 10, 32)
	if err != nil || sequence == 0 {
		return errors.New("invalid RSeq")
	}
	tag, _ := response.To().Params.Get("tag")
	if tag == "" {
		return errors.New("reliable provisional response has no dialog tag")
	}
	cl.mu.Lock()
	if cl.remoteRSeqInvite != response.CSeq().SeqNo {
		cl.remoteRSeq = make(map[string]uint32)
		cl.remoteRSeqInvite = response.CSeq().SeqNo
	}
	previous := cl.remoteRSeq[tag]
	if previous != 0 && uint32(sequence) != previous+1 {
		cl.mu.Unlock()
		return nil
	}
	if previous == 0 && len(cl.remoteRSeq) >= 16 {
		cl.mu.Unlock()
		return errors.New("too many early dialogs")
	}
	cl.remoteRSeq[tag] = uint32(sequence)
	cl.mu.Unlock()
	var body []byte
	if prepare != nil {
		body, err = prepare(response)
		if err != nil {
			return err
		}
	}
	req := wire.NewRequest(wire.PRACK, response.Contact().Address)
	if len(body) > 0 {
		req.SetBody(body)
		req.AppendHeader(wire.NewHeader("Content-Type", "application/sdp"))
	}
	req.AppendHeader(wire.NewHeader("RAck", fmt.Sprintf("%d %d INVITE", sequence, response.CSeq().SeqNo)))
	res, err := c.doDialog(ctx, cl, req)
	if err != nil {
		return err
	}
	if res == nil || !res.IsSuccess() {
		return errors.New("PRACK was rejected")
	}
	if len(response.Body()) > 0 {
		key, _ := initialInviteKey(response)
		if early := cl.earlyDialogs[earlyDialogID{key.localTag, tag}]; early != nil {
			early.reliable = true
			if len(early.answer) == 0 {
				early.answer = append([]byte(nil), response.Body()...)
			}
		}
	}
	if len(body) > 0 {
		c.rememberEarlyAnswer(response)
	}
	return nil
}

func (c *Client) reliableRinging(cl *call) error {
	cl.mu.Lock()
	cl.localRSeq = uint32(rand.IntN(1<<30) + 1)
	cl.prack = make(chan struct{})
	sequence := cl.localRSeq
	cl.mu.Unlock()
	return cl.incoming.Respond(180, "Ringing", nil, wire.NewHeader("Require", "100rel"), wire.NewHeader("RSeq", strconv.FormatUint(uint64(sequence), 10)))
}

func (c *Client) awaitIncoming(cl *call) {
	interval := 500 * time.Millisecond
	timer := time.NewTimer(interval)
	defer timer.Stop()
	deadline := time.NewTimer(32 * time.Second)
	defer deadline.Stop()
	cl.mu.Lock()
	prack := cl.prack
	cl.mu.Unlock()
	var retransmit <-chan time.Time
	var expires <-chan time.Time
	if prack != nil {
		retransmit = timer.C
		expires = deadline.C
	}
	for {
		select {
		case <-cl.incoming.Context().Done():
			c.finish(cl, "remote ended call")
			return
		case <-cl.ctx.Done():
			return
		case <-prack:
			prack = nil
			retransmit = nil
			expires = nil
		case <-expires:
			ctx, cancel := context.WithTimeout(c.ctx, 5*time.Second)
			_ = c.respondCall(ctx, cl, func() error { return cl.incoming.Respond(504, "PRACK Timeout", nil) })
			cancel()
			c.finish(cl, "reliable provisional response was not acknowledged")
			return
		case <-retransmit:
			cl.mu.Lock()
			ready := cl.ready
			sequence := cl.localRSeq
			cl.mu.Unlock()
			if ready {
				retransmit = nil
				expires = nil
				continue
			}
			if cl.operation.TryLock() {
				err := cl.incoming.Respond(180, "Ringing", nil, wire.NewHeader("Require", "100rel"), wire.NewHeader("RSeq", strconv.FormatUint(uint64(sequence), 10)))
				cl.operation.Unlock()
				if err != nil {
					c.finish(cl, err.Error())
					return
				}
			}
			interval *= 2
			timer.Reset(interval)
		}
	}
}

func (c *Client) onPRACK(req *wire.Request, tx wire.ServerTransaction) {
	cl := c.match(req)
	if cl == nil || cl.incoming == nil || req.CSeq() == nil {
		respond(req, tx, 481, "Dialog Does Not Exist")
		return
	}
	rack := req.GetHeader("RAck")
	if rack == nil {
		respond(req, tx, 400, "Missing RAck")
		return
	}
	fields := strings.Fields(rack.Value())
	if len(fields) != 3 || fields[2] != "INVITE" {
		respond(req, tx, 400, "Invalid RAck")
		return
	}
	sequence, e1 := strconv.ParseUint(fields[0], 10, 32)
	inviteSequence, e2 := strconv.ParseUint(fields[1], 10, 32)
	cl.mu.Lock()
	valid := e1 == nil && e2 == nil && cl.prack != nil && uint32(sequence) == cl.localRSeq && uint32(inviteSequence) == cl.incoming.InviteRequest.CSeq().SeqNo
	cl.mu.Unlock()
	if !valid {
		respond(req, tx, 481, "No Matching Reliable Response")
		return
	}
	if len(req.Body()) > 0 {
		respond(req, tx, 488, "No Offer Expected In PRACK")
		return
	}
	// ReadAck compares against the INVITE sequence; advancing sipgo's remote
	// sequence here would make its initial ACK validator reject a correct ACK.
	respond(req, tx, 200, "OK")
	cl.mu.Lock()
	if cl.prack != nil {
		close(cl.prack)
		cl.prack = nil
	}
	cl.mu.Unlock()
}

func (c *Client) byeDialog(ctx context.Context, cl *call) error {
	cl.mu.Lock()
	remote := cl.remote
	cl.mu.Unlock()
	req := wire.NewRequest(wire.BYE, remote)
	_, err := c.doDialog(ctx, cl, req)
	return err
}
