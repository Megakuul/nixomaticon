package sip

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	wire "github.com/emiago/sipgo/sip"
)

type forkPeer struct {
	client  *Client
	invites chan *wire.Request
	acks    chan *wire.Request
	byes    chan *wire.Request
	infos   chan *wire.Request
	pracks  chan *wire.Request
}

func newForkPeer(t *testing.T, challengeBye bool) *forkPeer {
	t.Helper()
	peer := &forkPeer{invites: make(chan *wire.Request, 8), acks: make(chan *wire.Request, 32), byes: make(chan *wire.Request, 32), infos: make(chan *wire.Request, 8), pracks: make(chan *wire.Request, 8)}
	client, err := newClient(Config{Server: "127.0.0.1", Username: "bob", LocalAddress: "127.0.0.1:0"}, nil, func(c *Client) {
		c.server.OnInvite(func(req *wire.Request, tx wire.ServerTransaction) {
			peer.invites <- req.Clone()
			respond(req, tx, 180, "Ringing")
			<-tx.Done()
		})
		c.server.OnAck(func(req *wire.Request, _ wire.ServerTransaction) { peer.acks <- req.Clone() })
		c.server.OnBye(func(req *wire.Request, tx wire.ServerTransaction) {
			peer.byes <- req.Clone()
			if challengeBye {
				response := wire.NewResponseFromRequest(req, 401, "Unauthorized", nil)
				response.AppendHeader(wire.NewHeader("WWW-Authenticate", `Digest realm="office", nonce="fork", algorithm=SHA-256, qop="auth"`))
				_ = tx.Respond(response)
				return
			}
			respond(req, tx, 200, "OK")
		})
		c.server.OnPrack(func(req *wire.Request, tx wire.ServerTransaction) {
			peer.pracks <- req.Clone()
			respond(req, tx, 200, "OK")
		})
		c.server.OnInfo(func(req *wire.Request, tx wire.ServerTransaction) {
			peer.infos <- req.Clone()
			respond(req, tx, 200, "OK")
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	peer.client = client
	t.Cleanup(func() { client.Close() })
	return peer
}

func nextForkRequest(t *testing.T, requests <-chan *wire.Request) *wire.Request {
	t.Helper()
	select {
	case request := <-requests:
		return request
	case <-time.After(3 * time.Second):
		t.Fatal("expected fork dialog request did not arrive")
		return nil
	}
}

func (p *forkPeer) success(t *testing.T, invite *wire.Request, tag string, routed bool) *wire.Response {
	t.Helper()
	response := wire.NewResponseFromRequest(invite, 200, "OK", []byte(offerSDP))
	response.To().Params.Add("tag", tag)
	contact := wire.HeaderClone(&p.client.contact).(*wire.ContactHeader)
	if routed {
		contact.Address.Host = "fork-contact.invalid"
		response.AppendHeader(wire.NewHeader("Record-Route", "<sip:"+p.client.LocalAddr()+";lr>"))
	}
	response.AppendHeader(contact)
	response.AppendHeader(wire.NewHeader("Content-Type", "application/sdp"))
	response.SetDestination(invite.Source())
	p.sendResponse(t, response)
	return response
}

func (p *forkPeer) sendResponse(t *testing.T, response *wire.Response) {
	t.Helper()
	destination, err := net.ResolveUDPAddr("udp", response.Destination())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.client.listener.(net.PacketConn).WriteTo([]byte(response.String()), destination); err != nil {
		t.Fatal(err)
	}
}

func TestForkedSuccessAcknowledgesAndEndsOnlyExtraDialog(t *testing.T) {
	for _, simultaneous := range []bool{false, true} {
		t.Run(fmt.Sprint(simultaneous), func(t *testing.T) {
			first, second := newForkPeer(t, false), newForkPeer(t, false)
			caller, events := newTestClient(t, "alice")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			id, err := caller.Dial(ctx, first.client.contact.Address.String(), []byte(offerSDP))
			if err != nil {
				t.Fatal(err)
			}
			invite := nextForkRequest(t, first.invites)
			firstResponse := first.success(t, invite, "first", false)
			if !simultaneous {
				nextEvent(t, events, "connected")
			}
			secondResponse := second.success(t, invite, "second", true)
			if simultaneous {
				nextEvent(t, events, "connected")
			}
			cl, _ := caller.getCall(id)
			cl.mu.Lock()
			selectedTag, _ := cl.outgoing.InviteResponse.To().Params.Get("tag")
			cl.mu.Unlock()
			selected, extra, extraResponse := first, second, secondResponse
			if selectedTag == "second" {
				selected, extra, extraResponse = second, first, firstResponse
			}
			for _, peer := range []*forkPeer{first, second} {
				ack := nextForkRequest(t, peer.acks)
				if ack.CSeq().SeqNo != invite.CSeq().SeqNo {
					t.Fatal("fork ACK has wrong INVITE sequence")
				}
				if peer == second && (ack.Route() == nil || ack.Recipient.Host != "fork-contact.invalid") {
					t.Fatal("fork ACK did not follow its own Contact/Record-Route")
				}
			}
			bye := nextForkRequest(t, extra.byes)
			if tag, _ := bye.To().Params.Get("tag"); tag == selectedTag {
				t.Fatal("fork cleanup ended selected call")
			}
			extra.sendResponse(t, extraResponse)
			if ack := nextForkRequest(t, extra.acks); ack.To().Params.ToString(';') != bye.To().Params.ToString(';') {
				t.Fatal("retransmitted fork response received another dialog's ACK")
			}
			if err := caller.SendDTMF(ctx, id, "5"); err != nil {
				t.Fatal(err)
			}
			nextForkRequest(t, selected.infos)
			if err := caller.Hangup(ctx, id); err != nil {
				t.Fatal(err)
			}
			nextForkRequest(t, selected.byes)
			nextEvent(t, events, "ended")
			if len(extra.byes) != 0 {
				t.Fatal("retransmitted successful response repeated BYE")
			}
		})
	}
}

func TestLateSuccessAfterCancellationIsAcknowledgedAndEnded(t *testing.T) {
	peer := newForkPeer(t, false)
	caller, events := newTestClient(t, "alice")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	id, err := caller.Dial(ctx, peer.client.contact.Address.String(), []byte(offerSDP))
	if err != nil {
		t.Fatal(err)
	}
	invite := nextForkRequest(t, peer.invites)
	nextEvent(t, events, "ringing")
	if err := caller.Hangup(ctx, id); err != nil {
		t.Fatal(err)
	}
	nextEvent(t, events, "ended")
	if _, err := caller.getCall(id); err == nil {
		t.Fatal("canceled call is still active")
	}
	response := peer.success(t, invite, "late-answer", false)
	nextForkRequest(t, peer.acks)
	nextForkRequest(t, peer.byes)
	peer.sendResponse(t, response)
	nextForkRequest(t, peer.acks)
	if len(peer.byes) != 0 {
		t.Fatal("late response retransmission repeated BYE")
	}
}

func TestForkCleanupDoesNotAuthenticateForeignContact(t *testing.T) {
	trusted, foreign := newForkPeer(t, false), newForkPeer(t, true)
	events := make(chan Event, 32)
	caller, err := NewClient(Config{Server: "127.0.0.1", Port: trusted.client.contact.Address.Port, Username: "alice", Password: "secret", LocalAddress: "127.0.0.1:0"}, func(event Event) { events <- event })
	if err != nil {
		t.Fatal(err)
	}
	defer caller.Close()
	id, err := caller.Dial(context.Background(), trusted.client.contact.Address.String(), []byte(offerSDP))
	if err != nil {
		t.Fatal(err)
	}
	invite := nextForkRequest(t, trusted.invites)
	trusted.success(t, invite, "trusted", false)
	nextEvent(t, events, "connected")
	foreign.success(t, invite, "foreign", false)
	nextForkRequest(t, foreign.acks)
	if bye := nextForkRequest(t, foreign.byes); bye.GetHeader("Authorization") != nil {
		t.Fatal("inherited credentials on foreign fork cleanup")
	}
	select {
	case <-foreign.byes:
		t.Fatal("foreign fork challenge obtained credentials")
	case <-time.After(100 * time.Millisecond):
	}
	caller.Hangup(context.Background(), id)
}

func TestForkResponseRequiresOriginalTransactionIdentity(t *testing.T) {
	peer, extra := newForkPeer(t, false), newForkPeer(t, false)
	caller, events := newTestClient(t, "alice")
	id, err := caller.Dial(context.Background(), peer.client.contact.Address.String(), []byte(offerSDP))
	if err != nil {
		t.Fatal(err)
	}
	invite := nextForkRequest(t, peer.invites)
	response := peer.success(t, invite, "chosen", false)
	nextEvent(t, events, "connected")
	nextForkRequest(t, peer.acks)
	for _, change := range []func(*wire.Response){
		func(r *wire.Response) { r.CSeq().SeqNo++ },
		func(r *wire.Response) { r.From().Params.Add("tag", "wrong-local-tag") },
		func(r *wire.Response) { r.Via().Params.Add("branch", "z9hG4bK.wrong") },
	} {
		invalid := response.Clone()
		invalid.To().Params.Add("tag", "unmatched")
		invalid.ReplaceHeader(wire.HeaderClone(&extra.client.contact))
		change(invalid)
		extra.sendResponse(t, invalid)
	}
	select {
	case <-extra.acks:
		t.Fatal("unrelated response acknowledged as a fork")
	case <-time.After(100 * time.Millisecond):
	}
	extra.success(t, invite, "matched", false)
	nextForkRequest(t, extra.acks)
	nextForkRequest(t, extra.byes)
	caller.Hangup(context.Background(), id)
}

func TestForkStateIsBoundedAndClearedOnClose(t *testing.T) {
	peer, extra := newForkPeer(t, false), newForkPeer(t, false)
	caller, events := newTestClient(t, "alice")
	if _, err := caller.Dial(context.Background(), peer.client.contact.Address.String(), []byte(offerSDP)); err != nil {
		t.Fatal(err)
	}
	invite := nextForkRequest(t, peer.invites)
	peer.success(t, invite, "chosen", false)
	nextEvent(t, events, "connected")
	for i := range 18 {
		extra.success(t, invite, fmt.Sprintf("fork-%d", i), false)
	}
	for range 15 {
		nextForkRequest(t, extra.byes)
	}
	caller.inviteMu.Lock()
	for _, snapshot := range caller.invites {
		if len(snapshot.forks) > 16 {
			t.Error("fork state exceeded fixed bound")
		}
	}
	caller.inviteMu.Unlock()
	if err := caller.Close(); err != nil {
		t.Fatal(err)
	}
	caller.inviteMu.Lock()
	defer caller.inviteMu.Unlock()
	if len(caller.invites) != 0 {
		t.Fatal("closed account retained fork cleanup state")
	}
}
