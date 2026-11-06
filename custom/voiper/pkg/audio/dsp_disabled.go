//go:build !speex

package audio

import "errors"

func DSPAvailable() bool { return false }

type nativeProcessor struct{}

func newNativeProcessor(_ int, _, _ bool) (*nativeProcessor, error) {
	return nil, errors.New("audio DSP requires a build with -tags speex and libspeexdsp")
}
func (*nativeProcessor) process([]int16, []int16) { panic("unavailable DSP processor") }
func (*nativeProcessor) close()                   {}

func (*nativeProcessor) reset() {}
