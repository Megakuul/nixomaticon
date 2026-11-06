package codec

import (
	"math"
	"testing"
)

func TestG711SilenceVectors(t *testing.T) {
	for _, test := range []struct {
		name    string
		encoded byte
		decoded int16
	}{{"PCMA", 0xd5, 8}, {"PCMU", 0xff, 0}} {
		c, err := New(Format{Name: test.name})
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := c.Encode([]int16{0})
		if err != nil || encoded[0] != test.encoded {
			t.Fatalf("%s encoding: %x %v", test.name, encoded, err)
		}
		decoded, err := c.Decode(encoded)
		if err != nil || decoded[0] != test.decoded {
			t.Fatalf("%s decoding: %v %v", test.name, decoded, err)
		}
	}
}
func TestCodecsRoundTripSpeechFrame(t *testing.T) {
	for _, format := range Available() {
		t.Run(format.Name, func(t *testing.T) {
			c, err := New(format)
			if err != nil {
				t.Fatal(err)
			}
			pcm := make([]int16, format.SampleRate/50)
			for i := range pcm {
				pcm[i] = int16(10000 * math.Sin(2*math.Pi*440*float64(i)/float64(format.SampleRate)))
			}
			encoded, err := c.Encode(pcm)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := c.Decode(encoded)
			if err != nil {
				t.Fatal(err)
			}
			if len(decoded) != len(pcm) {
				t.Fatalf("samples %d, want %d", len(decoded), len(pcm))
			}
			energy := int64(0)
			for _, sample := range decoded {
				energy += int64(sample) * int64(sample)
			}
			if energy == 0 {
				t.Fatal("decoded silence from tone")
			}
		})
	}
}
func TestG722ClockIsDifferentFromPCM(t *testing.T) {
	for _, format := range Available() {
		if format.Name == "G722" && (format.SampleRate != 16000 || format.ClockRate != 8000) {
			t.Fatalf("format: %+v", format)
		}
	}
}
func TestRejectOversizedFrames(t *testing.T) {
	for _, name := range []string{"PCMA", "PCMU", "G722"} {
		c, _ := New(Format{Name: name})
		if _, err := c.Decode(make([]byte, 961)); err == nil {
			t.Fatalf("%s accepted oversized frame", name)
		}
	}
}

func TestPacketLossConcealmentIsOneSpeechFrame(t *testing.T) {
	for _, format := range Available() {
		t.Run(format.Name, func(t *testing.T) {
			coder, err := New(format)
			if err != nil {
				t.Fatal(err)
			}
			samples := format.SampleRate / 50
			payload, err := coder.Encode(make([]int16, samples))
			if err != nil {
				t.Fatal(err)
			}
			if _, err = coder.Decode(payload); err != nil {
				t.Fatal(err)
			}
			pcm, err := coder.Conceal(samples, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(pcm) != samples {
				t.Fatalf("concealed %d samples, want %d", len(pcm), samples)
			}
		})
	}
}
