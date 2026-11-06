//go:build opus

package codec

import (
	"fmt"
	"gopkg.in/hraban/opus.v2"
)

const opusAvailable = true

type opusCodec struct {
	encoder                          *opus.Encoder
	decoder                          *opus.Decoder
	bitrate, maxBitrate, goodReports int
}

func newOpus(format Format) (Codec, error) {
	enc, err := opus.NewEncoder(48000, 1, opus.AppVoIP)
	if err != nil {
		return nil, err
	}
	bitrate := 32000
	if format.Bitrate >= 6000 {
		bitrate = min(bitrate, format.Bitrate)
	}
	if err = enc.SetBitrate(bitrate); err != nil {
		return nil, err
	}
	bandwidth := opus.Fullband
	switch rate := format.MaxPlaybackRate; {
	case rate == 0:
	case rate < 12000:
		bandwidth = opus.Narrowband
	case rate < 16000:
		bandwidth = opus.Mediumband
	case rate < 24000:
		bandwidth = opus.Wideband
	case rate < 48000:
		bandwidth = opus.SuperWideband
	}
	if err = enc.SetMaxBandwidth(bandwidth); err != nil {
		return nil, err
	}
	if err = enc.SetInBandFEC(format.UseFEC); err != nil {
		return nil, err
	}
	if format.UseFEC {
		if err = enc.SetPacketLossPerc(5); err != nil {
			return nil, err
		}
	}
	dec, err := opus.NewDecoder(48000, 1)
	if err != nil {
		return nil, err
	}
	return &opusCodec{encoder: enc, decoder: dec, bitrate: bitrate, maxBitrate: bitrate}, nil
}
func (c *opusCodec) Encode(pcm []int16) ([]byte, error) {
	out := make([]byte, 1275)
	n, err := c.encoder.Encode(pcm, out)
	if err != nil {
		return nil, err
	}
	return out[:n], nil
}
func (c *opusCodec) Decode(data []byte) ([]int16, error) {
	out := make([]int16, 5760)
	n, err := c.decoder.Decode(data, out)
	if err != nil {
		return nil, err
	}
	return out[:n], nil
}

func (c *opusCodec) Conceal(samples int, next []byte) ([]int16, error) {
	pcm := make([]int16, samples)
	var err error
	if len(next) > 0 {
		err = c.decoder.DecodeFEC(next, pcm)
	} else {
		err = c.decoder.DecodePLC(pcm)
	}
	if err != nil {
		return nil, err
	}
	return pcm, nil
}

func (c *opusCodec) EncoderBitrate() int { return c.bitrate }

// Loss reports are a conservative resilience signal, not a bandwidth estimate.
// Stay within the negotiated cap and recover slowly after three good intervals.
func (c *opusCodec) SetPacketLoss(percent int) (int, error) {
	if percent < 0 || percent > 100 {
		return c.bitrate, fmt.Errorf("invalid packet loss percentage %d", percent)
	}
	if err := c.encoder.SetPacketLossPerc(percent); err != nil {
		return c.bitrate, err
	}
	bitrate := c.bitrate
	switch {
	case percent >= 5:
		c.goodReports = 0
		bitrate = max(min(12000, c.maxBitrate), bitrate*9/10)
	case percent <= 1:
		c.goodReports++
		if c.goodReports >= 3 {
			bitrate = min(c.maxBitrate, bitrate+1000)
			c.goodReports = 0
		}
	default:
		c.goodReports = 0
	}
	if bitrate != c.bitrate {
		if err := c.encoder.SetBitrate(bitrate); err != nil {
			return c.bitrate, err
		}
		c.bitrate = bitrate
	}
	return c.bitrate, nil
}
