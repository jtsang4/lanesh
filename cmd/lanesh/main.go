// Command lanesh is the single entry point for the Lanesh server, the machine
// daemon, and the management CLI.
package main

import (
	"os"

	"github.com/jtsang4/lanesh/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
