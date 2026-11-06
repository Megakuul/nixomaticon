package app

import "errors"

// Accept long call setup immediately; snapshots and phone-error events report
// progress. One pending activation prevents double clicks from queuing calls.
func (a *App) startCall(action func() error) error {
	if err := a.ready(); err != nil {
		return err
	}
	select {
	case a.callCommand <- struct{}{}:
	default:
		return errors.New("finish or cancel the pending call first")
	}
	a.mu.Lock()
	if a.closing {
		a.mu.Unlock()
		<-a.callCommand
		return errors.New("application is closing")
	}
	a.workers.Add(1)
	a.mu.Unlock()
	go func() {
		defer a.workers.Done()
		defer func() { <-a.callCommand }()
		if err := action(); err != nil {
			a.emitPhoneEvent("phone-error", err.Error())
		}
	}()
	return nil
}

// Consult calls a transfer target and keeps the call id on hold meanwhile.
func (a *App) Consult(id, target string) error {
	return a.startCall(func() error { return a.phone.Consult(id, target) })
}
