//go:build speex

package media

import (
	"math"
	"testing"
)

func TestConferenceResamplesDifferentDevices(t *testing.T) {
	rates := []int{8000, 16000, 48000}
	calls := make([]*Call, len(rates))
	for i, rate := range rates {
		calls[i] = &Call{connected: true, deviceRate: rate}
		defer calls[i].LeaveConference()
	}
	if err := JoinConference(calls...); err != nil {
		t.Fatal(err)
	}
	// Only the 8 kHz leg supplies a tone. Other legs hear it at their own rate;
	// its source hears no copy of itself.
	for frame := 0; frame < 10; frame++ {
		source := make([]int16, rates[0]/50)
		for i := range source {
			source[i] = int16(4000 * math.Sin(2*math.Pi*440*float64(frame*len(source)+i)/float64(rates[0])))
		}
		if err := calls[0].conference.Load().forward(source); err != nil {
			t.Fatal(err)
		}
		for i, call := range calls {
			received := make([]int16, rates[i]/50)
			call.conference.Load().mix(received)
			var energy int64
			for _, sample := range received {
				energy += int64(sample) * int64(sample)
			}
			if i == 0 && energy != 0 {
				t.Fatal("conference reflected source audio")
			}
			if i > 0 && frame > 1 && energy < int64(len(received))*1000000 {
				t.Fatalf("leg %d did not receive resampled speech", i)
			}
		}
	}
}
