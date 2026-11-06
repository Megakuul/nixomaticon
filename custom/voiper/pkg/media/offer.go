package media

import (
	"bytes"
	"errors"

	"github.com/megakuul/voiper/pkg/audio"

	"github.com/megakuul/voiper/pkg/codec"
)

// CurrentOffer starts a fresh offer using local hold state and the active transport.
func (c *Call) CurrentOffer(advertisedIP string) []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.restart != nil {
		return nil
	}
	c.version++
	direction := "sendrecv"
	if c.held.Load() {
		direction = "sendonly"
	}
	data := c.sdp(advertisedIP, direction, true)
	c.localOffer = append([]byte(nil), data...)
	return data
}

// ValidateAnswer performs no device, network, or negotiated-state changes.
func (c *Call) ValidateAnswer(answer []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.validateAnswer(c.localOffer, answer)
}
func (c *Call) validateAnswer(localOffer, answer []byte) error {
	if c.closed {
		return errors.New("media call is closed")
	}
	if len(localOffer) == 0 {
		return errors.New("no local media offer is pending")
	}
	remote, err := negotiate(answer, c.formats)
	if err != nil {
		return err
	}
	// The answer may give the selected codec its own payload number; the pipeline
	// sends with that number and still accepts the one from our offer.
	offered, err := negotiate(localOffer, []codec.Format{remote.format})
	if err != nil {
		return errors.New("answer selected a codec that was not offered")
	}
	// RFC 3264 section 6.1 lets an answer list further codecs, as SwyxIt! clients
	// do. Only the selected codec and telephone events are sent, so others are ignored.
	if c.deviceRate != 0 && remote.format.SampleRate != c.deviceRate && !audio.DSPAvailable() {
		return errors.New("codec rate conversion requires SpeexDSP")
	}
	if remote.fingerprint != "" {
		if offered.fingerprint == "" || remote.dtlsProfile != offered.dtlsProfile {
			return errors.New("answer changed the offered DTLS-SRTP profile")
		}
		if remote.setup == "actpass" || (offered.setup == "active" && remote.setup != "passive") || (offered.setup == "passive" && remote.setup != "active") {
			return errors.New("answer has an incompatible DTLS setup role")
		}
	}
	if err := c.validateDTLS(remote, false); err != nil {
		return err
	}
	if remote.secure != offered.secure || (remote.secure && remote.cryptoTag != offered.cryptoTag) {
		return errors.New("answer changed the offered media security")
	}
	if remote.mux && !offered.mux {
		return errors.New("answer selected RTCP multiplexing that was not offered")
	}
	if remote.ice.Username != "" && offered.ice.Username == "" {
		return errors.New("answer selected ICE that was not offered")
	}
	if c.settings.ICEPolicy == "required" && (remote.ice.Username == "" || !remote.mux) {
		return errors.New("peer must support ICE and RTCP multiplexing")
	}
	if remote.ice.Username != "" && !remote.mux && offered.ice.Username != "" {
		return errors.New("ICE requires RTCP multiplexing")
	}
	if (c.connected || c.early) && remote.ice.Lite != c.remote.ice.Lite {
		return errors.New("changing full/lite ICE mode during a call is unsupported")
	}
	if (c.connected || c.early) && c.iceDescription.Username != "" && (remote.ice.Username != c.remote.ice.Username || remote.ice.Password != c.remote.ice.Password) {
		if c.restart == nil || !bytes.Equal(localOffer, c.restart.offer) {
			return errors.New("ICE restart requires a replacement local offer")
		}
	}
	if (remote.send && !offered.receive) || (remote.receive && !offered.send) {
		return errors.New("answer direction is incompatible with the local offer")
	}
	// The answer may number telephone events differently; DTMF is sent with its
	// payload type, and later offers reuse it.
	if remote.hasTelephone && !offered.hasTelephone {
		return errors.New("answer selected telephone events that were not offered")
	}
	return nil
}

// AcceptAnswer commits an answer against the offer actually sent on the wire.
// Signaling must serialize this with subsequent offers for the same dialog.
func (c *Call) AcceptAnswer(localOffer, answer []byte) error {
	if len(localOffer) == 0 {
		return errors.New("local media offer is missing")
	}
	if handled, err := c.acceptRestart(localOffer, answer); handled {
		return err
	}
	return c.connect(answer, false, false, localOffer)
}
