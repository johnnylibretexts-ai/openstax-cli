package main

import (
	"fmt"
	"os"

	"github.com/johnnylibretexts/openstax-cli/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		if !cli.ErrorAlreadyReported(err) {
			fmt.Fprintln(os.Stderr, "Error:", err)
		}
		os.Exit(cli.ExitCode(err))
	}
}
