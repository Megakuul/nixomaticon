//go:build speex

package audio

/*
#cgo pkg-config: speexdsp
#include <speex/speex_resampler.h>
*/
import "C"

import (
	"fmt"
	"unsafe"
)

type nativeResampler struct{ state *C.SpeexResamplerState }

func newNativeResampler(inputRate, outputRate int) (*nativeResampler, error) {
	var code C.int
	state := C.speex_resampler_init(1, C.spx_uint32_t(inputRate), C.spx_uint32_t(outputRate), 5, &code)
	if state == nil || code != C.RESAMPLER_ERR_SUCCESS {
		if state != nil {
			C.speex_resampler_destroy(state)
		}
		return nil, fmt.Errorf("initialize Speex resampler: %s", C.GoString(C.speex_resampler_strerror(code)))
	}
	return &nativeResampler{state: state}, nil
}
func (r *nativeResampler) process(pcm []int16, inputRate, outputRate int) ([]int16, error) {
	output := make([]int16, (len(pcm)*outputRate+inputRate-1)/inputRate+r.latency()+16)
	inputCount, outputCount := C.spx_uint32_t(len(pcm)), C.spx_uint32_t(len(output))
	code := C.speex_resampler_process_int(r.state, 0, (*C.spx_int16_t)(unsafe.Pointer(&pcm[0])), &inputCount, (*C.spx_int16_t)(unsafe.Pointer(&output[0])), &outputCount)
	if code != C.RESAMPLER_ERR_SUCCESS {
		return nil, fmt.Errorf("resample audio: %s", C.GoString(C.speex_resampler_strerror(code)))
	}
	if int(inputCount) != len(pcm) {
		return nil, fmt.Errorf("resampler consumed %d of %d samples", inputCount, len(pcm))
	}
	return output[:int(outputCount)], nil
}
func (r *nativeResampler) latency() int { return int(C.speex_resampler_get_output_latency(r.state)) }
func (r *nativeResampler) close()       { C.speex_resampler_destroy(r.state) }
