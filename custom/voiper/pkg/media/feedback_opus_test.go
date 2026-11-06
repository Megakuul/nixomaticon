//go:build opus

package media

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/megakuul/voiper/pkg/codec"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
)

func TestOpusCaptureAppliesPeerReceiverReport(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	call, err := NewCall(ctx, "feedback", Settings{BindAddress: "127.0.0.1:0", Codecs: []string{"opus"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer call.Close()
	call.openAudio = syntheticAudio
	call.LocalSDP("127.0.0.1")
	peer, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	control, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	format := codec.Format{Name: "opus", PayloadType: 111, SampleRate: 48000, ClockRate: 48000, Channels: 2}
	answer := localSDP(1, 1, "127.0.0.1", peer.LocalAddr().(*net.UDPAddr).Port, control.LocalAddr().(*net.UDPAddr).Port, []codec.Format{format}, 102, true, 48000, "sendrecv", nil, "")
	if err = call.Connect(answer); err != nil {
		t.Fatal(err)
	}
	if err = peer.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	data := make([]byte, 1500)
	n, _, err := peer.ReadFromUDP(data)
	if err != nil {
		t.Fatal(err)
	}
	var packet rtp.Packet
	if err = packet.Unmarshal(data[:n]); err != nil {
		t.Fatal(err)
	}
	report, err := (&rtcp.ReceiverReport{SSRC: 77, Reports: []rtcp.ReceptionReport{{SSRC: packet.SSRC, LastSequenceNumber: uint32(packet.SequenceNumber), FractionLost: 26}}}).Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = control.WriteToUDP(report, call.session.ControlAddr()); err != nil {
		t.Fatal(err)
	}
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			t.Fatal("Opus capture did not apply fresh receiver loss feedback")
		case <-tick.C:
			stats := call.Stats()
			if stats.EncoderBitrate == 28800 && stats.ReceiverReportAvailable && stats.RemotePacketLossPercent > 10 {
				return
			}
		}
	}
}
