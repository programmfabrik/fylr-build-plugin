package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// buildDir is the folder everything is assembled into. build/<plugin.name>/ is
// the on-disk plugin fylr loads via plugin.paths, and the tree the release zip
// is made of.
const buildDir = "build"

// goArch is one cross-compile target, named exactly like the Go build
// environment variables it sets.
type goArch struct {
	GOOS   string `yaml:"GOOS"`
	GOARCH string `yaml:"GOARCH"`
}

// defaultArchs is the default cross-compile matrix for Go server extensions —
// every platform fylr runs on, matching what fylr resolves via
// %_exec.GOOS%/%_exec.GOARCH%.
var defaultArchs = []goArch{
	{"linux", "amd64"}, {"linux", "arm64"},
	{"darwin", "amd64"}, {"darwin", "arm64"},
	{"windows", "amd64"}, {"windows", "arm64"},
}

// manifest is the lenient view of manifest.yml — only what the build needs.
// fylr itself parses the manifest leniently too (unknown keys are ignored).
type manifest struct {
	Plugin struct {
		Name        string `yaml:"name"`
		L10n        string `yaml:"l10n"`
		Webfrontend struct {
			URL  string `yaml:"url"`
			CSS  string `yaml:"css"`
			HTML string `yaml:"html"`
			// Readme is retired (#80537): the README.md next to manifest.yml
			// is the plugin's docs, no key involved. Parsed only so build can
			// reject a manifest that still declares it.
			Readme string `yaml:"readme"`
		} `yaml:"webfrontend"`
	} `yaml:"plugin"`
	BaseURLPrefix string `yaml:"base_url_prefix"`
	FasConfig     struct {
		ProduceConfig string   `yaml:"produce_config"`
		Cookbooks     []string `yaml:"cookbooks"`
	} `yaml:"fas_config"`
}

// buildConfig is build.yml — the explicit description of what the plugin
// delivers. Delivered files are never derived from the manifest (a derived
// list can silently be incomplete); instead, check validates the assembled
// tree against every path the manifest references.
type buildConfig struct {
	// Loca sheets pulled by the loca command.
	Loca []locaSheet `yaml:"loca"`
	// Build describes what the build produces beyond the plugin folder.
	Build struct {
		// ZipAliases are additional names the release zip is ALSO
		// written under, byte-identical copies beside <repo>.zip.
		//
		// This exists because a published asset name can never be
		// retired, only added to: fylr updates a url plugin by
		// re-fetching the url it stored at install time, not by
		// re-reading the marketplace catalog. An instance that
		// installed while the asset had a different name holds that
		// url forever, so dropping the name breaks its updates
		// silently, with no way for the instance to discover the new
		// one.
		//
		// So a repo whose asset name has ever changed - every plugin
		// migrated to this tool from a hand-written Makefile, which
		// named its zip whatever it liked - lists its historical
		// names here and keeps publishing them.
		ZipAliases []string `yaml:"zip_aliases"`
		// Bundles are assembled outputs: each is one file concatenated
		// from an ordered list of sources, every source handled by its
		// extension. The webfrontend bundle named by the manifest is
		// the common case and has its own shorthand under
		// webfrontend.coffee1/js; a bundle is what you reach for when
		// a plugin produces a SECOND assembled file, such as the
		// server-side updater the custom-data-type plugins run
		// through custom_types.<type>.update.exec.
		Bundles []bundle `yaml:"bundles"`
	} `yaml:"build"`
	// Server describes the server-side delivery.
	Server struct {
		// Install lists the files/dirs copied verbatim into the plugin
		// build folder (server code, fas_config, l10n, ...).
		Install []string `yaml:"install"`
		// Go describes the Go server extensions to cross-compile. Their
		// sources are never copied, only the built binaries ship.
		Go struct {
			// Archs lists the GOOS/GOARCH targets to build for.
			// Default: every platform fylr runs on.
			Archs []goArch `yaml:"archs"`
			// Modules lists the Go modules and their output paths.
			Modules []goExe `yaml:"modules"`
		} `yaml:"go"`
	} `yaml:"server"`
	// Webfrontend describes the webfrontend delivery.
	Webfrontend struct {
		// Coffee1 is compiled and concatenated (in this order) into
		// plugin.webfrontend.url under base_url_prefix. The key carries
		// the major version: the plugins are stuck on CoffeeScript 1.x.
		Coffee1 []string `yaml:"coffee1"`
		// JS files join the same bundle verbatim, after the compiled
		// CoffeeScript and in this order — for vendored libraries that
		// are not CoffeeScript. fylr loads only the one bundle named by
		// plugin.webfrontend.url, so a library delivered next to it
		// (webfrontend.install) would never be loaded.
		JS []string `yaml:"js"`
		// Scss files compile to same-named .css files ("_*.scss"
		// partials belong here only via @use, not listed).
		Scss []string `yaml:"scss"`
		// L10nJSON converts loca CSVs into the per-culture JSON files
		// the webfrontend loads (easydb-library l10n2json format).
		L10nJSON []l10nJSON `yaml:"l10n_json"`
		// Install lists webfrontend files delivered as-is: plain css,
		// html, images, fonts, ...
		Install []string `yaml:"install"`
	} `yaml:"webfrontend"`
}

type l10nJSON struct {
	CSV string `yaml:"csv"` // source loca CSV in the repo
	Out string `yaml:"out"` // dir under base_url_prefix, default "l10n"
}

