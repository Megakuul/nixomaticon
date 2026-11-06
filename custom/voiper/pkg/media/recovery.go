package media

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/megakuul/voiper/pkg/audio"
)

type audioDevices struct{ capture, playback *audio.Stream }

func (d *audioDevices) stopped() bool {
	return d != nil && ((d.capture != nil && d.capture.Stats().Stopped) || (d.playback != nil && d.playback.Stats().Stopped))
}
func (d *audioDevices) close() {
	if d.capture != nil && d.capture != d.playback {
		d.capture.Close()
	}
	if d.playback != nil {
		d.playback.Close()
	}
}

func (c *Call) startRecovery() {
	if c.settings.DisableAutoRecovery || c.recoveryStarted {
		return
	}
	c.recoveryStarted = true
	c.workers.Add(1)
	go c.recoverAudio()
}

// RetryAudio explicitly restarts a failed bounded recovery cycle.
func (c *Call) RetryAudio() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || (!c.connected && !c.early) {
		return errors.New("call has no audio devices to recover")
	}
	if c.nativeOpenPending.Load() {
		return errors.New("a previous native audio open is still finishing")
	}
	if !c.recoveryStarted {
		c.recoveryStarted = true
		c.workers.Add(1)
		go c.recoverAudio()
	}
	select {
	case c.recoveryWake <- struct{}{}:
	default:
	}
	return nil
}

func (c *Call) recoverAudio() {
	defer c.workers.Done()
	ticker := time.NewTicker(c.recoveryInterval)
	defer ticker.Stop()
	exhausted := false
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-c.recoveryWake:
			exhausted = false
		case <-ticker.C:
		}
		old := c.devices.Load()
		if old == nil || !old.stopped() || exhausted {
			continue
		}
		for attempt := 0; attempt < 5; attempt++ {
			if attempt > 0 {
				timer := time.NewTimer(time.Duration(1<<(attempt-1)) * c.recoveryInterval)
				select {
				case <-c.ctx.Done():
					timer.Stop()
					return
				case <-timer.C:
				}
			}
			c.mu.Lock()
			if c.closed {
				c.mu.Unlock()
				return
			}
			if c.connecting {
				c.mu.Unlock()
				break
			}
			c.audioRecovering = true
			c.audioRecoveryAttempts++
			settings := audio.Settings{InputDevice: c.settings.InputDevice, OutputDevice: c.settings.OutputDevice, Backend: c.settings.Backend}
			if (settings.Backend == "" || settings.Backend == "auto") && old.playback.Backend != "" {
				settings.Backend = old.playback.Backend
			}
			rate := c.deviceRate
			openDuplex, openCapture, openPlayback := c.openAudio, c.openCapture, c.openPlayback
			c.mu.Unlock()
			replacement, err := c.reopenDevices(func() (*audioDevices, error) {
				if err := c.closeDevices(old); err != nil {
					return nil, err
				}
				if old.capture == old.playback {
					stream, err := openDuplex(settings, rate)
					if err != nil {
						return nil, err
					}
					return &audioDevices{capture: stream, playback: stream}, nil
				}
				playback, err := openPlayback(settings, rate)
				if err != nil {
					return nil, err
				}
				next := &audioDevices{playback: playback}
				if old.capture != nil {
					next.capture, err = openCapture(settings, rate)
					if err != nil {
						c.closeDevices(next)
						return nil, err
					}
				}
				return next, nil
			})
			c.mu.Lock()
			c.audioRecovering = false
			if c.closed {
				c.mu.Unlock()
				if replacement != nil {
					c.disposeDevices(replacement)
				}
				return
			}
			if err != nil {
				c.audioRecoveryError = err.Error()
				exhausted = attempt == 4 || errors.Is(err, context.DeadlineExceeded)
				if exhausted {
					c.audioRecoveryError = "automatic recovery stopped: " + err.Error()
				}
				c.mu.Unlock()
				if exhausted {
					break
				}
				continue
			}
			// A final answer may have replaced an early-media device while it reopened.
			if c.devices.Load() != old {
				c.mu.Unlock()
				c.disposeDevices(replacement)
				break
			}
			c.stream = replacement.playback
			c.captureStream = replacement.capture
			c.devices.Store(replacement)
			c.audioRecoveryError = ""
			c.mu.Unlock()
			break
		}
	}
}

func (c *Call) reopenDevices(open func() (*audioDevices, error)) (*audioDevices, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, errors.New("media call is closed")
	}
	if !c.nativeOpenPending.CompareAndSwap(false, true) {
		c.mu.Unlock()
		return nil, errors.New("a native audio open is already pending")
	}
	c.operations.Add(1)
	c.mu.Unlock()
	ctx, cancel := context.WithTimeout(c.ctx, c.recoveryTimeout)
	defer cancel()
	type outcome struct {
		devices *audioDevices
		err     error
	}
	result := make(chan outcome)
	go func() {
		defer c.operations.Done()
		devices, err := open()
		select {
		case result <- outcome{devices, err}:
		case <-ctx.Done():
			cleanupErr := err
			if devices != nil {
				cleanupErr = errors.Join(cleanupErr, c.closeDevices(devices))
			}
			if cleanupErr != nil {
				c.mu.Lock()
				c.closeErr = errors.Join(c.closeErr, cleanupErr)
				c.mu.Unlock()
			}
			c.nativeOpenPending.Store(false)
		}
	}()
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("open audio devices: %w", ctx.Err())
	case opened := <-result:
		c.nativeOpenPending.Store(false)
		return opened.devices, opened.err
	}
}
