//go:build speex

package audio

import (
	"math"
	"math/rand/v2"
	"testing"
)

func TestEchoCancellationAttenuatesDelayedPlayback(t *testing.T) {
	const rate = 16000
	const frames = 250
	const frameSize = rate / 50
	processor, err := NewProcessor(rate, true, false)
	if err != nil {
		t.Fatal(err)
	}
	defer processor.Close()
	random := rand.New(rand.NewPCG(1, 2))
	playback := make([]int16, frames*frameSize)
	for i := range playback {
		playback[i] = int16(random.IntN(14001) - 7000)
	}
	capture := make([]int16, frameSize)
	var before, after float64
	for frame := 0; frame < frames; frame++ {
		for i := range capture {
			index := frame*frameSize + i - 2*frameSize
			if index >= 0 {
				capture[i] = int16(float64(playback[index]) * .6)
			} else {
				capture[i] = 0
			}
		}
		if frame > frames-50 {
			for _, sample := range capture {
				before += float64(sample) * float64(sample)
			}
		}
		if err = processor.Process(capture, playback[frame*frameSize:(frame+1)*frameSize]); err != nil {
			t.Fatal(err)
		}
		if frame > frames-50 {
			for _, sample := range capture {
				after += float64(sample) * float64(sample)
			}
		}
	}
	attenuation := 10 * math.Log10(before/max(after, 1))
	if attenuation < 15 {
		t.Fatalf("echo attenuation %.1f dB, want at least 15 dB", attenuation)
	}
	t.Logf("40 ms synthetic echo attenuation: %.1f dB", attenuation)
}

func TestNoiseSuppressionReducesStationaryNoise(t *testing.T) {
	const rate = 16000
	processor, err := NewProcessor(rate, false, true)
	if err != nil {
		t.Fatal(err)
	}
	defer processor.Close()
	random := rand.New(rand.NewPCG(3, 4))
	frame := make([]int16, rate/50)
	var before, after float64
	for n := 0; n < 200; n++ {
		for i := range frame {
			frame[i] = int16(random.IntN(1201) - 600)
		}
		if n > 150 {
			for _, sample := range frame {
				before += float64(sample) * float64(sample)
			}
		}
		if err = processor.Process(frame, nil); err != nil {
			t.Fatal(err)
		}
		if n > 150 {
			for _, sample := range frame {
				after += float64(sample) * float64(sample)
			}
		}
	}
	attenuation := 10 * math.Log10(before/max(after, 1))
	if attenuation < 6 {
		t.Fatalf("noise attenuation %.1f dB, want at least 6 dB", attenuation)
	}
	t.Logf("stationary noise attenuation: %.1f dB", attenuation)
}

func TestDSPDoesNotAccumulateFrameLatency(t *testing.T) {
	processor, err := NewProcessor(8000, false, false)
	if err != nil {
		t.Fatal(err)
	}
	defer processor.Close()
	frame := make([]int16, 160)
	frame[0] = 1234
	if err = processor.Process(frame, nil); err != nil {
		t.Fatal(err)
	}
	if frame[0] != 1234 {
		t.Fatal("disabled processing added buffering or changed input")
	}
	if err = processor.Process(frame[:100], nil); err == nil {
		t.Fatal("accepted partial DSP frame")
	}
}
