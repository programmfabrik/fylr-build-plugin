package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func runCheck(args []string) error {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, "Usage: fylr-build-plugin check\n\n"+
			"Validate the assembled build/<plugin.name>/ tree: every file the manifest\n"+
			"references (exec programs, l10n, webfrontend, fas_config) should exist.\n"+
			"Missing references are reported as warnings — the example plugin, for\n"+
			"instance, references callbacks that are missing on purpose so fylr's\n"+
			"apitests can assert the missing-module behavior. build runs this\n"+
			"automatically.\n")
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	p, err := loadPlugin()
	if err != nil {
		return err
	}
	if _, err := os.Stat(p.Dir()); err != nil {
		return fmt.Errorf("%s not found — run build first", p.Dir())
	}
	return check(p)
}

// check validates the build tree against everything the manifest references.
// A dangling reference usually means the build.yml delivery lists are
// incomplete and the installed plugin would fail at runtime — but it can also
// be deliberate (see runCheck), so it warns instead of failing.
func check(p *plugin) error {
	dir := p.Dir()
	var missing []string
	need := func(rel, what string) {
		for _, resolved := range expandOSArch(rel, p.archs()) {
			if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(resolved))); err != nil {
				missing = append(missing, fmt.Sprintf("%s (%s)", resolved, what))
			}
		}
	}

	for _, ref := range p.ExecRefs {
		need(ref, "exec reference")
	}
	if l := p.Manifest.Plugin.L10n; l != "" {
		need(l, "plugin.l10n")
	}
	web, err := p.webPrefix()
	if err != nil {
		return err
	}
	wf := p.Manifest.Plugin.Webfrontend
	for what, fn := range map[string]string{
		"plugin.webfrontend.url":  wf.URL,
		"plugin.webfrontend.css":  wf.CSS,
		"plugin.webfrontend.html": wf.HTML,
	} {
		if fn != "" {
			need(web+"/"+fn, what)
		}
	}
	if fc := p.Manifest.FasConfig.ProduceConfig; fc != "" {
		need(fc, "fas_config.produce_config")
	}
	for _, cb := range p.Manifest.FasConfig.Cookbooks {
		need(cb, "fas_config.cookbooks")
	}

	for _, m := range missing {
		fmt.Fprintf(os.Stderr, "check: WARNING: manifest references %s, missing in %s\n", m, dir)
	}
	fmt.Printf("check: %d manifest reference(s), %d missing in %s/\n",
		len(p.ExecRefs), len(missing), dir)
	return nil
}

// expandOSArch resolves %_exec.GOOS%/%_exec.GOARCH% placeholders into every
// configured arch, so a cross-compiled reference is checked for all targets.
func expandOSArch(ref string, archs []goArch) []string {
	if !strings.Contains(ref, "%_exec.GOOS%") && !strings.Contains(ref, "%_exec.GOARCH%") {
		return []string{ref}
	}
	out := make([]string, 0, len(archs))
	for _, arch := range archs {
		r := strings.ReplaceAll(ref, "%_exec.GOOS%", arch.GOOS)
		r = strings.ReplaceAll(r, "%_exec.GOARCH%", arch.GOARCH)
		out = append(out, r)
	}
	return out
}
