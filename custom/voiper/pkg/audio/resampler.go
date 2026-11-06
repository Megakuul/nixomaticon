package audio

import "errors"

// Resampler preserves filter state across chunks and is owned by one audio worker.
type Resampler struct {
	inputRate, outputRate int
	native                *nativeResampler
	closed                bool
}

func NewResampler(inputRate, outputRate int) (*Resampler, error) {
	if inputRate < 8000 || inputRate > 48000 || outputRate < 8000 || outputRate > 48000 {
		return nil, errors.New("resampling rates must be between 8 and 48 kHz")
	}
	r := &Resampler{inputRate: inputRate, outputRate: outputRate}
	if inputRate == outputRate {
		return r, nil
	}
	native, err := newNativeResampler(inputRate, outputRate)
	if err != nil {
		return nil, err
	}
	r.native = native
	return r, nil
}
func (r *Resampler) Process(pcm []int16) ([]int16, error) {
	if r.closed {
		return nil, errors.New("resampler is closed")
	}
	if len(pcm) > r.inputRate*120/1000 {
		return nil, errors.New("resampler chunk exceeds 120 ms")
	}
	if r.native == nil || len(pcm) == 0 {
		return pcm, nil
	}
	return r.native.process(pcm, r.inputRate, r.outputRate)
}
func (r *Resampler) LatencySamples() int {
	if r.native == nil {
		return 0
	}
	return r.native.latency()
}
func (r *Resampler) Close() {
	if r.native != nil {
		r.native.close()
		r.native = nil
	}
	r.closed = true
}
