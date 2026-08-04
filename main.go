// Command fylr-build-plugin is the build driver for fylr plugins — the "make"
// that knows how a fylr plugin is put together. It is run from a plugin repo's
// root without a submodule or vendored include, e.g.
//
//	go run github.com/programmfabrik/fylr-build-plugin@latest build
//
// It assembles build/<plugin.name>/ (loadable directly by fylr via
// plugin.paths), zips it for release, and can seal the zip for the fylr Plugin
// Marketplace (the former fylr-seal-plugin, merged here — the pluginseal
// package is imported by fylr to open sealed plugins).
//
// The name follows the fylr convention: "fylr-plugin-*" is reserved for
// plugins, tooling is "fylr-<verb>-plugin".
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
	var err error
	switch args[0] {
	case "init":
		err = runInit(args[1:])
	case "build":
		err = runBuild(args[1:])
	case "zip":
		err = runZip(args[1:])
	case "seal":
		err = runSeal(args[1:])
	case "genkey":
		err = runGenkey(args[1:])
	case "info":
		err = runInfo(args[1:])
	case "loca":
		err = runLoca(args[1:])
	case "readme":
		err = runReadme(args[1:])
	case "check":
		err = runCheck(args[1:])
	case "clean":
		err = runClean(args[1:])
	case "-h", "--help", "help":
		usage(os.Stdout)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", args[0])
		usage(os.Stderr)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "fylr-build-plugin %s: %s\n", args[0], err)
		os.Exit(1)
	}
}

func usage(w *os.File) {
	fmt.Fprint(w, `fylr-build-plugin — build driver for fylr plugins

Run from the plugin repo root (where manifest.yml lives).

Usage:
  fylr-build-plugin init     scaffold a new plugin into the current directory
  fylr-build-plugin build    assemble build/<name>/ — loadable by fylr from disk
  fylr-build-plugin zip      build + release zip
  fylr-build-plugin seal     seal a plugin zip for the marketplace (public key only)
  fylr-build-plugin genkey   generate a seal recipient keypair
  fylr-build-plugin info     inspect a (sealed) plugin zip
  fylr-build-plugin loca     pull loca CSV(s) from Google Sheets (build.yml)
  fylr-build-plugin readme   write a self-contained README (images inlined)
  fylr-build-plugin check    validate the build tree against the manifest
  fylr-build-plugin clean    remove the build folder

Run "fylr-build-plugin <command> -h" for the command's flags.
`)
}
