package media

import (
	"context"
	"encoding/binary"
	"testing"
	"time"

	"github.com/megakuul/voiper/pkg/audio"
)

func TestEarlyOfferKeepsCaptureClosedUntilFinalAnswer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	settings := Settings{BindAddress: "127.0.0.1:0", Codecs: []string{"PCMA", "PCMU"}, DisableAutoRecovery: true}
	caller, err := NewCall(ctx, "caller", settings, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer caller.Close()
	initialOffer := caller.LocalSDP("127.0.0.1")
	peer, err := NewCall(ctx, "peer", settings, initialOffer)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	peer.openAudio = syntheticAudio
	if err = peer.Connect(initialOffer); err != nil {
		t.Fatal(err)
	}
	microphoneOpens := 0
	caller.openPlayback = syntheticAudio
	caller.openCapture = func(settings audio.Settings, rate int) (*audio.Stream, error) {
		microphoneOpens++
		return syntheticAudio(settings, rate)
	}
	originalAnswer := peer.LocalSDP("127.0.0.1")
	if _, err = caller.AnswerEarlyOffer(originalAnswer, "127.0.0.1"); err == nil {
		t.Fatal("accepted early offer before provisional playback existed")
	}
	if err = caller.ConnectEarly(originalAnswer); err != nil {
		t.Fatal(err)
	}
	replacement := codecOffer(t, peer, "PCMU")
	answer, err := caller.AnswerEarlyOffer(replacement, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if err = peer.Connect(answer); err != nil {
		t.Fatal(err)
	}
	if microphoneOpens != 0 || caller.captureStream != nil || !caller.Stats().EarlyMedia || caller.Stats().Codec != "PCMU" {
		t.Fatal("early codec change activated capture or discarded provisional state")
	}
	frame := make([]byte, 320)
	for i := 0; i < len(frame); i += 2 {
		binary.LittleEndian.PutUint16(frame[i:], 1200)
	}
	heard := false
	for !heard {
		select {
		case <-ctx.Done():
			t.Fatal("updated early playback stopped")
		case <-time.After(20 * time.Millisecond):
		}
		peer.captureStream.Capture.Write(frame)
		output := make([]byte, 320)
		count := caller.stream.Playback.Read(output)
		for i := 0; i+1 < count; i += 2 {
			if int16(binary.LittleEndian.Uint16(output[i:])) > 500 {
				heard = true
			}
		}
	}
	if caller.Stats().PacketsSent != 0 {
		t.Fatal("early offer enabled RTP capture transmission")
	}
	if err = caller.Connect(replacement); err != nil {
		t.Fatal(err)
	}
	if microphoneOpens != 1 || caller.Stats().EarlyMedia {
		t.Fatal("final answer did not activate capture exactly once")
	}
	if _, err = caller.AnswerEarlyOffer(replacement, "127.0.0.1"); err == nil {
		t.Fatal("confirmed call accepted an early-only operation")
	}
	exchangePCM(t, caller, peer)
}
