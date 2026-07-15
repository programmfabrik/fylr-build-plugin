// Command fylr-build-plugin bundles build helpers for fylr plugins. It is meant
// to be run from a plugin's build/CI without a submodule, e.g.
//
//	go run github.com/programmfabrik/fylr-build-plugin@latest readme --out build/README.md
//
// The name follows the fylr convention: "fylr-plugin-*" is reserved for plugins,
// tooling is "fylr-<verb>-plugin" (this builds plugins; the sealer is
// fylr-seal-plugin).
package main

import (
	"fmt"
	"os"
)

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		usage(os.Stderr)
		os.Exit(2)
	}
	switch args[0] {
	case "readme":
		if err := runReadme(args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, "fylr-build-plugin readme:", err)
			os.Exit(1)
		}
	case "-h", "--help", "help":
		usage(os.Stdout)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", args[0])
		usage(os.Stderr)
		os.Exit(2)
	}
}

func usage(w *os.File) {
	fmt.Fprint(w, `fylr-build-plugin — build helpers for fylr plugins

Usage:
  fylr-build-plugin readme [flags]   write a self-contained README (relative images inlined)

Run "fylr-build-plugin readme -h" for the readme flags.
`)
}
