//go:build speex

package audio

/*
#cgo pkg-config: speexdsp
#include <speex/speex_echo.h>
#include <speex/speex_preprocess.h>
*/
import "C"

import (
	"errors"
	"unsafe"
)

func DSPAvailable() bool { return true }

type nativeProcessor struct {
	echo       *C.SpeexEchoState
	preprocess *C.SpeexPreprocessState
	output     []int16
}

func newNativeProcessor(rate int, echo, noise bool) (*nativeProcessor, error) {
	frame := rate / 50
	p := &nativeProcessor{output: make([]int16, frame)}
	p.preprocess = C.speex_preprocess_state_init(C.int(frame), C.int(rate))
	if p.preprocess == nil {
		return nil, errors.New("initialize SpeexDSP preprocessor")
	}
	denoise := C.int(0)
	if noise {
		denoise = 1
	}
	suppression := C.int(-20)
	if C.speex_preprocess_ctl(p.preprocess, C.SPEEX_PREPROCESS_SET_DENOISE, unsafe.Pointer(&denoise)) != 0 || C.speex_preprocess_ctl(p.preprocess, C.SPEEX_PREPROCESS_SET_NOISE_SUPPRESS, unsafe.Pointer(&suppression)) != 0 {
		p.close()
		return nil, errors.New("configure SpeexDSP noise suppression")
	}
	if echo {
		// A 160 ms filter accommodates typical desktop buffering and room echoes.
		p.echo = C.speex_echo_state_init(C.int(frame), C.int(rate*160/1000))
		if p.echo == nil {
			p.close()
			return nil, errors.New("initialize SpeexDSP echo canceller")
		}
		sampleRate := C.int(rate)
		if C.speex_echo_ctl(p.echo, C.SPEEX_ECHO_SET_SAMPLING_RATE, unsafe.Pointer(&sampleRate)) != 0 || C.speex_preprocess_ctl(p.preprocess, C.SPEEX_PREPROCESS_SET_ECHO_STATE, unsafe.Pointer(p.echo)) != 0 {
			p.close()
			return nil, errors.New("configure SpeexDSP echo canceller")
		}
	}
	return p, nil
}
func (p *nativeProcessor) process(capture, reference []int16) {
	if p.echo != nil {
		C.speex_echo_cancellation(p.echo, (*C.spx_int16_t)(unsafe.Pointer(&capture[0])), (*C.spx_int16_t)(unsafe.Pointer(&reference[0])), (*C.spx_int16_t)(unsafe.Pointer(&p.output[0])))
		copy(capture, p.output)
	}
	C.speex_preprocess_run(p.preprocess, (*C.spx_int16_t)(unsafe.Pointer(&capture[0])))
}
func (p *nativeProcessor) close() {
	if p.preprocess != nil {
		C.speex_preprocess_state_destroy(p.preprocess)
		p.preprocess = nil
	}
	if p.echo != nil {
		C.speex_echo_state_destroy(p.echo)
		p.echo = nil
	}
}

func (p *nativeProcessor) reset() {
	if p.echo != nil {
		C.speex_echo_state_reset(p.echo)
	}
}
