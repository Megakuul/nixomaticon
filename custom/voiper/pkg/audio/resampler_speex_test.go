//go:build speex

package audio

import (
	"fmt"
	"math"
	"testing"
)

func TestResamplerRateAndBoundedLatency(t *testing.T) {
	for _, rates := range [][2]int{{8000, 48000}, {48000, 8000}, {16000, 48000}, {48000, 16000}} {
		t.Run(fmt.Sprintf("%d_to_%d", rates[0], rates[1]), func(t *testing.T) {
			resampler, err := NewResampler(rates[0], rates[1])
			if err != nil {
				t.Fatal(err)
			}
			defer resampler.Close()
			frame := make([]int16, rates[0]/50)
			total, peakIndex := 0, -1
			var maximum int16
			for n := 0; n < 20; n++ {
				clear(frame)
				if n == 0 {
					frame[0] = 20000
				}
				out, err := resampler.Process(frame)
				if err != nil {
					t.Fatal(err)
				}
				for i, sample := range out {
					if sample > maximum {
						maximum = sample
						peakIndex = total + i
					}
				}
				total += len(out)
			}
			if total != rates[1]*400/1000 {
				t.Fatalf("output %d samples, want %d", total, rates[1]*400/1000)
			}
			if math.Abs(float64(peakIndex-resampler.LatencySamples())) > 1 {
				t.Fatalf("impulse delay %d, reported %d", peakIndex, resampler.LatencySamples())
			}
			if resampler.LatencySamples() > rates[1]/50 {
				t.Fatalf("resampling latency exceeds 20 ms")
			}
		})
	}
}
