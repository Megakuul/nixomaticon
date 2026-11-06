package app

func (a *App) UnpublishStatus(account string) error {
	if err := a.ready(); err != nil {
		return err
	}
	return a.phone.UnpublishStatus(account)
}

func (a *App) UnwatchPresence(account, target string) error {
	if err := a.ready(); err != nil {
		return err
	}
	return a.phone.Unwatch(account, target)
}

// WatchVisiblePresence follows the status of the contacts currently displayed.
func (a *App) WatchVisiblePresence(account string, targets []string) error {
	if err := a.ready(); err != nil {
		return err
	}
	return a.phone.WatchVisible(account, targets)
}

// TrustNewSwyxCertificate accepts a changed Swyx API certificate.
func (a *App) TrustNewSwyxCertificate(account string) error {
	if err := a.ready(); err != nil {
		return err
	}
	return a.phone.TrustNewSwyxCertificate(account)
}
