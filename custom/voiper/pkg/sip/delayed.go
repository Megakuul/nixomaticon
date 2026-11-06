package sip

import (
	"context"
	"errors"
	"math/rand/v2"
	"mime"

	wire "github.com/emiago/sipgo/sip"
	"github.com/pion/sdp/v3"
)

func prepareDelayedAnswer(ctx context.Context, response *wire.Response, answerOffer func(context.Context, []byte) ([]byte, error)) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	contentType := response.ContentType()
	if contentType == nil || len(response.Body()) == 0 || len(response.Body()) > 65536 {
		return nil, errors.New("delayed INVITE response did not contain a bounded SDP offer")
	}
	mediaType, _, err := mime.ParseMediaType(contentType.Value())
	if err != nil || mediaType != "application/sdp" {
		return nil, errors.New("delayed INVITE response did not contain an SDP offer")
	}
	answer, err := answerOffer(ctx, append([]byte(nil), response.Body()...))
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(answer) == 0 || len(answer) > 65536 {
		return nil, errors.New("delayed offer callback did not return a bounded SDP answer")
	}
	return answer, nil
}

// RFC 3261 section 13.2.2.4 requires an answer even when an unwanted 2xx is
// immediately ended with BYE. Reject every stream without invoking media code.
func rejectDelayedOffer(body []byte) []byte {
	if len(body) == 0 || len(body) > 65536 {
		return nil
	}
	var offer sdp.SessionDescription
	if err := offer.Unmarshal(body); err != nil {
		return nil
	}
	answer := sdp.SessionDescription{
		Origin:                sdp.Origin{Username: "-", SessionID: rand.Uint64(), SessionVersion: 1, NetworkType: "IN", AddressType: "IP4", UnicastAddress: "0.0.0.0"},
		SessionName:           "-",
		ConnectionInformation: &sdp.ConnectionInformation{NetworkType: "IN", AddressType: "IP4", Address: &sdp.Address{Address: "0.0.0.0"}},
		TimeDescriptions:      []sdp.TimeDescription{{Timing: sdp.Timing{}}},
	}
	for _, media := range offer.MediaDescriptions {
		name := media.MediaName
		name.Port = sdp.RangedPort{Value: 0}
		if len(name.Formats) > 1 {
			name.Formats = name.Formats[:1]
		}
		answer.MediaDescriptions = append(answer.MediaDescriptions, &sdp.MediaDescription{MediaName: name})
	}
	result, err := answer.Marshal()
	if err != nil {
		return nil
	}
	return result
}
