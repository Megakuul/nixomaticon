package media

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/megakuul/voiper/pkg/audio"
	"github.com/megakuul/voiper/pkg/codec"
)

type mediaPipeline struct {
	encoderBitrate atomic.Int64
	ctx            context.Context
	cancel         context.CancelFunc
	workers        sync.WaitGroup
	coder          codec.Codec
	format         codec.Format
	// receivePayloadType is the number our offer gave the codec. RFC 3264 lets an
	// answer choose another number for the packets we send; the peer still sends ours.
	receivePayloadType                  uint8
	deviceRate                          int
	captureConverter, playbackConverter *audio.Resampler
}

func newPipeline(ctx context.Context, format codec.Format, receivePayloadType uint8, deviceRate int) (*mediaPipeline, error) {
	coder, err := codec.New(format)
	if err != nil {
		return nil, err
	}
	capture, err := audio.NewResampler(deviceRate, format.SampleRate)
	if err != nil {
		return nil, err
	}
	playback, err := audio.NewResampler(format.SampleRate, deviceRate)
	if err != nil {
		capture.Close()
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	return &mediaPipeline{ctx: ctx, cancel: cancel, coder: coder, format: format, receivePayloadType: receivePayloadType, deviceRate: deviceRate, captureConverter: capture, playbackConverter: playback}, nil
}
func (p *mediaPipeline) close() {
	p.cancel()
	p.workers.Wait()
	p.captureConverter.Close()
	p.playbackConverter.Close()
}
func (c *Call) startPipeline(p *mediaPipeline, capture, playback bool) {
	if playback {
		c.workers.Add(1)
		p.workers.Add(1)
		go c.playback(p)
	}
	if capture {
		c.workers.Add(1)
		p.workers.Add(1)
		go c.capture(p)
	}
}

// Called with the call lock held. Device callbacks keep their existing PCM rate.
func (c *Call) replacePipeline(remote description, next *mediaPipeline, capture bool, localHeld *bool) error {
	previous := c.pipeline
	c.connecting = true
	c.mu.Unlock()
	previous.close()
	c.mu.Lock()
	c.connecting = false
	if c.closed || c.ctx.Err() != nil {
		next.close()
		return errors.New("call closed while changing codec")
	}
	if err := c.applyRemote(remote, localHeld); err != nil {
		next.close()
		return err
	}
	// Discard queued packets from the previous payload mapping before the new decoder starts.
drain:
	for range 128 {
		select {
		case _, ok := <-c.session.Packets():
			if !ok {
				break drain
			}
		default:
			break drain
		}
	}
	c.pipeline = next
	c.startPipeline(next, capture, true)
	return nil
}
func (c *Call) applyRemote(remote description, localHeld *bool) error {
	if remote.secure && remote.fingerprint == "" {
		if err := c.session.ConfigureSRTP(c.localKey, remote.key); err != nil {
			return err
		}
	}
	if remote.mux {
		remote.control = remote.address
	}
	if err := c.session.SetRemote(remote.address, remote.control, remote.format.ClockRate); err != nil {
		return err
	}
	c.session.SetRTCPMux(remote.mux)
	if c.mediaStarted.IsZero() || c.remote.send != remote.send || c.remote.receive != remote.receive || c.remote.format != remote.format || (localHeld != nil && *localHeld != c.held.Load()) {
		c.mediaStarted = time.Now()
	}
	c.remote = remote
	if localHeld != nil {
		c.held.Store(*localHeld)
	}
	c.selected = true
	if remote.secure {
		c.cryptoTag = remote.cryptoTag
	}
	c.sendEnabled.Store(remote.send)
	c.receiveEnabled.Store(remote.receive)
	return nil
}
