// Package codec provides mono speech codecs with independent state per call.
package codec

import (
	"fmt"
	"strings"

	"github.com/gotranspile/g722"
	"github.com/zaf/g711"
)

type Format struct {
	Name                     string
	PayloadType              uint8
	SampleRate               int
	ClockRate                int
	Channels                 int
	Bitrate, MaxPlaybackRate int
	UseFEC                   bool
}

// Available is preference ordered. G.722 samples are 16 kHz but its RTP clock is 8 kHz.
func Available() []Format {
	formats := []Format{{Name: "G722", PayloadType: 9, SampleRate: 16000, ClockRate: 8000, Channels: 1}, {Name: "PCMA", PayloadType: 8, SampleRate: 8000, ClockRate: 8000, Channels: 1}, {Name: "PCMU", PayloadType: 0, SampleRate: 8000, ClockRate: 8000, Channels: 1}}
	if opusAvailable {
		formats = append([]Format{{Name: "opus", PayloadType: 111, SampleRate: 48000, ClockRate: 48000, Channels: 2}}, formats...)
	}
	return formats
}

// Codec operates on 20 ms mono PCM frames. Channels describes the SDP mapping;
// Opus uses the required opus/48000/2 mapping even for mono speech.
type Codec interface {
	Encode([]int16) ([]byte, error)
	Decode([]byte) ([]int16, error)
	Conceal(samples int, next []byte) ([]int16, error)
}

// LossAdaptiveEncoder is implemented by codecs with runtime loss controls.
// Call its methods only from the worker that owns Encode, once per fresh RTCP
// reporting interval. Decoder use on another worker remains independent.
type LossAdaptiveEncoder interface {
	EncoderBitrate() int
	SetPacketLoss(percent int) (int, error)
}

func New(format Format) (Codec, error) {
	switch strings.ToUpper(format.Name) {
	case "PCMA", "PCMU":
		return &g711Codec{alaw: strings.EqualFold(format.Name, "PCMA")}, nil
	case "G722":
		return &g722Codec{g722.NewEncoder(g722.Rate64000, 0), g722.NewDecoder(g722.Rate64000, 0)}, nil
	case "OPUS":
		return newOpus(format)
	default:
		return nil, fmt.Errorf("unsupported codec %q", format.Name)
	}
}

type g711Codec struct{ alaw bool }

func (c *g711Codec) Encode(pcm []int16) ([]byte, error) {
	out := make([]byte, len(pcm))
	for i, s := range pcm {
		if c.alaw {
			out[i] = g711.EncodeAlawFrame(s)
		} else {
			out[i] = g711.EncodeUlawFrame(s)
		}
	}
	return out, nil
}
func (c *g711Codec) Decode(data []byte) ([]int16, error) {
	if len(data) > 960 {
		return nil, fmt.Errorf("G.711 frame exceeds 120 ms")
	}
	out := make([]int16, len(data))
	for i, s := range data {
		if c.alaw {
			out[i] = g711.DecodeAlawFrame(s)
		} else {
			out[i] = g711.DecodeUlawFrame(s)
		}
	}
	return out, nil
}

type g722Codec struct {
	encoder *g722.Encoder
	decoder *g722.Decoder
}

func (c *g722Codec) Encode(pcm []int16) ([]byte, error) {
	if len(pcm)%2 != 0 {
		return nil, fmt.Errorf("G.722 requires an even number of samples")
	}
	out := make([]byte, len(pcm)/2)
	return out[:c.encoder.Encode(out, pcm)], nil
}
func (c *g722Codec) Decode(data []byte) ([]int16, error) {
	if len(data) > 960 {
		return nil, fmt.Errorf("G.722 frame exceeds 120 ms")
	}
	out := make([]int16, len(data)*2)
	return out[:c.decoder.Decode(out, data)], nil
}

func (c *g711Codec) Conceal(samples int, next []byte) ([]int16, error) {
	return make([]int16, samples), nil
}
func (c *g722Codec) Conceal(samples int, next []byte) ([]int16, error) {
	return make([]int16, samples), nil
}
