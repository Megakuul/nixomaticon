package sip

import "context"

// Signaling operations are serialized, while lock waits honor cancellation.
type operationLock chan struct{}

func (operation operationLock) Lock(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case operation <- struct{}{}:
		if err := ctx.Err(); err != nil {
			operation.Unlock()
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (operation operationLock) TryLock() bool {
	select {
	case operation <- struct{}{}:
		return true
	default:
		return false
	}
}

func (operation operationLock) Unlock() { <-operation }
