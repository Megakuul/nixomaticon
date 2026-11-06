package version

var VersionOverride string

func Version() string {
	if VersionOverride != "" {
		return VersionOverride
	}

	return "v0.0.0-dev"
}
