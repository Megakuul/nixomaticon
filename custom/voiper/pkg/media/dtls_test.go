package media

import (
	"context"
	"strings"
	"testing"
	"time"
)

func dtlsCalls(t *testing.T, icePolicy string) (*Call, *Call) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	t.Cleanup(cancel)
	settings := Settings{BindAddress: "127.0.0.1:0", Codecs: []string{"PCMA"}, MediaSecurity: "dtls", SecureSignaling: true, ICEPolicy: icePolicy, DisableAutoRecovery: true}
	a, err := NewCall(ctx, "a", settings, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	offer := a.LocalSDP("127.0.0.1")
	if !strings.Contains(string(offer), "UDP/TLS/RTP/SAVP") || !strings.Contains(string(offer), "a=setup:actpass") || strings.Contains(string(offer), "a=crypto:") {
		t.Fatal("incorrect DTLS offer")
	}
	b, err := NewCall(ctx, "b", settings, offer)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	a.openAudio, b.openAudio = syntheticAudio, syntheticAudio
	if a.Stats().Encrypted || b.Stats().Encrypted {
		t.Fatal("media reported encrypted before handshake")
	}
	answer := b.LocalSDP("127.0.0.1")
	done := make(chan error, 1)
	go func() { done <- b.Connect(offer) }()
	if err = a.Connect(answer); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if !a.Stats().Encrypted || !b.Stats().Encrypted {
		t.Fatal("handshake did not enable SRTP")
	}
	return a, b
}

func TestDTLSMediaAndRenegotiation(t *testing.T) {
	for _, policy := range []string{"disabled", "required"} {
		t.Run(policy, func(t *testing.T) {
			a, b := dtlsCalls(t, policy)
			exchangePCM(t, a, b)
			offer := a.LocalOffer("127.0.0.1", true)
			answer, err := b.AnswerOffer(offer, "127.0.0.1")
			if err != nil {
				t.Fatal(err)
			}
			if err = a.AcceptAnswer(offer, answer); err != nil {
				t.Fatal(err)
			}
			offer = a.LocalOffer("127.0.0.1", false)
			answer, err = b.AnswerOffer(offer, "127.0.0.1")
			if err != nil {
				t.Fatal(err)
			}
			if err = a.AcceptAnswer(offer, answer); err != nil {
				t.Fatal(err)
			}
			exchangePCM(t, a, b)
			if policy == "required" {
				offer, err = a.RestartOffer(context.Background(), "127.0.0.1")
				if err != nil {
					t.Fatal(err)
				}
				answer, err = b.AnswerOffer(offer, "127.0.0.1")
				if err != nil {
					t.Fatal(err)
				}
				if err = a.AcceptAnswer(offer, answer); err != nil {
					t.Fatal(err)
				}
				timeout := time.After(2 * time.Second)
				for a.Stats().ICERestartPending || b.Stats().ICERestartPending {
					select {
					case <-timeout:
						t.Fatal("DTLS ICE restart did not finish")
					case <-time.After(10 * time.Millisecond):
					}
				}
				exchangePCM(t, a, b)
			}
		})
	}
}

func TestDTLSDowngradeAndIdentityChangesRejected(t *testing.T) {
	a, b := dtlsCalls(t, "disabled")
	offer := a.CurrentOffer("127.0.0.1")
	downgrade := strings.Replace(string(offer), "UDP/TLS/RTP/SAVP", "RTP/AVP", 1)
	if _, err := b.AnswerOffer([]byte(downgrade), "127.0.0.1"); err == nil {
		t.Fatal("accepted plaintext downgrade")
	}
	changed := strings.Replace(string(offer), a.fingerprint, "sha-256 "+strings.Repeat("00:", 31)+"00", 1)
	if _, err := b.AnswerOffer([]byte(changed), "127.0.0.1"); err == nil {
		t.Fatal("accepted changed certificate fingerprint")
	}
	exchangePCM(t, a, b)
}

func TestDTLSRequiresVerifiedSignaling(t *testing.T) {
	call, err := NewCall(context.Background(), "untrusted", Settings{MediaSecurity: "dtls"}, nil)
	if err == nil {
		call.Close()
		t.Fatal("DTLS accepted unauthenticated signaling")
	}
}

func TestDTLSSDPRejectsAmbiguousOrIncompatibleNegotiation(t *testing.T) {
	a, b := dtlsCalls(t, "disabled")
	offer := string(a.CurrentOffer("127.0.0.1"))
	cases := map[string]string{
		"duplicate fingerprint": offer + "a=fingerprint:" + a.fingerprint + "\r\n",
		"duplicate setup":       offer + "a=setup:passive\r\n",
		"duplicate association": offer + "a=tls-id:" + a.tlsID + "\r\n",
		"invalid fingerprint":   strings.Replace(offer, a.fingerprint, "sha-256 invalid", 1),
		"changed association":   strings.Replace(offer, "a=tls-id:"+a.tlsID, "a=tls-id:"+strings.Repeat("x", 32), 1),
		"changed role":          strings.Replace(offer, "a=setup:actpass", "a=setup:active", 1),
		"no rtcp mux":           strings.Replace(offer, "a=rtcp-mux\r\n", "", 1),
		"SDES mixed with DTLS":  offer + "a=crypto:1 AES_CM_128_HMAC_SHA1_80 inline:key\r\n",
	}
	for name, changed := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := b.AnswerOffer([]byte(changed), "127.0.0.1"); err == nil {
				t.Fatal("accepted incompatible SDP")
			}
		})
	}
	exchangePCM(t, a, b)
}
