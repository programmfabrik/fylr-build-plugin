package main

import (
	"archive/zip"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

func runZip(args []string) error {
	fs := flag.NewFlagSet("zip", flag.ContinueOnError)
	release := fs.String("release", "", "release tag written to build-info.json")
	// -out used to override the zip name. It is kept only so that a repo
	// still passing ZIP_NAME fails with an explanation instead of the bare
	// "flag provided but not defined".
	out := fs.String("out", "", "")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, "Usage: fylr-build-plugin zip [flags]\n\n"+
			"Build the plugin (including its self-contained README.md) and pack\n"+
			"build/<plugin.name>/ into build/<repo>.zip.\n\n"+
			"The zip is ALWAYS named after the repository — see zipName.\n\nFlags:\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *out != "" {
		return fmt.Errorf("the -out flag is gone: the release zip is always named after the repository, <repo>.zip.\n"+
			"Drop ZIP_NAME from .github/workflows and the ZIP_FLAGS line from the Makefile, and attach build/*.zip in the release step.\n"+
			"(this build would have written %q)", *out)
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
// after the REPOSITORY, "<repo>.zip". There is no flag, no env var and no
// per-repo exception.
//
// The repository is what a release url already names, so an asset named after
// it reads as one piece and can be written down without looking anything up:
//
//	https://github.com/programmfabrik/<repo>/releases/latest/download/<repo>.zip
//
// The manifest's plugin.name is deliberately NOT used. The two diverge often
// enough (fylr-plugin-scancode-display ships the plugin fylr-scancode-display,
// fylr-plugin-custom-data-type-k10plus ships custom-data-type-gvk) that a
// manifest-derived asset would sit at a url that disagrees with itself, and
// unlike the repository, plugin.name can be changed by an ordinary edit — the
// asset url would then move without anyone touching the release process.
//
// This is also simply what most plugins already publish, so the rule is the
// existing convention rather than a migration.
//
// Inside the zip, the top-level folder stays the plugin name: fylr requires
// it, and it is unaffected by the name of the file it arrives in.
func zipName(p *plugin) string {
	return repoName(p) + ".zip"
}

// repoName determines the repository the plugin is built from: the origin
// remote if there is one (authoritative — it survives a locally renamed
// checkout), otherwise the name of the directory holding the manifest.
func repoName(p *plugin) string {
	dir := p.Dir()
	if dir == "" {
		dir = "."
	}
	if url, err := gitOriginURL(dir); err == nil {
		if n := repoFromURL(url); n != "" {
			return n
		}
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	return filepath.Base(abs)
}

func gitOriginURL(dir string) (string, error) {
	cmd := exec.Command("git", "-C", dir, "remote", "get-url", "origin")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// repoFromURL takes the repository out of a remote url, in either form:
// git@github.com:programmfabrik/fylr-plugin-x.git or
// https://github.com/programmfabrik/fylr-plugin-x
func repoFromURL(url string) string {
	url = strings.TrimSuffix(strings.TrimSpace(url), "/")
	url = strings.TrimSuffix(url, ".git")
	if i := strings.LastIndexAny(url, "/:"); i >= 0 {
		url = url[i+1:]
	}
	return url
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
