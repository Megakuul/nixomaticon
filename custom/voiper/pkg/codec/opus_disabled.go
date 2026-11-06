//go:build !opus

package codec

import "errors"

const opusAvailable = false

func newOpus(_ Format) (Codec, error) {
	return nil, errors.New("Opus support requires a build with -tags opus and libopus")
}
