package rtp

import (
	"context"
	"testing"

	"github.com/pion/rtcp"
)

func TestReceiverLossReportsAreScopedAndFresh(t *testing.T) {
	s, err := New(context.Background(), "127.0.0.1:0", 8000)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	peer, err := New(context.Background(), "127.0.0.1:0", 8000)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	if err = s.SetRemote(peer.LocalAddr(), peer.ControlAddr(), 8000); err != nil {
		t.Fatal(err)
	}
	if err = s.Send(0, []byte{1}, 160, false); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	source := s.ssrc
	s.mu.Unlock()
	if s.Stats().LastSent.IsZero() {
		t.Fatal("successful RTP send did not record activity")
	}
	report := func(source, sequence uint32, loss uint8, trusted bool) {
		t.Helper()
		data, err := (&rtcp.ReceiverReport{SSRC: 77, Reports: []rtcp.ReceptionReport{{SSRC: source, LastSequenceNumber: sequence, FractionLost: loss}}}).Marshal()
		if err != nil {
			t.Fatal(err)
		}
		from := peer.ControlAddr()
		if !trusted {
			from = peer.LocalAddr()
		}
		s.handleControl(data, from, false)
	}
	report(source+1, 100, 80, true)
	report(source, 100, 80, false)
	if !s.Stats().ReceiverReportAt.IsZero() {
		t.Fatal("unrelated report accepted")
	}
	report(source, 100, 25, true)
	first := s.Stats()
	if first.ReceiverReportAt.IsZero() || first.RemoteFractionLost != 25 {
		t.Fatalf("fresh report: %+v", first)
	}
	report(source, 100, 99, true)
	report(source, 99, 99, true)
	if stats := s.Stats(); stats.ReceiverReportAt != first.ReceiverReportAt || stats.RemoteFractionLost != 25 {
		t.Fatal("old report changed feedback")
	}
	report(source, 101, 3, true)
	if s.Stats().RemoteFractionLost != 3 {
		t.Fatal("new report not accepted")
	}
	if err = s.SetRemote(peer.LocalAddr(), peer.ControlAddr(), 48000); err != nil {
		t.Fatal(err)
	}
	if !s.Stats().ReceiverReportAt.IsZero() {
		t.Fatal("old sender feedback survived clock change")
	}
}
