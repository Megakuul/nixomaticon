package audio

import (
	"context"
	"encoding/binary"
	"errors"
	"math"
	"time"
)

type TestResult struct {
	Backend                            string
	InputPeak, OutputPeak              float64
	CaptureOverruns, PlaybackUnderruns uint64
}

// Test runs an explicit, bounded device check. Microphone mode measures input;
// speaker mode emits a quiet tone; loopback mode routes input to the output.
func Test(ctx context.Context, settings Settings, mode string, duration time.Duration) (TestResult, error) {
	if duration <= 0 || duration > 10*time.Second {
		return TestResult{}, errors.New("audio test duration must be between zero and ten seconds")
	}
	var opener func(Settings, int) (*Stream, error)
	switch mode {
	case "microphone":
		opener = OpenCapture
	case "speaker":
		opener = OpenPlayback
	case "loopback":
		opener = Open
	default:
		return TestResult{}, errors.New("audio test mode must be microphone, speaker or loopback")
	}
	stream, closeDevice, err := openDiagnostic(ctx, func() (*Stream, error) { return opener(settings, 48000) })
	if err != nil {
		return TestResult{}, err
	}
	defer closeDevice()
	ctx, cancel := context.WithTimeout(ctx, duration)
	defer cancel()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	frame := make([]byte, 1920)
	phase := 0
	result := TestResult{Backend: stream.Backend}
	for {
		select {
		case <-ctx.Done():
			return result, nil
		case <-ticker.C:
			switch mode {
			case "speaker":
				for i := 0; i < len(frame); i += 2 {
					sample := int16(3000 * math.Sin(2*math.Pi*440*float64(phase)/48000))
					binary.LittleEndian.PutUint16(frame[i:], uint16(sample))
					phase++
				}
				stream.Playback.Write(frame)
			case "microphone", "loopback":
				n := stream.Capture.Read(frame)
				if mode == "loopback" {
					stream.Playback.Write(frame[:n])
				}
			}
			stats := stream.Stats()
			result.InputPeak = max(result.InputPeak, stats.InputLevel)
			result.OutputPeak = max(result.OutputPeak, stats.OutputLevel)
			result.CaptureOverruns = stats.CaptureOverruns
			result.PlaybackUnderruns = stats.PlaybackUnderruns
		}
	}
}

// Ring plays a bounded-cadence incoming-call tone until its owner stops it.
func StartRinging(ctx context.Context, settings Settings) (func(), error) {
	return startTone(ctx, settings, 48000, 48000*3)
}
func StartRingback(ctx context.Context, settings Settings) (func(), error) {
	return startTone(ctx, settings, 48000*2, 48000*6)
}
func startTone(ctx context.Context, settings Settings, onSamples, periodSamples int) (func(), error) {
	stream, closeDevice, err := openDiagnostic(ctx, func() (*Stream, error) { return OpenPlayback(settings, 48000) })
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer closeDevice()
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		frame := make([]byte, 1920)
		sampleIndex := 0
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				for i := 0; i < len(frame); i += 2 {
					value := 0.0
					if sampleIndex%periodSamples < onSamples {
						value = 2200 * (math.Sin(2*math.Pi*440*float64(sampleIndex)/48000) + math.Sin(2*math.Pi*480*float64(sampleIndex)/48000))
					}
					binary.LittleEndian.PutUint16(frame[i:], uint16(int16(value)))
					sampleIndex++
				}
				stream.Playback.Write(frame)
			}
		}
	}()
	return func() { cancel(); <-done }, nil
}
