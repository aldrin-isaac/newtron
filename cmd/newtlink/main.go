// newtlink — standalone bridge process for newtlab
//
// Usage:
//
//	newtlink <bridge-config.json>    Run bridge workers from config file
//	newtlink --version               Print version information
//
// newtlink is uploaded to remote servers by the newtlab orchestrator.
// It takes a bridge config file path (not a lab name) and is self-contained
// with no dependency on ~/.newtlab/ directory conventions.
package main

import (
	"fmt"
	"os"

	"github.com/newtron-network/newtron/pkg/newtlab"
	"github.com/newtron-network/newtron/pkg/version"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Printf("newtlink %s (%s)\n", version.Version, version.GitCommit)
		os.Exit(0)
	}
	if len(os.Args) != 2 {
		fmt.Fprintf(os.Stderr, "usage: newtlink <bridge-config.json>\n")
		os.Exit(1)
	}
	if err := newtlab.RunBridgeFromFile(os.Args[1]); err != nil {
		fmt.Fprintf(os.Stderr, "newtlink: %v\n", err)
		os.Exit(1)
	}
}
