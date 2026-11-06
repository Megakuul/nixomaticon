//go:build opus

package codec

import "testing"

func TestOpusLossAdaptationHonorsNegotiatedCap(t *testing.T) {
	for _, cap := range []int{0, 6000, 16000, 64000} {
		format := Format{Name: "opus", SampleRate: 48000, ClockRate: 48000, Channels: 2, Bitrate: cap, UseFEC: true}
		coder, err := New(format)
		if err != nil {
			t.Fatal(err)
		}
		adaptive := coder.(LossAdaptiveEncoder)
		maximum := 32000
		if cap > 0 {
			maximum = min(maximum, cap)
		}
		if adaptive.EncoderBitrate() != maximum {
			t.Fatalf("initial bitrate %d, cap %d", adaptive.EncoderBitrate(), cap)
		}
		for range 30 {
			bitrate, err := adaptive.SetPacketLoss(20)
			if err != nil || bitrate < min(12000, maximum) || bitrate > maximum {
				t.Fatalf("loss update %d: %v", bitrate, err)
			}
		}
		for range 100 {
			if _, err = adaptive.SetPacketLoss(0); err != nil {
				t.Fatal(err)
			}
		}
		if adaptive.EncoderBitrate() != maximum {
			t.Fatalf("recovery bitrate %d, want %d", adaptive.EncoderBitrate(), maximum)
		}
		if _, err = adaptive.SetPacketLoss(101); err == nil {
			t.Fatal("accepted invalid loss percentage")
		}
		packet, err := coder.Encode(make([]int16, 960))
		if err != nil {
			t.Fatal(err)
		}
		pcm, err := coder.Decode(packet)
		if err != nil || len(pcm) != 960 {
			t.Fatalf("encode/decode after adaptation: %d samples, %v", len(pcm), err)
		}
	}
}
