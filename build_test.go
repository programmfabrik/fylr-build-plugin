package main

import (
	"os"
	"path/filepath"
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
