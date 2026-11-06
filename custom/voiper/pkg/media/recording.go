package media

import (
	"errors"

	"github.com/megakuul/voiper/pkg/audio"
)

func (c *Call) StartRecording(path string) error {
	c.mu.Lock()
	ready := c.connected && !c.closed
	rate := c.deviceRate
	c.mu.Unlock()
	if !ready {
		return errors.New("connect the call before recording")
	}
	if c.recording.Load() != nil {
		return errors.New("call is already recording")
	}
	recording, err := audio.NewRecording(path, rate)
	if err != nil {
		return err
	}
	c.mu.Lock()
	if c.closed || c.deviceRate != rate || !c.recording.CompareAndSwap(nil, recording) {
		c.mu.Unlock()
		recording.Stop()
		return errors.New("call ended or another recording started")
	}
	c.mu.Unlock()
	go func() {
		<-recording.Done()
		if err := recording.Err(); err != nil {
			c.setError(err)
		}
		c.recording.CompareAndSwap(recording, nil)
	}()
	return nil
}
func (c *Call) StopRecording() error {
	recording := c.recording.Swap(nil)
	if recording == nil {
		return nil
	}
	return recording.Stop()
}
