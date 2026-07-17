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
		case ".DS_Store", ".gitignore":
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
// (in list order) into the manifest's bundle, SCSS into same-named CSS files.
// Plain webfrontend artifacts ship via webfrontend.install.
func buildWebfrontend(p *plugin) error {
	coffees := p.Config.Webfrontend.Coffee1
	scssFiles := p.Config.Webfrontend.Scss
	if len(coffees) == 0 && len(scssFiles) == 0 {
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

	// CoffeeScript -> one bundle, named by the manifest
	if len(coffees) > 0 {
		if p.Manifest.Plugin.Webfrontend.URL == "" {
			return fmt.Errorf("build.yml coffee1 lists %d files but manifest.yml plugin.webfrontend.url is empty", len(coffees))
		}
		if err := needTool("coffee", "npm install -g coffeescript@1.12.7"); err != nil {
			return err
		}
		var bundle bytes.Buffer
		for _, cf := range coffees {
			out, err := runCmd("", "coffee", "-b", "-p", "--compile", cf)
			if err != nil {
				return fmt.Errorf("compiling %s: %w", cf, err)
			}
			bundle.Write(out)
		}
		target := filepath.Join(dst, p.Manifest.Plugin.Webfrontend.URL)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, bundle.Bytes(), 0o644); err != nil {
			return err
		}
		fmt.Printf("coffee: %d file(s) -> %s\n", len(coffees), target)
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
			cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+arch.GOOS, "GOARCH="+arch.GOARCH)
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
