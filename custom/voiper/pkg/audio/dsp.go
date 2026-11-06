package audio

import "errors"

// Processor is owned by one capture worker. Process uses the actual playback
// reference from the device callback; callers close it after that worker stops.
type Processor struct {
	native                             *nativeProcessor
	frameSize                          int
	EchoCancellation, NoiseSuppression bool
}

func NewProcessor(rate int, echoCancellation, noiseSuppression bool) (*Processor, error) {
	if rate != 8000 && rate != 16000 && rate != 48000 {
		return nil, errors.New("DSP supports 8, 16 and 48 kHz mono audio")
	}
	p := &Processor{frameSize: rate / 50, EchoCancellation: echoCancellation, NoiseSuppression: noiseSuppression}
	if !echoCancellation && !noiseSuppression {
		return p, nil
	}
	native, err := newNativeProcessor(rate, echoCancellation, noiseSuppression)
	if err != nil {
		return nil, err
	}
	p.native = native
	return p, nil
}
func (p *Processor) Process(capture, reference []int16) error {
	if len(capture) != p.frameSize || (p.EchoCancellation && len(reference) != p.frameSize) {
		return errors.New("DSP requires one 20 ms mono capture and playback frame")
	}
	if p.native == nil {
		if p.EchoCancellation || p.NoiseSuppression {
			return errors.New("DSP processor is closed")
		}
		return nil
	}
	p.native.process(capture, reference)
	return nil
}
func (p *Processor) Close() {
	if p.native != nil {
		p.native.close()
		p.native = nil
	}
}

func (p *Processor) Reset() {
	if p.native != nil {
		p.native.reset()
	}
}
