package media

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/megakuul/voiper/pkg/audio"
)

func TestICEMediaNegotiationAndFallback(t *testing.T) {
	for _, peerPolicy := range []string{"required", "disabled"} {
		t.Run(peerPolicy, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			settings := Settings{BindAddress: "127.0.0.1:0", Codecs: []string{"PCMA"}, ICEPolicy: "auto", DisableAutoRecovery: true}
			a, err := NewCall(ctx, "a", settings, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			offer := a.LocalSDP("127.0.0.1")
			if !strings.Contains(string(offer), "a=ice-ufrag:") || !strings.Contains(string(offer), "a=rtcp-mux") {
				t.Fatal("offer missing ICE or RTCP mux")
			}
			settings.ICEPolicy = peerPolicy
			b, err := NewCall(ctx, "b", settings, offer)
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			a.openAudio = syntheticAudio
			b.openAudio = syntheticAudio
			answer := b.LocalSDP("127.0.0.1")
			connected := make(chan error, 1)
			go func() { connected <- a.Connect(answer) }()
			if err = b.Connect(offer); err != nil {
				t.Fatal(err)
			}
			if err = <-connected; err != nil {
				t.Fatal(err)
			}
			expected := "connected"
			if peerPolicy == "disabled" {
				expected = "fallback"
			}
			if a.Stats().ICEState != expected || !a.Stats().RTCPMux {
				t.Fatalf("wrong negotiated transport %+v", a.Stats())
			}
			ticker := time.NewTicker(10 * time.Millisecond)
			defer ticker.Stop()
			for a.Stats().PacketsReceived == 0 || b.Stats().PacketsReceived == 0 {
				select {
				case <-ctx.Done():
					t.Fatal("duplex RTP did not arrive")
				case <-ticker.C:
				}
			}
			if peerPolicy == "required" {
				held := strings.Replace(string(offer), "a=sendrecv", "a=sendonly", 1)
				if _, err = b.AnswerOffer([]byte(held), "127.0.0.1"); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestRequiredICERejectsPlainPeerWithoutMicrophone(t *testing.T) {
	ctx := context.Background()
	call, err := NewCall(ctx, "required", Settings{BindAddress: "127.0.0.1:0", Codecs: []string{"PCMA"}, ICEPolicy: "required"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer call.Close()
	call.openAudio = func(audio.Settings, int) (*audio.Stream, error) {
		t.Fatal("microphone opened before ICE acceptance")
		return nil, nil
	}
	plain := []byte("v=0\r\no=- 1 1 IN IP4 127.0.0.1\r\ns=test\r\nc=IN IP4 127.0.0.1\r\nt=0 0\r\nm=audio 3000 RTP/AVP 8\r\na=sendrecv\r\n")
	if err = call.Connect(plain); err == nil {
		t.Fatal("required ICE accepted plaintext fallback")
	}
}
