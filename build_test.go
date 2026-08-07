package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A plugin whose whole webfrontend is vendored JS: no coffee is involved, and
// the sources land in the manifest's bundle in list order. The first file ends
// without a newline — the parts must not be glued together.
func TestBuildWebfrontendJSOnly(t *testing.T) {
	t.Chdir(t.TempDir())
	write(t, "manifest.yml", `plugin:
  name: js-plugin
  webfrontend:
    url: js-plugin.js
base_url_prefix: webfrontend
`)
	write(t, "build.yml", `webfrontend:
  js:
    - webfrontend/lib/vendor.js
    - webfrontend/JSPlugin.js
`)
	write(t, "webfrontend/lib/vendor.js", "var vendor = 1;")
	write(t, "webfrontend/JSPlugin.js", "var plugin = vendor;\n")

	p, err := loadPlugin()
	if err != nil {
		t.Fatal(err)
	}
	if err := buildWebfrontend(p); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(p.Dir(), "webfrontend", "js-plugin.js"))
	if err != nil {
		t.Fatal(err)
	}
	if want := "var vendor = 1;\nvar plugin = vendor;\n"; string(got) != want {
		t.Errorf("bundle = %q, want %q", got, want)
	}
}

// The bundle has no file to be written to when the manifest names none.
func TestBuildWebfrontendJSWithoutBundleURL(t *testing.T) {
	t.Chdir(t.TempDir())
	write(t, "manifest.yml", `plugin:
  name: js-plugin
  webfrontend:
    css: js-plugin.css
base_url_prefix: webfrontend
`)
	write(t, "build.yml", `webfrontend:
  js:
    - webfrontend/JSPlugin.js
`)
	write(t, "webfrontend/JSPlugin.js", "var plugin = 1;\n")

	p, err := loadPlugin()
	if err != nil {
		t.Fatal(err)
	}
	if err := buildWebfrontend(p); err == nil {
		t.Fatal("js sources without plugin.webfrontend.url did not fail")
	}
}

