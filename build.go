package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func runBuild(args []string) error {
	fs := flag.NewFlagSet("build", flag.ContinueOnError)
	release := fs.String("release", "", "release tag written to build-info.json (the release workflow passes its tag via the Makefile)")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, "Usage: fylr-build-plugin build [flags]\n\n"+
			"Assemble build/<plugin.name>/: compile CoffeeScript/SCSS/Go and copy the\n"+
			"delivered files as listed in build.yml, write build-info.json, then\n"+
			"validate the result against every path manifest.yml references.\n\nFlags:\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	p, err := loadPlugin()
	if err != nil {
		return err
	}
	return build(p, *release)
}

func runClean(args []string) error {
	fs := flag.NewFlagSet("clean", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := os.RemoveAll(buildDir); err != nil {
		return err
	}
	fmt.Printf("removed %s/\n", buildDir)
	return nil
}

// build assembles the plugin build folder from scratch. release (may be
// empty) is recorded in build-info.json.
func build(p *plugin, release string) error {
	if err := os.RemoveAll(buildDir); err != nil {
		return err
	}
	dir := p.Dir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	// manifest.yml travels verbatim
	if err := os.WriteFile(filepath.Join(dir, "manifest.yml"), p.ManifestRaw, 0o644); err != nil {
		return err
	}

	mods, err := p.goModules()
	if err != nil {
		return err
	}
	goDirs := map[string]bool{}
	for _, m := range mods {
		goDirs[filepath.Clean(m.Dir)] = true
	}
	for _, item := range p.installSet() {
		if err := copyTree(item, filepath.Join(dir, item), goDirs); err != nil {
			return err
		}
	}

	if err := buildWebfrontend(p); err != nil {
		return err
	}
	// after install, so a bundle may overwrite a file that was copied in
	// verbatim — geonames appends its country list to the l10n CSV that way
	if err := buildBundles(p); err != nil {
		return err
	}
	if err := buildReadme(p); err != nil {
		return err
	}
	if err := buildL10nJSON(p); err != nil {
		return err
	}
	if err := buildGoModules(p, mods); err != nil {
		return err
	}
	if err := writeBuildInfo(p, release); err != nil {
		return err
	}
	if err := check(p); err != nil {
		return err
	}
	fmt.Printf("built %s/\n", dir)
	return nil
}

// copyTree copies src (file or directory) to dst. Go module directories are
// skipped (they are compiled, their sources do not ship), as are VCS/editor
// droppings and node_modules.
func copyTree(src, dst string, goDirs map[string]bool) error {
	info, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("install source %q: %w", src, err)
	}
	if !info.IsDir() {
		return copyFile(src, dst, info.Mode())
	}
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if goDirs[filepath.Clean(path)] {
				return filepath.SkipDir
			}
			switch d.Name() {
			case ".git", "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		switch d.Name() {
		// ".git" as a FILE is a submodule pointer — never ship it
		case ".DS_Store", ".gitignore", ".git", ".gitmodules":
			return nil
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		return copyFile(path, filepath.Join(dst, rel), info.Mode())
	})
}

