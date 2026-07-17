package main

import (
	"embed"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

//go:embed all:scaffold templates
var scaffoldFS embed.FS

// scaffoldFiles maps embedded scaffold sources to their target paths in the
// new plugin repo. Targets may contain the __PLUGIN_NAME__/__PLUGIN_PASCAL__
// tokens; contents are templated the same way. The gitignore is embedded
// without its dot so it does not act as an ignore file in THIS repo, and the
// public release workflow template doubles as the scaffold's workflow.
var scaffoldFiles = []struct{ src, dst string }{
	{"scaffold/manifest.yml", "manifest.yml"},
	{"scaffold/build.yml", "build.yml"},
	{"scaffold/Makefile", "Makefile"},
	{"scaffold/README.md", "README.md"},
	{"scaffold/gitignore", ".gitignore"},
	{"scaffold/l10n/loca.csv", "l10n/__PLUGIN_NAME__.csv"},
	{"scaffold/server/extension/hello.js", "server/extension/hello.js"},
	{"scaffold/webfrontend/plugin.coffee", "webfrontend/__PLUGIN_PASCAL__.coffee"},
	{"scaffold/webfrontend/plugin.css", "webfrontend/__PLUGIN_NAME__.css"},
	{"templates/release-public.yaml", ".github/workflows/release.yaml"},
}

var pluginNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

func runInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, "Usage: fylr-build-plugin init [plugin-name]\n\n"+
			"Scaffold a new fylr plugin into the current directory: manifest.yml,\n"+
			"build.yml, Makefile, an example API extension, webfrontend and loca —\n"+
			"buildable right away with \"make build\". Without an argument the plugin\n"+
			"name derives from the directory name (a fylr-plugin- prefix is\n"+
			"stripped). Fails inside an existing plugin; files that already exist\n"+
			"(README.md, LICENSE, ...) are kept and skipped.\n")
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 1 {
		fs.Usage()
		return fmt.Errorf("at most one plugin-name argument expected")
	}

	// refuse inside an existing plugin
	for _, fn := range []string{"manifest.yml", "build.yml"} {
		if _, err := os.Stat(fn); err == nil {
			return fmt.Errorf("%s exists — this is already a fylr plugin, init scaffolds only a fresh one", fn)
		}
	}

	name := fs.Arg(0)
	if name == "" {
		wd, err := os.Getwd()
		if err != nil {
			return err
		}
		name = strings.TrimPrefix(filepath.Base(wd), "fylr-plugin-")
	}
	if !pluginNameRe.MatchString(name) {
		return fmt.Errorf("plugin name %q: use lowercase letters, digits, - and _", name)
	}

	apply := func(s string) string {
		s = strings.ReplaceAll(s, "__PLUGIN_NAME__", name)
		return strings.ReplaceAll(s, "__PLUGIN_PASCAL__", pascalCase(name))
	}
	written := 0
	for _, f := range scaffoldFiles {
		dst := apply(f.dst)
		if _, err := os.Stat(dst); err == nil {
			fmt.Fprintf(os.Stderr, "init: %s exists, skipped\n", dst)
			continue
		}
		data, err := scaffoldFS.ReadFile(f.src)
		if err != nil {
			return err
		}
		if dir := filepath.Dir(dst); dir != "." {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return err
			}
		}
		if err := os.WriteFile(dst, []byte(apply(string(data))), 0o644); err != nil {
			return err
		}
		fmt.Printf("init: %s\n", dst)
		written++
	}
	fmt.Printf(`
plugin %q scaffolded (%d files). Next steps:
  make build            build into build/%s/ (needs go, coffee)
  fylr.yml              plugin: paths+: [<this repo>/build] for development
  build.yml             configure the loca sheet once the plugin has a tab
`, name, written, name)
	return nil
}

// pascalCase turns my-plugin_name into MyPluginName for the CoffeeScript
// class stub.
func pascalCase(name string) string {
	var b strings.Builder
	for _, part := range strings.FieldsFunc(name, func(r rune) bool { return r == '-' || r == '_' }) {
		b.WriteString(strings.ToUpper(part[:1]) + part[1:])
	}
	return b.String()
}
