// entire-upgrade is an Entire CLI external command.
//
// Once built as an executable named `entire-upgrade`, the parent Entire CLI
// dispatches it when a user runs `entire upgrade`.
package main

import (
	"fmt"
	"os"

	"github.com/entireio/entire-upgrade/internal/cli"
)

var version = "dev"

func main() {
	if err := cli.Execute(version); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
