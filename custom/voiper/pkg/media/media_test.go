package media

import (
	"context"
	"encoding/binary"
	"strings"
	"testing"
	"time"

	"github.com/megakuul/voiper/pkg/audio"
	"github.com/megakuul/voiper/pkg/codec"
)

func TestOfferAnswerPayloadMapping(t *testing.T) {
	data := []byte("v=0\r\no=- 1 1 IN IP4 127.0.0.1\r\ns=test\r\nc=IN IP4 127.0.0.1\r\nt=0 0\r\nm=audio 3000 RTP/AVP 105 101\r\na=rtpmap:105 G722/8000\r\na=rtpmap:101 telephone-event/8000\r\na=sendonly\r\n")
	remote, err := negotiate(data, codec.Available())
	if err != nil {
		t.Fatal(err)
	}
	if remote.format.Name != "G722" || remote.format.PayloadType != 105 || remote.send || !remote.receive || !remote.hasTelephone {
		t.Fatalf("negotiation %+v", remote)
	}
}
func TestSDPRejectsUnsupportedMedia(t *testing.T) {
	for _, body := range []string{
		"v=0\r\no=- 1 1 IN IP4 127.0.0.1\r\ns=test\r\nt=0 0\r\nm=audio 3000 RTP/AVP 8\r\n",
		"v=0\r\no=- 1 1 IN IP4 127.0.0.1\r\ns=test\r\nc=IN IP4 127.0.0.1\r\nt=0 0\r\nm=audio 3000 RTP/SAVP 8\r\n",
		"v=0\r\no=- 1 1 IN IP4 127.0.0.1\r\ns=test\r\nc=IN IP4 127.0.0.1\r\nt=0 0\r\nm=audio 3000 RTP/AVP 120\r\na=rtpmap:120 unknown/8000\r\n",
	} {
		if _, err := negotiate([]byte(body), codec.Available()); err == nil {
			t.Fatalf("accepted unsupported SDP %q", body)
		}
	}
}
func syntheticAudio(_ audio.Settings, rate int) (*audio.Stream, error) {
	return &audio.Stream{Capture: audio.NewRing(rate * 2), Playback: audio.NewRing(rate * 2)}, nil
}
func TestDuplexMediaLoopback(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	settings := Settings{BindAddress: "127.0.0.1:0", Codecs: []string{"PCMA"}}
	a, err := NewCall(ctx, "a", settings, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.openAudio = syntheticAudio
	offer := a.LocalSDP("127.0.0.1")
	b, err := NewCall(ctx, "b", settings, offer)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	b.openAudio = syntheticAudio
	if err = b.Connect(offer); err != nil {
		t.Fatal(err)
	}
	if err = a.Connect(b.LocalSDP("127.0.0.1")); err != nil {
		t.Fatal(err)
	}
	frame := make([]byte, 320)
	for i := 0; i < len(frame); i += 2 {
		binary.LittleEndian.PutUint16(frame[i:], 1000)
	}
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	receivedA, receivedB := false, false
	output := make([]byte, 320)
	for !receivedA || !receivedB {
		select {
		case <-ctx.Done():
			t.Fatal("synthetic duplex media timed out")
		case <-ticker.C:
			a.stream.Capture.Write(frame)
			b.stream.Capture.Write(frame)
			for _, side := range []struct {
				call     *Call
				received *bool
			}{{a, &receivedA}, {b, &receivedB}} {
				n := side.call.stream.Playback.Read(output)
				for i := 0; i+1 < n; i += 2 {
					if int16(binary.LittleEndian.Uint16(output[i:])) > 500 {
						*side.received = true
					}
				}
			}
		}
	}
	if stats := a.Stats(); stats.Codec != "PCMA" || stats.PacketsReceived == 0 || stats.PacketsSent == 0 {
		t.Fatalf("stats %+v", stats)
	}
	heldOffer := strings.Replace(string(offer), "a=sendrecv", "a=sendonly", 1)
	answer, err := b.AnswerOffer([]byte(heldOffer), "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(answer), "a=recvonly") {
		t.Fatalf("hold answer: %s", answer)
	}
}

func TestSRTPRequiresProtectedSignaling(t *testing.T) {
	_, err := NewCall(context.Background(), "insecure", Settings{MediaSecurity: "required"}, nil)
	if err == nil {
		t.Fatal("SDES keys allowed over unprotected signaling")
	}
	settings := Settings{BindAddress: "127.0.0.1:0", Codecs: []string{"PCMA"}, MediaSecurity: "required", SecureSignaling: true}
	a, err := NewCall(context.Background(), "a", settings, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	offer := a.LocalSDP("127.0.0.1")
	if !strings.Contains(string(offer), "RTP/SAVP") || !strings.Contains(string(offer), "a=crypto:") {
		t.Fatal("missing secure offer")
	}
	b, err := NewCall(context.Background(), "b", settings, offer)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	a.openAudio = syntheticAudio
	b.openAudio = syntheticAudio
	if err = b.Connect(offer); err != nil {
		t.Fatal(err)
	}
	if err = a.Connect(b.LocalSDP("127.0.0.1")); err != nil {
		t.Fatal(err)
	}
	if !a.Stats().Encrypted || !b.Stats().Encrypted {
		t.Fatal("SRTP not reported")
	}
	plain := strings.Replace(string(offer), "RTP/SAVP", "RTP/AVP", 1)
	if err = a.Connect([]byte(plain)); err == nil {
		t.Fatal("accepted mid-call security downgrade")
	}
}

func TestConferenceMixMinus(t *testing.T) {
	calls := []*Call{{connected: true, deviceRate: 8000}, {connected: true, deviceRate: 8000}, {connected: true, deviceRate: 8000}}
	if err := JoinConference(calls...); err != nil {
		t.Fatal(err)
	}
	for i, value := range []int16{100, 200, 400} {
		calls[i].conference.Load().forward([]int16{value})
	}
	for i, want := range []int16{600, 500, 300} {
		pcm := []int16{0}
		calls[i].conference.Load().mix(pcm)
		if pcm[0] != want {
			t.Fatalf("leg %d heard %d, want %d", i, pcm[0], want)
		}
	}
	calls[0].LeaveConference()
	if calls[0].conference.Load() != nil {
		t.Fatal("conference leg remained active")
	}
}

func TestDeviceOpeningDoesNotBlockStatsOrHangup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	call, err := NewCall(ctx, "test", Settings{BindAddress: "127.0.0.1:0", Codecs: []string{"PCMA"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	call.closeTimeout = 20 * time.Millisecond
	started := make(chan struct{})
	release := make(chan struct{})
	call.openAudio = func(settings audio.Settings, rate int) (*audio.Stream, error) {
		close(started)
		<-release
		return syntheticAudio(settings, rate)
	}
	done := make(chan error, 1)
	offer := call.LocalSDP("127.0.0.1")
	go func() { done <- call.Connect(offer) }()
	<-started
	responsive := make(chan struct{})
	go func() { call.Stats(); call.Close(); close(responsive) }()
	select {
	case <-responsive:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("device opening blocked state/hangup")
	}
	close(release)
	if err = <-done; err == nil {
		t.Fatal("closed call connected after device opened")
	}
}

func TestAnswerMayRenumberSelectedCodec(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	settings := Settings{BindAddress: "127.0.0.1:0", Codecs: []string{"PCMA"}}
	a, err := NewCall(ctx, "a", settings, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.openAudio = syntheticAudio
	offer := a.LocalSDP("127.0.0.1")
	b, err := NewCall(ctx, "b", settings, offer)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	b.openAudio = syntheticAudio
	if err = b.Connect(offer); err != nil {
		t.Fatal(err)
	}
	// RFC 3264 lets the answer number PCMA 105; b still sends with the offered 8.
	answer := strings.Replace(string(b.LocalSDP("127.0.0.1")), "RTP/AVP 8 101", "RTP/AVP 105 101", 1)
	answer = strings.Replace(answer, "a=rtpmap:8 PCMA", "a=rtpmap:105 PCMA", 1)
	if err = a.Connect([]byte(answer)); err != nil {
		t.Fatal(err)
	}
	frame := make([]byte, 320)
	for i := 0; i < len(frame); i += 2 {
		binary.LittleEndian.PutUint16(frame[i:], 1000)
	}
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	output := make([]byte, 320)
	for {
		select {
		case <-ctx.Done():
			t.Fatal("audio sent with the offered payload number was not played")
		case <-ticker.C:
			b.stream.Capture.Write(frame)
			n := a.stream.Playback.Read(output)
			for i := 0; i+1 < n; i += 2 {
				if int16(binary.LittleEndian.Uint16(output[i:])) > 500 {
					return
				}
			}
		}
	}
}

func TestAnswerMayListUnofferedCodecs(t *testing.T) {
	call, err := NewCall(context.Background(), "extra", Settings{BindAddress: "127.0.0.1:0", Codecs: []string{"PCMA"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer call.Close()
	call.LocalSDP("127.0.0.1")
	// Shaped like a PBX client answering with its whole codec list and its own
	// telephone-event number, both permitted by RFC 3264 section 6.1.
	answer := []byte("v=0\r\no=- 1 1 IN IP4 127.0.0.1\r\ns=pbx\r\nc=IN IP4 127.0.0.1\r\nt=0 0\r\n" +
		"m=audio 3000 RTP/AVP 8 18 9 96\r\na=rtpmap:8 PCMA/8000\r\na=rtpmap:18 G729/8000\r\n" +
		"a=rtpmap:9 G722/16000\r\na=rtpmap:96 telephone-event/8000\r\na=fmtp:96 0-15\r\n")
	if err = call.ValidateAnswer(answer); err != nil {
		t.Fatal(err)
	}
}

func TestInvalidStaticPayloadMappingIsRejected(t *testing.T) {
	sdp := []byte("v=0\r\no=- 1 1 IN IP4 127.0.0.1\r\ns=test\r\nc=IN IP4 127.0.0.1\r\nt=0 0\r\nm=audio 3000 RTP/AVP 0\r\na=rtpmap:0 PCMA/8000\r\n")
	if _, err := negotiate(sdp, codec.Available()); err == nil {
		t.Fatal("accepted static payload 0 remapping")
	}
}

func TestEarlyMediaNeverOpensMicrophone(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	settings := Settings{BindAddress: "127.0.0.1:0", Codecs: []string{"PCMA"}}
	caller, err := NewCall(ctx, "early", settings, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer caller.Close()
	offer := caller.LocalSDP("127.0.0.1")
	peer, err := NewCall(ctx, "peer", settings, offer)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	peer.openAudio = syntheticAudio
	if err = peer.Connect(offer); err != nil {
		t.Fatal(err)
	}
	microphoneOpens := 0
	caller.openPlayback = syntheticAudio
	caller.openCapture = func(settings audio.Settings, rate int) (*audio.Stream, error) {
		microphoneOpens++
		return syntheticAudio(settings, rate)
	}
	answer := peer.LocalSDP("127.0.0.1")
	if err = caller.ConnectEarly(answer); err != nil {
		t.Fatal(err)
	}
	frame := make([]byte, 320)
	for i := 0; i < len(frame); i += 2 {
		binary.LittleEndian.PutUint16(frame[i:], 1000)
	}
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	output := make([]byte, 320)
	heard := false
	for !heard {
		select {
		case <-ctx.Done():
			t.Fatal("early audio timed out")
		case <-ticker.C:
			peer.stream.Capture.Write(frame)
			n := caller.stream.Playback.Read(output)
			for i := 0; i+1 < n; i += 2 {
				if int16(binary.LittleEndian.Uint16(output[i:])) > 500 {
					heard = true
				}
			}
		}
	}
	if microphoneOpens != 0 || caller.captureStream != nil || caller.Stats().PacketsSent != 0 {
		t.Fatal("early media activated microphone or sent RTP")
	}
	if err = caller.Connect(answer); err != nil {
		t.Fatal(err)
	}
	if microphoneOpens != 1 || caller.Stats().EarlyMedia {
		t.Fatal("answer did not activate microphone exactly once")
	}
	caller.captureStream.Capture.Write(frame)
}

func TestRejectedCodecChangePreservesRTCPMux(t *testing.T) {
	call, err := NewCall(context.Background(), "renegotiate", Settings{BindAddress: "127.0.0.1:0", Codecs: []string{"PCMA", "PCMU"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer call.Close()
	call.openAudio = syntheticAudio
	offer := call.LocalSDP("127.0.0.1")
	if err = call.Connect(offer); err != nil {
		t.Fatal(err)
	}
	changed := string(offer)
	changed = strings.Replace(changed, "RTP/AVP 8 0 101", "RTP/AVP 96 101", 1)
	changed = strings.Replace(changed, "a=rtcp-mux\r\n", "", 1)
	if err = call.Connect([]byte(changed)); err == nil {
		t.Fatal("codec change accepted")
	}
	if !call.Stats().RTCPMux || call.Stats().Codec != "PCMA" {
		t.Fatal("rejected update changed negotiated media")
	}
}
