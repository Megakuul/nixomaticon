package media

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/megakuul/voiper/pkg/codec"
)

func renegotiationPair(t *testing.T, security bool) (*Call, *Call) {
	t.Helper()
	settings := Settings{BindAddress: "127.0.0.1:0", Codecs: []string{"PCMA", "PCMU", "G722", "opus"}, DisableAutoRecovery: true}
	if security {
		settings.MediaSecurity = "required"
		settings.SecureSignaling = true
	}
	a, err := NewCall(context.Background(), "caller", settings, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	offer := a.LocalSDP("127.0.0.1")
	b, err := NewCall(context.Background(), "receiver", settings, offer)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	a.openAudio = syntheticAudio
	b.openAudio = syntheticAudio
	if err = b.Connect(offer); err != nil {
		t.Fatal(err)
	}
	if err = a.Connect(b.LocalSDP("127.0.0.1")); err != nil {
		t.Fatal(err)
	}
	return a, b
}
func codecOffer(t *testing.T, source *Call, name string) []byte {
	t.Helper()
	for _, format := range codec.Available() {
		if strings.EqualFold(format.Name, name) {
			return localSDP(source.id, 100, "127.0.0.1", source.session.LocalAddr().Port, source.session.ControlAddr().Port, []codec.Format{format}, 101, true, format.ClockRate, "sendrecv", source.localKey, source.cryptoTag)
		}
	}
	t.Fatalf("codec %s unavailable", name)
	return nil
}
func exchangePCM(t *testing.T, a, b *Call) {
	t.Helper()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.After(2 * time.Second)
	heard := []bool{false, false}
	output := make([]byte, 1920)
	frames := 0
	for !heard[0] || !heard[1] {
		select {
		case <-deadline:
			t.Fatalf("renegotiated duplex PCM did not arrive: caller=%+v receiver=%+v", a.Stats(), b.Stats())
		case <-ticker.C:
			frames++
			for i, call := range []*Call{a, b} {
				frame := make([]byte, call.deviceRate/50*2)
				for sample := 0; sample < len(frame); sample += 2 {
					value := int16(2500 * math.Sin(2*math.Pi*400*float64(sample/2)/float64(call.deviceRate)))
					binary.LittleEndian.PutUint16(frame[sample:], uint16(value))
				}
				call.captureStream.Capture.Write(frame)
				n := call.stream.Playback.Read(output)
				for sample := 0; sample+1 < n; sample += 2 {
					if frames >= 6 && int16(binary.LittleEndian.Uint16(output[sample:])) > 400 {
						heard[i] = true
					}
				}
			}
		}
	}
}
func TestCodecRenegotiationPreservesDevicesAndSRTP(t *testing.T) {
	a, b := renegotiationPair(t, true)
	oldA, oldB := a.stream, b.stream
	exchangePCM(t, a, b)
	answer, err := b.AnswerOffer(codecOffer(t, a, "PCMU"), "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Connect(answer); err != nil {
		t.Fatal(err)
	}
	exchangePCM(t, a, b)
	if a.stream != oldA || b.stream != oldB || a.Stats().Codec != "PCMU" || !a.Stats().Encrypted || !b.Stats().Encrypted {
		t.Fatal("codec update changed device or secure transport ownership")
	}
}
func TestCodecChangePreservesRecordingAndConference(t *testing.T) {
	for _, mode := range []string{"recording", "conference"} {
		t.Run(mode, func(t *testing.T) {
			a, b := renegotiationPair(t, false)
			if mode == "recording" {
				if err := b.StartRecording(filepath.Join(t.TempDir(), "call.wav")); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := JoinConference(a, b); err != nil {
					t.Fatal(err)
				}
			}
			answer, err := b.AnswerOffer(codecOffer(t, a, "PCMU"), "127.0.0.1")
			if err != nil {
				t.Fatal(err)
			}
			if err = a.Connect(answer); err != nil {
				t.Fatal(err)
			}
			exchangePCM(t, a, b)
			if mode == "recording" {
				if !b.Stats().Recording {
					t.Fatal("same-rate update stopped recording")
				}
				if err = b.StopRecording(); err != nil {
					t.Fatal(err)
				}
			} else {
				if !a.Stats().Conference || !b.Stats().Conference {
					t.Fatal("same-rate update detached conference")
				}
			}
		})
	}
}
func TestCloseDuringCodecWorkerReplacement(t *testing.T) {
	a, b := renegotiationPair(t, false)
	b.closeTimeout = 20 * time.Millisecond
	previous := b.pipeline
	previous.workers.Add(1)
	stopped, release := make(chan struct{}), make(chan struct{})
	go func() { <-previous.ctx.Done(); close(stopped); <-release; previous.workers.Done() }()
	updated := make(chan error, 1)
	go func() { _, err := b.AnswerOffer(codecOffer(t, a, "PCMU"), "127.0.0.1"); updated <- err }()
	<-stopped
	if err := b.Close(); !errors.Is(err, ErrCleanupPending) {
		close(release)
		t.Fatalf("close during codec change: %v", err)
	}
	close(release)
	select {
	case err := <-updated:
		if err == nil {
			t.Fatal("closed call accepted codec change")
		}
	case <-time.After(time.Second):
		t.Fatal("codec replacement ignored shutdown")
	}
	select {
	case <-b.closedDone:
	case <-time.After(time.Second):
		t.Fatal("codec resources not cleaned after shutdown")
	}
}

func TestCurrentOfferAndAnswerValidationAreStatePreserving(t *testing.T) {
	a, b := renegotiationPair(t, true)
	a.SetHeld(true)
	offer := a.CurrentOffer("127.0.0.1")
	if !strings.Contains(string(offer), "a=sendonly") || !strings.Contains(string(offer), "a=crypto:") {
		t.Fatal("fresh offer lost hold or security")
	}
	answer := strings.Replace(string(b.LocalSDP("127.0.0.1")), "a=sendrecv", "a=recvonly", 1)
	previous := a.pipeline
	if err := a.ValidateAnswer([]byte(answer)); err != nil {
		t.Fatal(err)
	}
	if a.pipeline != previous || !a.Stats().Held {
		t.Fatal("validation changed running media")
	}
	invalid := strings.Replace(answer, "RTP/SAVP 8 101", "RTP/SAVP 0 101", 1)
	if err := a.ValidateAnswer([]byte(invalid)); err == nil {
		t.Fatal("answer changed offered codec")
	}
	invalid = strings.Replace(answer, "a=recvonly", "a=sendrecv", 1)
	if err := a.ValidateAnswer([]byte(invalid)); err == nil {
		t.Fatal("answer violated local hold direction")
	}
	if a.pipeline != previous || a.Stats().Codec != "PCMA" {
		t.Fatal("invalid answer changed running media")
	}
}

func TestAcceptAnswerUsesWireOfferAndCommitsHold(t *testing.T) {
	a, b := renegotiationPair(t, false)
	offer := a.LocalOffer("127.0.0.1", true)
	answer, err := b.AnswerOffer(offer, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	newer := a.LocalOffer("127.0.0.1", false)
	if err = a.AcceptAnswer(offer, answer); err != nil {
		t.Fatal(err)
	}
	if !a.Stats().Held || !bytes.Equal(a.localOffer, newer) {
		t.Fatal("wire offer did not commit hold or consumed a newer pending offer")
	}
	answer, err = b.AnswerOffer(newer, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	pendingHold := a.CurrentOffer("127.0.0.1")
	if err = a.ValidateAnswer(answer); err == nil {
		t.Fatal("test requires answer incompatible with pending hold offer")
	}
	if err = a.AcceptAnswer(newer, answer); err != nil {
		t.Fatal(err)
	}
	if a.Stats().Held || !bytes.Equal(a.localOffer, pendingHold) {
		t.Fatal("explicit resume answer used the mutable pending hold offer")
	}
	exchangePCM(t, a, b)
}

func TestAcceptAnswerRejectsUnofferedCodecWithoutChangingState(t *testing.T) {
	a, b := renegotiationPair(t, true)
	offer := a.LocalOffer("127.0.0.1", true)
	previous := a.pipeline
	if err := a.AcceptAnswer(offer, codecOffer(t, b, "PCMU")); err == nil {
		t.Fatal("accepted codec absent from wire offer")
	}
	if a.pipeline != previous || a.Stats().Held || !bytes.Equal(a.localOffer, offer) {
		t.Fatal("rejected answer changed codec, hold or pending offer")
	}
	exchangePCM(t, a, b)
}

// A session refresh may re-send the initial multi-codec offer, and answers such
// as SwyxIt!'s list further codecs. The peer keeps sending the established codec.
func TestAnswerListingFurtherCodecsKeepsEstablishedCodec(t *testing.T) {
	a, err := NewCall(context.Background(), "caller", Settings{BindAddress: "127.0.0.1:0", Codecs: []string{"G722", "PCMA"}, DisableAutoRecovery: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	a.openAudio = syntheticAudio
	offer := a.LocalSDP("127.0.0.1")
	formats := map[string]codec.Format{}
	for _, format := range codec.Available() {
		formats[format.Name] = format
	}
	answer := localSDP(7, 1, "127.0.0.1", 40000, 40001, []codec.Format{formats["PCMA"]}, 101, true, 8000, "sendrecv", nil, "")
	if err = a.Connect(answer); err != nil {
		t.Fatal(err)
	}
	refreshAnswer := localSDP(7, 2, "127.0.0.1", 40000, 40001, []codec.Format{formats["PCMA"], formats["G722"]}, 101, true, 8000, "sendrecv", nil, "")
	if err = a.AcceptAnswer(offer, refreshAnswer); err != nil {
		t.Fatal(err)
	}
	if codec := a.Stats().Codec; codec != "PCMA" {
		t.Fatalf("answer moved established PCMA call to %s", codec)
	}
}
