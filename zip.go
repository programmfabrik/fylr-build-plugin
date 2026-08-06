package main

import (
	"archive/zip"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func runZip(args []string) error {
	fs := flag.NewFlagSet("zip", flag.ContinueOnError)
	release := fs.String("release", "", "release tag written to build-info.json")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, "Usage: fylr-build-plugin zip [flags]\n\n"+
			"Build the plugin (including its self-contained README.md) and pack\n"+
			"build/<plugin.name>/ into build/<plugin.name>.zip.\n\n"+
			"The zip is ALWAYS named after the plugin — see zipName.\n\nFlags:\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	p, err := loadPlugin()
	if err != nil {
		return err
	}
	if err := build(p, *release); err != nil {
		return err
	}
	return zipPlugin(p)
}

// zipName is the one naming rule for fylr plugin releases: the zip is named
// after the plugin's manifest name, "<plugin.name>.zip". It is deliberately
// not configurable.
//
// plugin.name is the identity fylr keys everything on — the plugin table, the
// baseconfig scope "plugin.<name>", system rights, and the zip's mandatory
// top-level folder — so naming the file after it makes the asset name a
// consequence of the plugin rather than a per-repo decision that can drift.
// Repository names are NOT used: they differ from the plugin name often
// enough (fylr-plugin-scancode-display ships fylr-scancode-display) that a
// repo-derived asset name would tell you the wrong thing about what you get.
//
// The practical payoff is that the marketplace catalog url,
// releases/latest/download/<plugin.name>.zip, is derivable from the manifest
// alone: nothing has to look up which asset a release happens to carry.
//
// There used to be an -out flag; the shipped workflow templates overrode the
// rule with the repo name, which is exactly the drift this prevents.
func zipName(p *plugin) string {
	return p.Name() + ".zip"
}

func zipPlugin(p *plugin) error {
	// the self-contained README next to the manifest (outside the seal for
	// sealed plugins) is delivered by build
	out := filepath.Join(buildDir, zipName(p))
	if err := writeZip(out, p.Dir(), p.Name()); err != nil {
		return err
	}
	info, err := os.Stat(out)
	if err != nil {
		return err
	}
	fmt.Printf("zip: %s (%d bytes)\n", out, info.Size())
	return nil
}

// writeZip packs root into a zip at outPath, every entry prefixed with
// topDir/ — fylr rejects a plugin zip whose top-level folder is not the
// plugin name.
func writeZip(outPath, root, topDir string) error {
	f, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer f.Close()
	zw := zip.NewWriter(f)

	var files []string
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		return err
	}
	sort.Strings(files)

	for _, path := range files {
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		hdr, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		hdr.Name = topDir + "/" + filepath.ToSlash(rel)
		hdr.Method = zip.Deflate
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			return err
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		_, err = io.Copy(w, in)
		in.Close()
		if err != nil {
			return err
		}
	}
	if err := zw.Close(); err != nil {
		return err
	}
	return f.Close()
}

// defaultZipPath is where "seal" expects the zip when -in is not given.
func defaultZipPath(p *plugin) string {
	return filepath.Join(buildDir, zipName(p))
}

// sealedName derives the sealed artifact name from the plain zip name.
func sealedName(zipPath string) string {
	base := strings.TrimSuffix(zipPath, ".zip")
	return base + "_sealed.zip"
}