func copyFile(src, dst string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode.Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// buildWebfrontend compiles the build.yml webfrontend sources: CoffeeScript
// and plain JS (in list order) into the manifest's bundle, SCSS into
// same-named CSS files. Plain webfrontend artifacts ship via
// webfrontend.install.
func buildWebfrontend(p *plugin) error {
	coffees := p.Config.Webfrontend.Coffee1
	jsFiles := p.Config.Webfrontend.JS
	scssFiles := p.Config.Webfrontend.Scss
	if len(coffees) == 0 && len(jsFiles) == 0 && len(scssFiles) == 0 {
		return nil
	}
	web, err := p.webPrefix()
	if err != nil {
		return err
	}
	if web == "" {
		return fmt.Errorf("build.yml has webfrontend sources but manifest.yml base_url_prefix is empty — set base_url_prefix: webfrontend")
	}
	dst := filepath.Join(p.Dir(), web)

	// compiled CoffeeScript + verbatim JS -> one bundle, named by the manifest
	if len(coffees) > 0 || len(jsFiles) > 0 {
		if p.Manifest.Plugin.Webfrontend.URL == "" {
			return fmt.Errorf("build.yml lists %d webfrontend bundle source(s) but manifest.yml plugin.webfrontend.url is empty", len(coffees)+len(jsFiles))
		}
		// coffee1 then js is the shorthand's fixed order; build.bundles
		// takes the sources in the order they are written
		content, err := assemble(append(append([]string{}, coffees...), jsFiles...))
		if err != nil {
			return err
		}
		target := filepath.Join(dst, p.Manifest.Plugin.Webfrontend.URL)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, content, 0o644); err != nil {
			return err
		}
		fmt.Printf("bundle: %d coffee + %d js file(s) -> %s\n", len(coffees), len(jsFiles), target)
	}

	// SCSS -> same-named .css
	if len(scssFiles) > 0 {
		if err := needTool("sass", "npm install -g sass"); err != nil {
			return err
		}
		for _, sf := range scssFiles {
			rel, err := filepath.Rel(web, sf)
			if err != nil || strings.HasPrefix(rel, "..") {
				return fmt.Errorf("scss file %q is not inside the webfrontend dir %q", sf, web)
			}
			target := filepath.Join(dst, strings.TrimSuffix(rel, ".scss")+".css")
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if _, err := runCmd("", "sass", "--no-source-map", sf, target); err != nil {
				return fmt.Errorf("compiling %s: %w", sf, err)
			}
		}
		fmt.Printf("sass: %d file(s) compiled\n", len(scssFiles))
	}
	return nil
}

// assemble concatenates sources in the given order, each handled by its
// extension: .coffee is compiled with CoffeeScript 1.x, .scss with sass,
// anything else is taken verbatim. Order is the caller's, deliberately —
// a vendored library may have to precede the code using it, or follow it.
func assemble(sources []string) ([]byte, error) {
	needs := func(ext string) bool {
		for _, s := range sources {
			if filepath.Ext(s) == ext {
				return true
			}
		}
		return false
	}
	if needs(".coffee") {
		if err := needTool("coffee", "npm install -g coffeescript@1.12.7"); err != nil {
			return nil, err
		}
	}
	if needs(".scss") {
		if err := needTool("sass", "npm install -g sass"); err != nil {
			return nil, err
		}
	}

	var out bytes.Buffer
	for _, src := range sources {
		var part []byte
		var err error
		switch filepath.Ext(src) {
		case ".coffee":
			part, err = runCmd("", "coffee", "-b", "-p", "--compile", src)
		case ".scss":
			part, err = runCmd("", "sass", "--no-source-map", "--stdin", src)
		default:
			part, err = os.ReadFile(src)
		}
		if err != nil {
			return nil, fmt.Errorf("bundle source %s: %w", src, err)
		}
		// two sources must not glue together, but they must not gain a
		// blank line either: a file meant to be appended may already
		// start with a newline (geonames' country list does), and in a
		// CSV that blank line is a row
		if out.Len() > 0 &&
			!bytes.HasSuffix(out.Bytes(), []byte("\n")) &&
			!bytes.HasPrefix(part, []byte("\n")) {
			out.WriteByte('\n')
		}
		out.Write(part)
	}
	return out.Bytes(), nil
}

