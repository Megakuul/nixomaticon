package media

import (
	"testing"
	"time"

	"github.com/megakuul/voiper/pkg/rtp"
)

func TestMediaFlowDiagnosis(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		name, want                                          string
		send, receive, early, held, closed, stopped, secure bool
		sent, received                                      bool
		age                                                 time.Duration
		warning                                             bool
	}{
		{name: "startup grace", want: "waiting", send: true, receive: true, age: time.Second},
		{name: "duplex", want: "duplex", send: true, receive: true, sent: true, received: true},
		{name: "no packets", want: "no-media", send: true, receive: true, warning: true},
		{name: "one way inbound", want: "missing-outbound", send: true, receive: true, received: true, warning: true},
		{name: "one way outbound", want: "missing-inbound", send: true, receive: true, sent: true, warning: true},
		{name: "receive only", want: "receiving", receive: true, received: true},
		{name: "send only", want: "sending", send: true, sent: true},
		{name: "early playback", want: "receiving", send: true, receive: true, received: true, early: true},
		{name: "inactive", want: "inactive"},
		{name: "held", want: "held", send: true, receive: true, held: true},
		{name: "closed", want: "closed", send: true, receive: true, closed: true},
		{name: "stopped device", want: "device-unavailable", send: true, receive: true, stopped: true, warning: true},
		{name: "DTLS handshake", want: "securing", send: true, receive: true, secure: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			age := tc.age
			if age == 0 {
				age = 10 * time.Second
			}
			c := &Call{connected: !tc.early, early: tc.early, closed: tc.closed, secure: tc.secure, mediaStarted: now.Add(-age)}
			c.sendEnabled.Store(tc.send)
			c.receiveEnabled.Store(tc.receive)
			transport := rtp.Stats{}
			if tc.sent {
				transport.LastSent = now
			}
			if tc.received {
				transport.LastReceived = now
			}
			result := Stats{Held: tc.held, AudioStopped: tc.stopped, Muted: true}
			c.diagnoseMedia(&result, transport, now)
			if result.MediaFlow != tc.want || (result.MediaWarning != "") != tc.warning {
				t.Fatalf("got %+v, want flow %s warning %v", result, tc.want, tc.warning)
			}
		})
	}
}