// bundle is one assembled output file.
type bundle struct {
	// Out is the file written, relative to the plugin build folder.
	Out string `yaml:"out"`
	// Sources are concatenated in this order. The extension decides
	// the handling: .coffee is compiled (CoffeeScript 1.x), .scss is
	// compiled, anything else is taken verbatim.
	Sources []string `yaml:"sources"`
}

type locaSheet struct {
	Sheet string `yaml:"sheet"` // Google spreadsheet key
	GID   string `yaml:"gid"`   // tab gid
	CSV   string `yaml:"csv"`   // target CSV path in the repo
}

// goExe is one Go module and the plugin path its binaries are built to.
// %GOOS% and %GOARCH% in Exe are replaced per arch.
type goExe struct {
	Dir string `yaml:"dir"` // module directory, repo-relative
	Exe string `yaml:"exe"` // output template, e.g. go/hello-%GOOS%-%GOARCH%.exe
}

// plugin is the resolved build model: manifest + build.yml + derived facts.
type plugin struct {
	Manifest    manifest
	ManifestRaw []byte
	Config      buildConfig

	// ExecRefs are the %_exec.pluginDir%-relative paths the manifest
	// references (may contain %_exec.GOOS%/%_exec.GOARCH% placeholders).
	ExecRefs []string
}

func (p *plugin) Name() string { return p.Manifest.Plugin.Name }

// Dir returns the plugin build folder, build/<name>.
func (p *plugin) Dir() string { return filepath.Join(buildDir, p.Name()) }

var execRefRe = regexp.MustCompile(`%_exec\.pluginDir%/([^\s"']+)`)

// loadPlugin reads manifest.yml and build.yml from the current directory (the
// plugin repo root) and derives the build model.
func loadPlugin() (*plugin, error) {
	raw, err := os.ReadFile("manifest.yml")
	if err != nil {
		return nil, fmt.Errorf("manifest.yml not found — run from the plugin repo root: %w", err)
	}
	p := &plugin{ManifestRaw: raw}
	if err := yaml.Unmarshal(raw, &p.Manifest); err != nil {
		return nil, fmt.Errorf("parsing manifest.yml: %w", err)
	}
	if p.Name() == "" {
		return nil, fmt.Errorf("manifest.yml: plugin.name is empty")
	}
	if strings.ContainsAny(p.Name(), "/\\") {
		return nil, fmt.Errorf("manifest.yml: plugin.name %q must not contain path separators", p.Name())
	}

	if data, err := os.ReadFile("build.yml"); err == nil {
		dec := yaml.NewDecoder(strings.NewReader(string(data)))
		dec.KnownFields(true)
		if err := dec.Decode(&p.Config); err != nil {
			return nil, fmt.Errorf("parsing build.yml: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	// scan line-wise so commented-out manifest blocks do not count
	seen := map[string]bool{}
	for line := range strings.Lines(string(raw)) {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		for _, m := range execRefRe.FindAllStringSubmatch(line, -1) {
			if !seen[m[1]] {
				seen[m[1]] = true
				p.ExecRefs = append(p.ExecRefs, m[1])
			}
		}
	}
	sort.Strings(p.ExecRefs)
	return p, nil
}

// webPrefix returns the webfrontend dir (source dir == served base_url_prefix
// by convention), or "" when the plugin has no webfrontend.
func (p *plugin) webPrefix() (string, error) {
	wf := p.Manifest.Plugin.Webfrontend
	if wf.URL == "" && wf.CSS == "" && wf.HTML == "" {
		return "", nil
	}
	if p.BaseURLPrefixClean() == "" {
		return "", fmt.Errorf("manifest.yml: plugin.webfrontend is set but base_url_prefix is empty — set base_url_prefix: webfrontend")
	}
	return p.BaseURLPrefixClean(), nil
}

func (p *plugin) BaseURLPrefixClean() string {
	return strings.Trim(p.Manifest.BaseURLPrefix, "/")
}

// installSet returns the explicit delivery list from build.yml: server and
// webfrontend installs, copied verbatim into the plugin build folder.
func (p *plugin) installSet() []string {
	var out []string
	out = append(out, p.Config.Server.Install...)
	out = append(out, p.Config.Webfrontend.Install...)
	for i, item := range out {
		out[i] = strings.TrimSuffix(strings.TrimSpace(item), "/")
	}
	sort.Strings(out)
	return out
}

// goModules returns the Go server extensions to cross-compile, from build.yml.
func (p *plugin) goModules() ([]goExe, error) {
	for i, a := range p.Config.Server.Go.Archs {
		if a.GOOS == "" || a.GOARCH == "" {
			return nil, fmt.Errorf("build.yml: server.go.archs[%d] needs GOOS and GOARCH", i)
		}
	}
	mods := p.Config.Server.Go.Modules
	multiArch := len(p.archs()) > 1
	for i, m := range mods {
		if m.Dir == "" || m.Exe == "" {
			return nil, fmt.Errorf("build.yml: server.go.modules[%d] needs dir and exe", i)
		}
		if multiArch && (!strings.Contains(m.Exe, "%GOOS%") || !strings.Contains(m.Exe, "%GOARCH%")) {
			return nil, fmt.Errorf("build.yml: server.go.modules[%d].exe %q must contain %%GOOS%% and %%GOARCH%% when building for %d archs", i, m.Exe, len(p.archs()))
		}
	}
	return mods, nil
}

// archs returns the GOOS/GOARCH targets to build for.
func (p *plugin) archs() []goArch {
	if len(p.Config.Server.Go.Archs) > 0 {
		return p.Config.Server.Go.Archs
	}
	return defaultArchs
}
