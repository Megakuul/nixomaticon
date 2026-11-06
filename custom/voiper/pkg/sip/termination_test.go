package sip

import (
	"context"
	"testing"

	wire "github.com/emiago/sipgo/sip"
)

func TestReasonCauseIgnoresArbitraryText(t *testing.T) {
	for _, tc := range []struct {
		reason string
		want   bool
	}{
		{`SIP ;cause=200 ;text="Call completed elsewhere"`, true},
		{`Q.850;cause=16, SIP;cause=200`, true},
		{`SIP;cause=486;text="Answered elsewhere"`, false},
		{`Q.850;cause=200;text="Answered elsewhere"`, false},
		{`SIP;cause=486;text="untrusted, SIP;cause=200"`, false},
		{`SIP;cause=486;text="escaped \", SIP;cause=200"`, false},
		{`SIP;cause=486;cause=200`, false},
	} {
		t.Run(tc.reason, func(t *testing.T) {
			req := wire.NewRequest(wire.CANCEL, wire.Uri{Scheme: "sip", Host: "example.test"})
			req.AppendHeader(wire.NewHeader("Reason", tc.reason))
			if got := completedElsewhere(req); got != tc.want {
				t.Fatalf("completed elsewhere = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCancelReasonRequiresMatchingInviteIdentity(t *testing.T) {
	for _, spoofed := range []bool{false, true} {
		name := "matched"
		if spoofed {
			name = "wrong-from-tag"
		}
		t.Run(name, func(t *testing.T) {
			caller, _ := newTestClient(t, "alice")
			receiver, events := newTestClient(t, "bob")
			id, err := caller.Dial(context.Background(), receiver.contact.Address.String(), []byte(offerSDP))
			if err != nil {
				t.Fatal(err)
			}
			nextEvent(t, events, "incoming")
			cl, err := receiver.getCall(id)
			if err != nil {
				t.Fatal(err)
			}
			cl.mu.Lock()
			cancel := cl.incoming.InviteRequest.Clone()
			cl.mu.Unlock()
			cancel.Method = wire.CANCEL
			cancel.CSeq().MethodName = wire.CANCEL
			cancel.To().Params.Remove("tag")
			if spoofed {
				cancel.From().Params.Add("tag", "foreign-dialog")
			}
			cancel.SetBody(nil)
			cancel.RemoveHeader("Content-Type")
			cancel.AppendHeader(wire.NewHeader("Reason", `SIP;cause=200;text="untrusted display text"`))
			cancel.SetDestination(receiver.LocalAddr())
			if err := caller.client.WriteRequest(cancel); err != nil {
				t.Fatal(err)
			}
			ended := nextEvent(t, events, "ended")
			want := "answered-elsewhere"
			if spoofed {
				want = ""
			}
			if ended.TerminationReason != want {
				t.Fatalf("ended reason = %q, want %q", ended.TerminationReason, want)
			}
		})
	}
}
