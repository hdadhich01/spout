package main

import (
	"os"

	"github.com/hdadhich01/spout/cmd/spout/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		cmd.PrintError(err)
		os.Exit(1)
	}
}
