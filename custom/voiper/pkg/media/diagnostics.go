package media

import (
	"time"

	"github.com/megakuul/voiper/pkg/rtp"
)

// diagnoseMedia describes packet flow, not whether the remote listener hears us.
// A silence-suppressing peer can stop sending RTP; keep that possibility visible
// rather than treating every quiet interval as a broken call or hanging it up.
// The caller holds c.mu; this reads snapshots and never waits on media workers.
func (c *Call) diagnoseMedia(result *Stats, transport rtp.Stats, now time.Time) {
	result.MediaFlow = "waiting"
	if c.closed {
		result.MediaFlow = "closed"
		return
	}
	if !c.connected && !c.early {
		return
	}
	if result.Held {
		result.MediaFlow = "held"
		return
	}
	if result.AudioStopped || result.AudioRecovering {
		result.MediaFlow = "device-unavailable"
		result.MediaWarning = "Audio device is unavailable; reconnect it or select another device."
		return
	}
	if c.secure && !result.Encrypted {
		result.MediaFlow = "securing"
		return
	}
	send, receive := c.sendEnabled.Load() && !c.early, c.receiveEnabled.Load()
	if !send && !receive {
		result.MediaFlow = "inactive"
		return
	}
	lastSent, lastReceived := transport.LastSent, transport.LastReceived
	if lastSent.Before(c.mediaStarted) {
		lastSent = c.mediaStarted
	}
	if lastReceived.Before(c.mediaStarted) {
		lastReceived = c.mediaStarted
	}
	if send {
		result.SendIdleSeconds = max(0, now.Sub(lastSent).Seconds())
	}
	if receive {
		result.ReceiveIdleSeconds = max(0, now.Sub(lastReceived).Seconds())
	}
	if now.Sub(c.mediaStarted) < 5*time.Second {
		return
	}
	missingSend := send && result.SendIdleSeconds >= 5
	missingReceive := receive && result.ReceiveIdleSeconds >= 5
	switch {
	case missingSend && missingReceive:
		result.MediaFlow = "no-media"
		result.MediaWarning = "No RTP packets are flowing. Check audio devices, the media address, firewall and ICE status."
	case missingSend:
		result.MediaFlow = "missing-outbound"
		result.MediaWarning = "No outgoing RTP packets. Check the audio device and the last media error."
	case missingReceive:
		result.MediaFlow = "missing-inbound"
		result.MediaWarning = "No incoming RTP packets. The peer may be silent; if speech is missing, check the remote media address, firewall and ICE status."
	case send && receive:
		result.MediaFlow = "duplex"
	case send:
		result.MediaFlow = "sending"
	default:
		result.MediaFlow = "receiving"
	}
}
