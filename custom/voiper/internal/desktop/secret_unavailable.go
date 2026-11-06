//go:build !secretservice || !cgo

package desktop

import "context"

const secretServiceBuilt = false

func secretOperation(context.Context, string, string, string) (string, error) {
	return "", ErrUnavailable
}
