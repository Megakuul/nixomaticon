package sip

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/emiago/sipgo"
	wire "github.com/emiago/sipgo/sip"
)

func TestPresenceSubscription(t *testing.T) {
	ua, err := sipgo.NewUA()
	if err != nil {
		t.Fatal(err)
	}
	defer ua.Close()
	server, err := sipgo.NewServer(ua)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	peer, err := sipgo.NewClient(ua, sipgo.WithClientAddr(conn.LocalAddr().String()))
	if err != nil {
		t.Fatal(err)
	}
	notifications := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	server.OnSubscribe(func(req *wire.Request, tx wire.ServerTransaction) {
		response := wire.NewResponseFromRequest(req, 200, "OK", nil)
		response.AppendHeader(wire.NewHeader("Expires", req.GetHeader("Expires").Value()))
		tx.Respond(response)
		if req.GetHeader("Expires").Value() == "0" {
			return
		}
		notify := wire.NewRequest(wire.NOTIFY, req.Contact().Address)
		from := response.To().AsFrom()
		to := req.From().AsTo()
		notify.AppendHeader(&from)
		notify.AppendHeader(&to)
		notify.AppendHeader(wire.HeaderClone(req.CallID()))
		notify.AppendHeader(&wire.CSeqHeader{SeqNo: 1, MethodName: wire.NOTIFY})
		notify.AppendHeader(wire.NewHeader("Event", "presence"))
		notify.AppendHeader(wire.NewHeader("Subscription-State", "active;expires=3600"))
		notify.AppendHeader(wire.NewHeader("Content-Type", "application/pidf+xml"))
		notify.SetBody([]byte(`<presence xmlns="urn:ietf:params:xml:ns:pidf" entity="sip:bob@localhost"><tuple id="1"><status><basic>open</basic></status></tuple></presence>`))
		res, err := peer.Do(ctx, notify)
		if err == nil && res.StatusCode != 200 {
			err = &ResponseError{Status: res.StatusCode, Reason: res.Reason}
		}
		notifications <- err
	})
	ready := make(chan struct{})
	go server.ServeUDP(&readyPacketConn{PacketConn: conn, ready: ready})
	<-ready
	client, events := newTestClient(t, "alice")
	target := "sip:bob@" + conn.LocalAddr().String()
	id, err := client.Subscribe(ctx, target, "presence", "application/pidf+xml")
	if err != nil {
		t.Fatal(err)
	}
	event := nextEvent(t, events, "presence")
	if event.CallID != id || event.State != "presence" || event.ContentType != "application/pidf+xml" || len(event.Body) == 0 {
		t.Fatalf("unexpected presence: %+v", event)
	}
	if err := <-notifications; err != nil {
		t.Fatal(err)
	}
	if err := client.Unsubscribe(ctx, id); err != nil {
		t.Fatal(err)
	}
	client.mu.Lock()
	count := len(client.subscriptions)
	client.mu.Unlock()
	if count != 0 {
		t.Fatal("unsubscribed dialog retained")
	}
}
