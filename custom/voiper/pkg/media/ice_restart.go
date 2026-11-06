package media

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/megakuul/voiper/pkg/rtp"
)

type iceRestart struct {
	transport *rtp.ICERestart
	ctx       context.Context
	cancel    context.CancelFunc
	offer     []byte
	answer    chan description
	started   bool
}

// RestartOffer gathers replacement ICE candidates while the existing media path continues.
// Signaling must call CancelRestart with this offer when its SIP negotiation fails.
func (c *Call) RestartOffer(ctx context.Context, advertisedIP string) ([]byte, error) {
	c.mu.Lock()
	if c.restart != nil {
		if c.restart.ctx.Err() != nil {
			c.mu.Unlock()
			return nil, errors.New("ICE restart cancellation is finishing")
		}
		offer := append([]byte(nil), c.restart.offer...)
		c.mu.Unlock()
		if len(offer) == 0 {
			return nil, errors.New("ICE restart is already in progress")
		}
		return offer, nil
	}
	c.mu.Unlock()
	return c.prepareRestart(ctx, advertisedIP, nil)
}

func (c *Call) prepareRestart(ctx context.Context, advertisedIP string, remote *description) ([]byte, error) {
	c.mu.Lock()
	if c.closed || !c.connected || c.connecting || c.restart != nil || c.iceDescription.Username == "" {
		c.mu.Unlock()
		return nil, errors.New("ICE restart requires a connected ICE call without a pending negotiation")
	}
	if remote != nil {
		if err := c.validateRestart(*remote); err != nil {
			c.mu.Unlock()
			return nil, err
		}
	}
	restartCtx, cancel := context.WithTimeout(c.ctx, 30*time.Second)
	pending := &iceRestart{ctx: restartCtx, cancel: cancel, answer: make(chan description, 1)}
	c.restart = pending
	c.operations.Add(1)
	c.mu.Unlock()
	// The caller may cancel gathering; after SDP is returned the call owns the checks.
	stop := context.AfterFunc(ctx, cancel)
	transport, err := c.session.PrepareICERestart(restartCtx, c.settings.ICEServers)
	stop()
	c.mu.Lock()
	if err != nil || c.closed || c.restart != pending || restartCtx.Err() != nil {
		if c.restart == pending {
			c.restart = nil
		}
		c.mu.Unlock()
		cancel()
		if transport != nil {
			transport.Cancel()
		}
		c.operations.Done()
		if err == nil {
			err = errors.New("ICE restart was cancelled")
		}
		return nil, err
	}
	pending.transport = transport
	original := c.iceDescription
	c.iceDescription = transport.Description()
	c.version++
	data := c.sdp(advertisedIP, "", remote == nil)
	c.iceDescription = original
	if remote == nil {
		pending.offer = append([]byte(nil), data...)
		c.localOffer = append([]byte(nil), data...)
	} else {
		pending.started = true
		pending.answer <- *remote
	}
	c.mu.Unlock()
	go c.finishRestart(pending)
	return data, nil
}

func (c *Call) validateRestart(remote description) error {
	if err := c.validateDTLS(remote, true); err != nil {
		return err
	}
	if remote.ice.Username == "" || remote.ice.Password == "" || !remote.mux ||
		remote.ice.Username == c.remote.ice.Username || remote.ice.Password == c.remote.ice.Password {
		return errors.New("ICE restart must replace both credentials and retain RTCP multiplexing")
	}
	if remote.fingerprint != c.remote.fingerprint || remote.dtlsProfile != c.remote.dtlsProfile || remote.ice.Lite != c.remote.ice.Lite || remote.format != c.remote.format || remote.secure != c.remote.secure || remote.cryptoTag != c.remote.cryptoTag ||
		!bytes.Equal(remote.key, c.remote.key) || remote.send != c.remote.send || remote.receive != c.remote.receive ||
		remote.telephone != c.remote.telephone || remote.hasTelephone != c.remote.hasTelephone || remote.events != c.remote.events {
		return errors.New("ICE restart cannot also change codecs, media security, direction or telephone events")
	}
	return nil
}

func (c *Call) acceptRestart(offer, answer []byte) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	pending := c.restart
	if pending == nil {
		return false, nil
	}
	if len(pending.offer) == 0 || !bytes.Equal(offer, pending.offer) || pending.started {
		return true, errors.New("another ICE restart is in progress")
	}
	if err := c.validateAnswer(offer, answer); err != nil {
		return true, err
	}
	remote, err := negotiate(answer, c.formats)
	if err != nil {
		return true, err
	}
	if err = c.validateRestart(remote); err != nil {
		return true, err
	}
	pending.started = true
	pending.answer <- remote
	return true, nil
}

func (c *Call) finishRestart(pending *iceRestart) {
	defer c.operations.Done()
	defer pending.cancel()
	var remote description
	var err error
	select {
	case remote = <-pending.answer:
		err = pending.transport.Connect(remote.ice)
	case <-pending.ctx.Done():
		err = pending.ctx.Err()
	}
	c.mu.Lock()
	if c.restart == pending {
		c.restart = nil
		if bytes.Equal(c.localOffer, pending.offer) {
			c.localOffer = nil
		}
		if err == nil {
			c.remote = remote
			c.iceDescription = pending.transport.Description()
			c.lastError = ""
		} else if !c.closed {
			c.lastError = fmt.Sprintf("ICE restart failed; previous media path retained: %v", err)
		}
	}
	c.mu.Unlock()
	pending.transport.Cancel()
}

// CancelRestart cancels only the outstanding negotiation that produced offer.
func (c *Call) CancelRestart(offer []byte) {
	c.mu.Lock()
	pending := c.restart
	if pending == nil || len(offer) == 0 || !bytes.Equal(pending.offer, offer) {
		c.mu.Unlock()
		return
	}
	if bytes.Equal(c.localOffer, offer) {
		c.localOffer = nil
	}
	pending.cancel()
	c.mu.Unlock()
	pending.transport.Cancel()
}
