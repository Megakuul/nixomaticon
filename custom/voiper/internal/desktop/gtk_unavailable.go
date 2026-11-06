//go:build !linux || !cgo || !webkit2_41

package desktop

func PrepareGTK() error { return nil }
