package main

import (
	"os"
	"strings"
	"testing"
)

func TestInitScaffold(t *testing.T) {
	t.Chdir(t.TempDir())

	if err := runInit([]string{"my-plugin"}); err != nil {
		t.Fatal(err)
	}
	for _, fn := range []string{
		"manifest.yml", "build.yml", "Makefile", "README.md", ".gitignore",
		"l10n/my-plugin.csv", "server/extension/hello.js",
		"webfrontend/MyPlugin.coffee", "webfrontend/my-plugin.css",
		".github/workflows/release.yaml",
	} {
		if _, err := os.Stat(fn); err != nil {
			t.Errorf("missing scaffold file %s", fn)
		}
	}

	// the scaffold must load as a valid plugin model
	p, err := loadPlugin()
	if err != nil {
		t.Fatal(err)
	}
	if p.Name() != "my-plugin" {
		t.Errorf("plugin name = %q", p.Name())
	}
	if len(p.Config.Webfrontend.Coffee1) != 1 || !strings.Contains(p.Config.Webfrontend.Coffee1[0], "MyPlugin") {
		t.Errorf("coffee1 = %v", p.Config.Webfrontend.Coffee1)
	}
	data, _ := os.ReadFile("manifest.yml")
	if strings.Contains(string(data), "__PLUGIN") {
		t.Error("manifest.yml still contains template tokens")
	}

	// second init inside the scaffold must refuse
	if err := runInit(nil); err == nil {
		t.Fatal("init inside an existing plugin did not fail")
	}
}

func TestPascalCase(t *testing.T) {
	for in, want := range map[string]string{
		"my-plugin": "MyPlugin", "fylr_example": "FylrExample", "x": "X",
	} {
		if got := pascalCase(in); got != want {
			t.Errorf("pascalCase(%q) = %q, want %q", in, got, want)
		}
	}
}