// buildBundles writes every build.bundles entry: one assembled file each,
// anywhere in the plugin folder. This is what lets a plugin produce a SECOND
// assembled output beside the webfrontend bundle — the custom-data-type
// plugins build a server-side updater that way, a hand-written Node script
// with a compiled CoffeeScript utility appended.
func buildBundles(p *plugin) error {
	bundles := p.Config.Build.Bundles
	if len(bundles) == 0 {
		return nil
	}
	seen := map[string]bool{}
	for _, b := range bundles {
		switch {
		case b.Out == "":
			return fmt.Errorf("build.bundles: an entry has no out")
		case len(b.Sources) == 0:
			return fmt.Errorf("build.bundles: %q lists no sources", b.Out)
		case filepath.IsAbs(b.Out) || strings.HasPrefix(filepath.Clean(b.Out), ".."):
			return fmt.Errorf("build.bundles: out %q must be inside the plugin folder", b.Out)
		case seen[filepath.Clean(b.Out)]:
			return fmt.Errorf("build.bundles: %q is written by two entries", b.Out)
		}
		seen[filepath.Clean(b.Out)] = true

		content, err := assemble(b.Sources)
		if err != nil {
			return err
		}
		target := filepath.Join(p.Dir(), b.Out)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, content, 0o644); err != nil {
			return err
		}
		fmt.Printf("bundle: %d source(s) -> %s\n", len(b.Sources), target)
	}
	return nil
}

// buildReadme delivers the plugin's docs: the self-contained README.md lands
// next to manifest.yml — the one place fylr reads it, for the marketplace and
// (since #80537) for the plugin manager's README tab of the installed plugin.
// No manifest key is involved; shipping the file is enough. The retired
// plugin.webfrontend.readme key is rejected so it cannot creep back in.
func buildReadme(p *plugin) error {
	if p.Manifest.Plugin.Webfrontend.Readme != "" {
		return fmt.Errorf("manifest.yml sets plugin.webfrontend.readme — remove the key: the README.md next to manifest.yml is the plugin's docs, nothing reads the key anymore (#80537)")
	}
	if _, err := os.Stat("README.md"); err != nil {
		return nil
	}
	return runReadme([]string{"--out", filepath.Join(p.Dir(), "README.md")})
}

// buildGoModules cross-compiles every Go module for the configured
// GOOS/GOARCH pairs, straight into the build tree (the module's exe template,
// %GOOS%/%GOARCH% replaced). Module sources do not ship.
func buildGoModules(p *plugin, mods []goExe) error {
	if len(mods) == 0 {
		return nil
	}
	if err := needTool("go", "https://go.dev/dl/"); err != nil {
		return err
	}
	for _, m := range mods {
		for _, arch := range p.archs() {
			exe := strings.ReplaceAll(m.Exe, "%GOOS%", arch.GOOS)
			exe = strings.ReplaceAll(exe, "%GOARCH%", arch.GOARCH)
			out := filepath.Join(p.Dir(), filepath.FromSlash(exe))
			if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
				return err
			}
			absOut, err := filepath.Abs(out)
			if err != nil {
				return err
			}
			cmd := exec.Command("go", "build", "-trimpath", `-ldflags=-s -w`, "-o", absOut, ".")
			cmd.Dir = m.Dir
			// GOWORK=off: a plugin's Go module is never a workspace member, but a
			// go.work anywhere above the checkout claims it and fails the build
			// with "not one of the workspace modules"
			cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOWORK=off", "GOOS="+arch.GOOS, "GOARCH="+arch.GOARCH)
			if out, err := cmd.CombinedOutput(); err != nil {
				return fmt.Errorf("go build %s (%s/%s): %w\n%s", m.Dir, arch.GOOS, arch.GOARCH, err, out)
			}
		}
		fmt.Printf("go: %s -> %s (%d archs)\n", m.Dir, m.Exe, len(p.archs()))
	}
	return nil
}

// needTool fails with an actionable message when an external build tool is not
// installed.
func needTool(name, installHint string) error {
	if _, err := exec.LookPath(name); err != nil {
		return fmt.Errorf("%q not found in PATH — install it: %s", name, installHint)
	}
	return nil
}

func runCmd(dir, name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w\n%s", name, strings.Join(args, " "), err, stderr.String())
	}
	return out, nil
}
