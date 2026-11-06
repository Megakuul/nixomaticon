package media

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/megakuul/voiper/pkg/audio"
)

func connectedICECalls(t *testing.T, secure bool) (*Call, *Call) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	t.Cleanup(cancel)
	settings := Settings{BindAddress: "127.0.0.1:0", Codecs: []string{"PCMA"}, ICEPolicy: "required", SecureSignaling: secure, DisableAutoRecovery: true}
	if secure {
		settings.MediaSecurity = "required"
	}
	a, err := NewCall(ctx, "a", settings, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	offer := a.LocalSDP("127.0.0.1")
	b, err := NewCall(ctx, "b", settings, offer)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	a.openAudio, b.openAudio = syntheticAudio, syntheticAudio
	answer := b.LocalSDP("127.0.0.1")
	done := make(chan error, 1)
	go func() { done <- a.Connect(answer) }()
	if err = b.Connect(offer); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	return a, b
}

func waitICEPackets(t *testing.T, a, b *Call, sentA, sentB uint64) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for a.Stats().PacketsReceived <= sentA || b.Stats().PacketsReceived <= sentB {
		select {
		case <-deadline:
			t.Fatal("duplex media stopped")
		case <-ticker.C:
		}
	}
}

func TestICERestartPreservesDuplexMediaAndDevices(t *testing.T) {
	for _, secure := range []bool{false, true} {
		t.Run(map[bool]string{false: "RTP", true: "SRTP"}[secure], func(t *testing.T) {
			a, b := connectedICECalls(t, secure)
			waitICEPackets(t, a, b, 0, 0)
			if err := a.StartRecording(filepath.Join(t.TempDir(), "restart.wav")); err != nil {
				t.Fatal(err)
			}
			for _, caller := range []*Call{a, b} {
				callee := b
				if caller == b {
					callee = a
				}
				caller.openAudio = func(audio.Settings, int) (*audio.Stream, error) {
					t.Error("restart opened audio devices")
					return nil, nil
				}
				callee.openAudio = caller.openAudio
				before := caller.Stats()
				offer, err := caller.RestartOffer(context.Background(), "127.0.0.1")
				if err != nil {
					t.Fatal(err)
				}
				retry, err := caller.RestartOffer(context.Background(), "127.0.0.1")
				if err != nil || !bytes.Equal(offer, retry) {
					t.Fatal("retry replaced the pending offer")
				}
				// Candidate gathering and waiting for SIP must leave existing media flowing.
				waitICEPackets(t, a, b, a.Stats().PacketsReceived, b.Stats().PacketsReceived)
				answer, err := callee.AnswerOffer(offer, "127.0.0.1")
				if err != nil {
					t.Fatal(err)
				}
				if err = caller.AcceptAnswer(offer, answer); err != nil {
					t.Fatal(err)
				}
				deadline := time.After(3 * time.Second)
				ticker := time.NewTicker(10 * time.Millisecond)
				for caller.Stats().ICERestartPending || callee.Stats().ICERestartPending {
					select {
					case <-deadline:
						t.Fatalf("restart timed out: %+v %+v", caller.Stats(), callee.Stats())
					case <-ticker.C:
					}
				}
				ticker.Stop()
				after := caller.Stats()
				if after.LastError != "" || after.ICEState != "connected" || after.LocalAddress == before.LocalAddress || after.Encrypted != secure || after.Codec != before.Codec || !a.Stats().Recording {
					t.Fatalf("restart did not preserve negotiated media: %+v", after)
				}
				exchangePCM(t, a, b)
			}
		})
	}
}

func TestICERestartCancellationAndInvalidAnswerKeepMedia(t *testing.T) {
	a, b := connectedICECalls(t, false)
	waitICEPackets(t, a, b, 0, 0)
	before := a.Stats().LocalAddress
	offer, err := a.RestartOffer(context.Background(), "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if err = a.AcceptAnswer(offer, b.LocalSDP("127.0.0.1")); err == nil {
		t.Fatal("accepted unchanged remote ICE credentials")
	}
	a.CancelRestart([]byte("stale offer"))
	if !a.Stats().ICERestartPending {
		t.Fatal("stale cancellation removed restart")
	}
	a.CancelRestart(offer)
	waitICEPackets(t, a, b, a.Stats().PacketsReceived, b.Stats().PacketsReceived)
	if a.Stats().LocalAddress != before {
		t.Fatal("cancelled restart changed media path")
	}
}

func TestICERestartRejectsCombinedChanges(t *testing.T) {
	a, b := connectedICECalls(t, false)
	waitICEPackets(t, a, b, 0, 0)
	offer, err := a.RestartOffer(context.Background(), "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	defer a.CancelRestart(offer)
	changed := strings.Replace(string(offer), "a=sendrecv", "a=sendonly", 1)
	if _, err = b.AnswerOffer([]byte(changed), "127.0.0.1"); err == nil {
		t.Fatal("accepted restart combined with hold")
	}
	waitICEPackets(t, a, b, a.Stats().PacketsReceived, b.Stats().PacketsReceived)
}

func TestICELiteIsSessionScoped(t *testing.T) {
	a, b := connectedICECalls(t, false)
	offer := string(a.CurrentOffer("127.0.0.1"))
	sessionLite := strings.Replace(offer, "m=audio", "a=ice-lite\r\nm=audio", 1)
	parsed, err := negotiate([]byte(sessionLite), b.formats)
	if err != nil || !parsed.ice.Lite {
		t.Fatalf("session ICE-lite not recognized: %v", err)
	}
	parsed, err = negotiate([]byte(offer+"a=ice-lite\r\n"), b.formats)
	if err != nil || parsed.ice.Lite {
		t.Fatalf("media-level ICE-lite incorrectly applied: %v", err)
	}
}
