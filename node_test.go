package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeNodeDepPlugin scaffolds a plugin with one node extension and a
// hand-made node_modules, so the test resolves real requires without npm.
func writeNodeDepPlugin(t *testing.T, entry string) *plugin {
	t.Helper()
	t.Chdir(t.TempDir())
	write(t, "manifest.yml", `plugin:
  name: node-plugin
extensions:
  dump/dep:
    exec:
      service: "node"
      commands:
        - prog: "node"
          args:
            - type: "value"
              value: "%_exec.pluginDir%/server/extension/dep.js"
`)
	write(t, "build.yml", `server:
  install:
    - server
`)
	write(t, "server/extension/dep.js", entry)
	write(t, "package.json", `{"dependencies": {"tiny-dep": "^1.0.0"}}`)
	write(t, "node_modules/tiny-dep/package.json", `{"name": "tiny-dep", "main": "index.js"}`)
	write(t, "node_modules/tiny-dep/index.js", "module.exports = function () { return 'from-the-dep' }\n")

	p, err := loadPlugin()
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range p.installSet() {
		if err := copyTree(item, filepath.Join(p.Dir(), item), nil); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

// node_modules never ships, so a require of an npm package has to be resolved
// into the delivered file — that is what the plugin's own build.yml can no
// longer express by hand (#80732).
func TestNodeDepsResolved(t *testing.T) {
	p := writeNodeDepPlugin(t, "const dep = require('tiny-dep')\nconsole.log(dep())\n")
	if err := buildNodeDeps(p); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(p.Dir(), "server/extension/dep.js"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "from-the-dep") {
		t.Errorf("dependency not inlined:\n%s", got)
	}
	if strings.Contains(string(got), `require("tiny-dep")`) {
		t.Errorf("dependency still required at runtime:\n%s", got)
	}
}

// A node builtin stays a real require: platform node, not a browser bundle.
func TestNodeDepsKeepBuiltins(t *testing.T) {
	p := writeNodeDepPlugin(t, "const fs = require('fs')\nconst dep = require('tiny-dep')\nconsole.log(fs.existsSync('.'), dep())\n")
	if err := buildNodeDeps(p); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(p.Dir(), "server/extension/dep.js"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), `require("fs")`) {
		t.Errorf("node builtin was bundled away:\n%s", got)
	}
}

// A program that requires nothing is delivered exactly as written — no
// rewrapped copy of itself, no shifted line numbers.
func TestNodeDepsUntouchedWithoutRequires(t *testing.T) {
	const src = "console.log('plain')\n"
	p := writeNodeDepPlugin(t, src)
	if err := buildNodeDeps(p); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(p.Dir(), "server/extension/dep.js"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != src {
		t.Errorf("delivered file = %q, want it untouched (%q)", got, src)
	}
}

// A require of a delivered sibling resolves on the instance just as it does
// here, so it is left alone — only npm packages have to be inlined.
func TestNodeDepsUntouchedWithOnlyRelativeRequires(t *testing.T) {
	const src = "const util = require('./util.js')\nconsole.log(util())\n"
	p := writeNodeDepPlugin(t, src)
	write(t, "server/extension/util.js", "module.exports = function () { return 'sibling' }\n")
	if err := copyTree("server", filepath.Join(p.Dir(), "server"), nil); err != nil {
		t.Fatal(err)
	}
	if err := buildNodeDeps(p); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(p.Dir(), "server/extension/dep.js"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != src {
		t.Errorf("delivered file = %q, want it untouched (%q)", got, src)
	}
}

// Without dependencies declared there is nothing to resolve, so esbuild never
// runs — a plugin that has always shipped plain node programs is unaffected.
func TestNodeDepsSkippedWithoutPackageJSON(t *testing.T) {
	const src = "const dep = require('tiny-dep')\n"
	p := writeNodeDepPlugin(t, src)
	if err := os.Remove("package.json"); err != nil {
		t.Fatal(err)
	}
	if err := buildNodeDeps(p); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(p.Dir(), "server/extension/dep.js"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != src {
		t.Errorf("delivered file = %q, want it untouched (%q)", got, src)
	}
}

// A require that cannot be resolved fails the build instead of shipping a
// program that dies on its first call.
func TestNodeDepsUnresolvableFails(t *testing.T) {
	p := writeNodeDepPlugin(t, "const nope = require('not-installed')\n")
	if err := buildNodeDeps(p); err == nil {
		t.Fatal("an unresolvable require did not fail the build")
	}
}