// The repo README.md ships self-contained next to manifest.yml — no manifest
// key involved; fylr reads it there for the marketplace and the plugin
// manager's README tab (#80537).
func TestBuildReadme(t *testing.T) {
	t.Chdir(t.TempDir())
	write(t, "manifest.yml", `plugin:
  name: readme-plugin
`)
	write(t, "README.md", "# readme-plugin\n")

	p, err := loadPlugin()
	if err != nil {
		t.Fatal(err)
	}
	if err := buildReadme(p); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(p.Dir(), "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if want := "# readme-plugin\n"; string(got) != want {
		t.Errorf("readme = %q, want %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(p.Dir(), "webfrontend", "README.md")); err == nil {
		t.Error("no webfrontend copy expected without a legacy webfrontend.readme key")
	}
}

// The retired plugin.webfrontend.readme key is rejected so it cannot creep
// back into manifests — the file next to manifest.yml is the docs, no key.
func TestBuildReadmeRetiredKeyRejected(t *testing.T) {
	t.Chdir(t.TempDir())
	write(t, "manifest.yml", `plugin:
  name: readme-plugin
  webfrontend:
    readme: README.md
base_url_prefix: webfrontend
`)
	write(t, "README.md", "# readme-plugin\n")

	p, err := loadPlugin()
	if err != nil {
		t.Fatal(err)
	}
	if err := buildReadme(p); err == nil {
		t.Fatal("retired plugin.webfrontend.readme key was not rejected")
	}
}

func write(t *testing.T, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// the naming rule: the release zip is named after the repository, in whatever
// form the origin remote happens to have
func TestRepoFromURL(t *testing.T) {
	for _, tc := range []struct{ url, want string }{
		{"git@github.com:programmfabrik/fylr-plugin-scancode-display.git", "fylr-plugin-scancode-display"},
		{"https://github.com/programmfabrik/fylr-plugin-pdf-creator", "fylr-plugin-pdf-creator"},
		{"https://github.com/programmfabrik/fylr-plugin-pdf-creator.git", "fylr-plugin-pdf-creator"},
		{"https://github.com/programmfabrik/fylr-plugin-geo-json/", "fylr-plugin-geo-json"},
		{"ssh://git@github.com/programmfabrik/fylr-plugin-orcid.git", "fylr-plugin-orcid"},
		{"  git@github.com:programmfabrik/fylr-plugin-orcid.git\n", "fylr-plugin-orcid"},
	} {
		if got := repoFromURL(tc.url); got != tc.want {
			t.Errorf("repoFromURL(%q) = %q, want %q", tc.url, got, tc.want)
		}
	}
}

// release.zip_aliases keeps a retired asset name resolving; the rejections
// matter because a bad alias is only noticed when a release publishes wrong
func TestZipAliasValidation(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "fylr-plugin-x.zip")
	if err := os.WriteFile(zipPath, []byte("PK-not-really"), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(aliases ...string) error {
		p := &plugin{}
		p.Config.Build.ZipAliases = aliases
		return writeZipAliases(p, zipPath)
	}

	for _, bad := range [][]string{
		{"sub/dir/Old.zip"},    // a path, not a name
		{"OldName"},            // no .zip
		{"Old.zip", "Old.zip"}, // listed twice
		{"fylr-plugin-x.zip"},  // the zip's own name
	} {
		if err := run(bad...); err == nil {
			t.Errorf("zip_aliases %v: expected an error, got none", bad)
		}
	}

	if err := run("OldName.zip", "EvenOlder.zip"); err != nil {
		t.Fatalf("valid aliases rejected: %v", err)
	}
	for _, name := range []string{"OldName.zip", "EvenOlder.zip"} {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("alias %s not written: %v", name, err)
		}
		if string(got) != "PK-not-really" {
			t.Errorf("alias %s is not a copy of the zip", name)
		}
	}
}

// assemble concatenates in the caller's order and never glues two sources
// together — the reason the updaters can put a hand-written .js first and the
// compiled .coffee after it
func TestAssembleOrderAndSeparation(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	// no trailing newline: the parts must not run into each other
	first := write("first.js", "var a = 1;")
	second := write("second.js", "var b = 2;\n")

	got, err := assemble([]string{first, second})
	if err != nil {
		t.Fatal(err)
	}
	if want := "var a = 1;\nvar b = 2;\n"; string(got) != want {
		t.Errorf("assemble = %q, want %q", got, want)
	}

	// a source that already starts with a newline supplies its own
	// separator — inserting another would add a blank line, which in a CSV
	// is a row
	leading := write("leading.csv", "\nb,2\n")
	joined, err := assemble([]string{write("head.csv", "a,1"), leading})
	if err != nil {
		t.Fatal(err)
	}
	if want := "a,1\nb,2\n"; string(joined) != want {
		t.Errorf("assemble with leading newline = %q, want %q", joined, want)
	}

	// the reverse order is a different file, not a normalised one
	rev, err := assemble([]string{second, first})
	if err != nil {
		t.Fatal(err)
	}
	if want := "var b = 2;\nvar a = 1;"; string(rev) != want {
		t.Errorf("reversed assemble = %q, want %q", rev, want)
	}
}

func TestBundleValidation(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "a.js")
	if err := os.WriteFile(src, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	run := func(bs ...bundle) error {
		p := &plugin{}
		p.Config.Build.Bundles = bs
		return buildBundles(p)
	}

	for name, bs := range map[string][]bundle{
		"no out":        {{Sources: []string{src}}},
		"no sources":    {{Out: "updater/x.js"}},
		"escapes":       {{Out: "../outside.js", Sources: []string{src}}},
		"absolute":      {{Out: "/etc/x.js", Sources: []string{src}}},
		"two write one": {{Out: "x.js", Sources: []string{src}}, {Out: "./x.js", Sources: []string{src}}},
	} {
		if err := run(bs...); err == nil {
			t.Errorf("%s: expected an error, got none", name)
		}
	}
}

// an empty directory inside an installed tree is an uninitialised submodule
// in all but name: the build would otherwise succeed and ship without it
func TestInstallRejectsEmptyDirs(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "server")
	if err := os.MkdirAll(filepath.Join(src, "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "main.py"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := copyTree(src, filepath.Join(root, "out"), nil)
	if err == nil {
		t.Fatal("expected an error for the empty lib/ directory, got none")
	}
	if !strings.Contains(err.Error(), "submodules") {
		t.Errorf("error should point at the submodule cause, got: %v", err)
	}

	// with a file in it, the same tree copies fine
	if err := os.WriteFile(filepath.Join(src, "lib", "util.py"), []byte("y\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := copyTree(src, filepath.Join(root, "out2"), nil); err != nil {
		t.Fatalf("populated tree rejected: %v", err)
	}
}
