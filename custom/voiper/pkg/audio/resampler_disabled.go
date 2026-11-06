//go:build !speex

package audio

import "errors"

type nativeResampler struct{}

func newNativeResampler(_, _ int) (*nativeResampler, error) {
	return nil, errors.New("mixed-rate audio requires a build with -tags speex and libspeexdsp")
}
func (*nativeResampler) process([]int16, int, int) ([]int16, error) {
	return nil, errors.New("native resampler unavailable")
}
func (*nativeResampler) latency() int { return 0 }
func (*nativeResampler) close()       {}
