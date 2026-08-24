package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/evanw/esbuild/pkg/api"
)

// buildNodeDeps resolves the npm dependencies of the plugin's node programs
// into the delivered files (#80732).
//
// node_modules never ships (copyTree skips it), so a `require("pg")` in a
// server program has nothing to resolve against on the installed instance.
// This does what node would do at runtime if the tree were there: follow every
// require from the entry, inline the reached modules into the one file fylr
// executes, and leave node's builtins as real requires.
//
// The entries are not configured anywhere. They are the manifest's exec
// references — the programs fylr runs — filtered to .js, which is the same set
// check validates. A plugin without npm dependencies never reaches esbuild,
// and a file whose module graph turns out to be itself alone is left exactly
// as delivered.
func buildNodeDeps(p *plugin) error {
	deps, err := npmDependencies()
	if err != nil || len(deps) == 0 {
		return err
	}
	var entries []string
	for _, ref := range p.ExecRefs {
		if filepath.Ext(ref) != ".js" {
			continue
		}
		// a reference may be missing on purpose (the example plugin's
		// _missing_.js callbacks); check reports those, not this
		rel := filepath.FromSlash(ref)
		if _, err := os.Stat(filepath.Join(p.Dir(), rel)); err == nil {
			entries = append(entries, rel)
		}
	}
	if len(entries) == 0 {
		return nil
	}
	if err := ensureNodeModules(); err != nil {
		return err
	}

	bundled := 0
	for _, rel := range entries {
		ok, err := resolveRequires(filepath.Join(p.Dir(), rel))
		if err != nil {
			return err
		}
		if ok {
			bundled++
			fmt.Printf("node: %s (npm dependencies resolved)\n", rel)
		}
	}
	fmt.Printf("node: %d of %d node program(s) needed npm dependencies\n", bundled, len(entries))
	return nil
}

// resolveRequires bundles file in place and reports whether anything was
// inlined. Resolution happens from the file's place in the BUILD tree, which
// is inside the repo, so node_modules is found by walking up exactly as node
// would; a require of a sibling that was installed alongside resolves against
// the delivered copy.
func resolveRequires(file string) (bool, error) {
	res := api.Build(api.BuildOptions{
		EntryPoints: []string{file},
		Bundle:      true,
		Platform:    api.PlatformNode,
		Format:      api.FormatCommonJS,
		Metafile:    true,
		Write:       false,
		LogLevel:    api.LogLevelWarning,
	})
	if len(res.Errors) > 0 {
		var msgs []string
		for _, e := range res.Errors {
			where := ""
			if e.Location != nil {
				where = fmt.Sprintf(" (%s:%d)", e.Location.File, e.Location.Line)
			}
			msgs = append(msgs, e.Text+where)
		}
		return false, fmt.Errorf("resolving npm dependencies of %s:\n  %s\n"+
			"a native module (node-gyp) cannot be bundled and must not be used in a plugin",
			file, strings.Join(msgs, "\n  "))
	}
	if len(res.OutputFiles) != 1 {
		return false, fmt.Errorf("resolving npm dependencies of %s: got %d output files, want 1", file, len(res.OutputFiles))
	}

	// Only an npm package needs inlining. A require of a delivered sibling
	// resolves on the instance exactly as it does here, so a program that
	// pulls nothing out of node_modules keeps the file it was written as —
	// no rewrapped copy of itself, no shifted line numbers.
	var meta struct {
		Inputs map[string]json.RawMessage `json:"inputs"`
	}
	if err := json.Unmarshal([]byte(res.Metafile), &meta); err != nil {
		return false, fmt.Errorf("reading esbuild metafile for %s: %w", file, err)
	}
	fromNodeModules := false
	for in := range meta.Inputs {
		if strings.Contains(filepath.ToSlash(in), "node_modules/") {
			fromNodeModules = true
			break
		}
	}
	if !fromNodeModules {
		return false, nil
	}
	return true, os.WriteFile(file, res.OutputFiles[0].Contents, 0o644)
}

// npmDependencies returns the runtime dependencies declared in the repo's
// package.json. devDependencies do not count: a plugin's test runner is not
// something a delivered program requires.
func npmDependencies() (map[string]string, error) {
	data, err := os.ReadFile("package.json")
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var pkg struct {
		Dependencies         map[string]string `json:"dependencies"`
		OptionalDependencies map[string]string `json:"optionalDependencies"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return nil, fmt.Errorf("parsing package.json: %w", err)
	}
	deps := pkg.Dependencies
	if deps == nil {
		deps = map[string]string{}
	}
	for name, v := range pkg.OptionalDependencies {
		deps[name] = v
	}
	return deps, nil
}

// ensureNodeModules installs the declared dependencies when the tree is
// missing or older than the lockfile. `npm ci` is the deterministic form and
// needs package-lock.json — a plugin repo commits it, it is a source not a
// build artifact.
func ensureNodeModules() error {
	lock := "package-lock.json"
	lockInfo, lockErr := os.Stat(lock)
	if modInfo, err := os.Stat("node_modules"); err == nil {
		if lockErr != nil || !lockInfo.ModTime().After(modInfo.ModTime()) {
			return nil
		}
	}
	if err := needTool("npm", "https://nodejs.org/"); err != nil {
		return err
	}
	install := []string{"ci"}
	if lockErr != nil {
		install = []string{"install"}
	}
	fmt.Printf("npm: %s\n", strings.Join(install, " "))
	cmd := exec.Command("npm", install...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("npm %s: %w\n%s", strings.Join(install, " "), err, out)
	}
	// npm leaves node_modules' mtime behind the lockfile's often enough
	// that the next build would reinstall for nothing
	now := lockInfo
	if now != nil {
		_ = os.Chtimes("node_modules", now.ModTime(), now.ModTime())
	}
	return nil
}
