package sip

import (
	"context"
	"testing"

	wire "github.com/emiago/sipgo/sip"
)

func TestUpdateResponseRefreshesTargetForLaterRequests(t *testing.T) {
	moved := newForkPeer(t, false)
	caller, callerEvents := newTestClient(t, "alice")
	receiverEvents := make(chan Event, 32)
	receiver, err := newClient(Config{Server: "127.0.0.1", Username: "bob", LocalAddress: "127.0.0.1:0"}, func(e Event) { receiverEvents <- e }, func(c *Client) {
		c.server.OnUpdate(func(req *wire.Request, tx wire.ServerTransaction) {
			response := wire.NewResponseFromRequest(req, 200, "OK", nil)
			response.AppendHeader(wire.HeaderClone(&moved.client.contact))
			_ = tx.Respond(response)
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	defer receiver.Close()
	id := connectTestCall(t, caller, receiver, callerEvents, receiverEvents)
	if err := caller.Update(context.Background(), id, nil); err != nil {
		t.Fatal(err)
	}
	if err := caller.SendDTMF(context.Background(), id, "5"); err != nil {
		t.Fatal(err)
	}
	nextForkRequest(t, moved.infos)
	if err := caller.Hangup(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	nextForkRequest(t, moved.byes)
	receiver.AbortCall(id)
}

func TestIncomingUpdateRefreshesTargetForLaterRequests(t *testing.T) {
	moved := newForkPeer(t, false)
	caller, callerEvents := newTestClient(t, "alice")
	receiver, receiverEvents := newTestClient(t, "bob")
	id := connectTestCall(t, caller, receiver, callerEvents, receiverEvents)
	if _, err := caller.inDialog(context.Background(), id, wire.UPDATE, nil, wire.HeaderClone(&moved.client.contact)); err != nil {
		t.Fatal(err)
	}
	if err := receiver.SendDTMF(context.Background(), id, "5"); err != nil {
		t.Fatal(err)
	}
	nextForkRequest(t, moved.infos)
	if err := receiver.Hangup(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	nextForkRequest(t, moved.byes)
	caller.AbortCall(id)
}
