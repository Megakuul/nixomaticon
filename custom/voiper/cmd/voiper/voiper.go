package main

import (
	"os"

	"github.com/megakuul/voiper/cmd/voiper/app"
)

func main() {
	root := app.NewRootCmd()
	if err := root.Execute(); err != nil {
		os.Stderr.WriteString(err.Error())
		os.Exit(1)
	}
}
