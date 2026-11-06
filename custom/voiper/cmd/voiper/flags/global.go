package flags

import (
	"github.com/spf13/pflag"
)

type GlobalFlags struct {
	Base string
}

func NewGlobalFlags() *GlobalFlags {
	return &GlobalFlags{}
}

func (g *GlobalFlags) Attach(flagSet *pflag.FlagSet) {
	flagSet.StringVarP(&g.Base,
		"base", "b", "", "account directory (default: XDG config directory/voiper/accounts)",
	)
}
