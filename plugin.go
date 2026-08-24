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
	// ZipAliases are additional names the release zip is ALSO written
	// under, byte-identical copies beside <repo>.zip.
	//
	// This exists because a published asset name can never be retired,
	// only added to: fylr updates a url plugin by re-fetching the url it
	// stored at install time, not by re-reading the marketplace catalog.
	// An instance that installed while the asset had a different name
	// holds that url forever, so dropping the name breaks its updates
	// silently, with no way for the instance to discover the new one.
	//
	// So a repo whose asset name has ever changed - every plugin migrated
	// to this tool from a hand-written Makefile, which named its zip
	// whatever it liked - lists its historical names here and keeps
	// publishing them.
	ZipAliases []string `yaml:"zip_aliases"`
	// Server describes the server-side delivery: everything that is not
	// the webfrontend, l10n and fas_config included.
	Server struct {
		// Install lists the files/dirs copied verbatim into the plugin
		// build folder (server code, fas_config, l10n, ...).
		Install []string `yaml:"install"`
		// Bundles are assembled server-side outputs: each is one file
		// concatenated from an ordered list of sources, every source
		// handled by its extension. The custom-data-type plugins build
		// their updater this way (a hand-written Node script with a
		// compiled CoffeeScript utility appended), geonames assembles
		// its l10n CSV from two files.
		Bundles []bundle `yaml:"bundles"`
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
		// Bundles are assembled webfrontend outputs, the same mechanism
		// as server.bundles with one shorthand: an entry without "out"
		// is written to plugin.webfrontend.url, the only bundle fylr
		// loads — so the common case does not restate a path the
		// manifest already owns.
		//
		// Sources are concatenated in the order they are written,
		// whatever their extension: a vendored library that has to be
		// evaluated before the code using it simply comes first.
		Bundles []bundle `yaml:"bundles"`
		// Scss files compile to same-named .css files ("_*.scss"
		// partials belong here only via @use, not listed).
		Scss []string `yaml:"scss"`
		// L10nJSON converts loca CSVs into the per-culture JSON files
		// the webfrontend loads (easydb-library l10n2json format).
		L10nJSON []l10nJSON `yaml:"l10n_json"`
		// Install lists webfrontend files delivered as-is: plain css,
		// html, images, fonts, ...
		Install []string `yaml:"install"`

		// Coffee1 and JS are the retired pair, still parsed so an
		// unmigrated build.yml keeps building for one release
		// (migrateLegacy folds them into Bundles and warns). They were
		// two lists with a fixed coffee-then-js order, which could not
		// express a vendored library that has to be evaluated first —
		// commons-library had already escaped into a build.bundles
		// entry for exactly that.
		Coffee1 []string `yaml:"coffee1"`
		JS      []string `yaml:"js"`
	} `yaml:"webfrontend"`

	// Build is the retired wrapper: zip_aliases moved to the top level (in
	// a file named build.yml, a build: key says nothing), and its bundles
	// addressed any path at all, so migrateLegacy re-homes each by its out
	// — under base_url_prefix it is a webfrontend bundle, otherwise a
	// server one. Every JS file a plugin ships runs in one of those two
	// places; there was never a third.
	Build struct {
		ZipAliases []string `yaml:"zip_aliases"`
		Bundles    []bundle `yaml:"bundles"`
	} `yaml:"build"`
}

type l10nJSON struct {
	CSV string `yaml:"csv"` // source loca CSV in the repo
	Out string `yaml:"out"` // dir under base_url_prefix, default "l10n"
}

// bundle is one assembled output file.
type bundle struct {
	// Out is the file written, relative to the plugin build folder.
	// Required for a server bundle; a webfrontend bundle may leave it
	// empty, which means plugin.webfrontend.url under base_url_prefix.
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
	if err := p.migrateLegacy(); err != nil {
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

// migrateLegacy folds the retired build.yml keys (#80732) into the current
// layout and warns, so an unmigrated plugin repo keeps building for one
// release. A file that sets both spellings is an error, not a merge — the
// result would depend on an ordering nobody wrote down.
func (p *plugin) migrateLegacy() error {
	c := &p.Config
	warn := func(format string, a ...any) {
		fmt.Fprintf(os.Stderr, "build.yml: WARNING: "+format+"\n", a...)
	}

	if len(c.Build.ZipAliases) > 0 {
		if len(c.ZipAliases) > 0 {
			return fmt.Errorf("build.yml sets both zip_aliases and the retired build.zip_aliases — keep the top-level one")
		}
		c.ZipAliases = c.Build.ZipAliases
		warn("build.zip_aliases is retired, use top-level zip_aliases (#80732)")
	}

	if len(c.Webfrontend.Coffee1) > 0 || len(c.Webfrontend.JS) > 0 {
		if len(c.Webfrontend.Bundles) > 0 {
			return fmt.Errorf("build.yml sets webfrontend.bundles together with the retired webfrontend.coffee1/js — merge them into one bundles entry")
		}
		// coffee-then-js is what the shorthand always produced, whatever
		// order the two keys were written in
		sources := append(append([]string{}, c.Webfrontend.Coffee1...), c.Webfrontend.JS...)
		c.Webfrontend.Bundles = []bundle{{Sources: sources}}
		warn("webfrontend.coffee1/js are retired, use one ordered webfrontend.bundles entry (#80732)")
	}

	if len(c.Build.Bundles) > 0 {
		web, err := p.webPrefix()
		if err != nil {
			return err
		}
		for _, b := range c.Build.Bundles {
			if web != "" && underDir(b.Out, web) {
				c.Webfrontend.Bundles = append(c.Webfrontend.Bundles, b)
			} else {
				c.Server.Bundles = append(c.Server.Bundles, b)
			}
		}
		warn("build.bundles is retired, use server.bundles / webfrontend.bundles (#80732)")
	}
	return nil
}

// underDir reports whether the build-folder-relative path is inside dir.
func underDir(path, dir string) bool {
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// bundles returns every assembled output, server first, with each out
// resolved to a build-folder-relative path: a webfrontend entry may omit it,
// meaning the one bundle fylr loads (plugin.webfrontend.url).
//
// The two lists are kept apart because the sides are not interchangeable: a
// server bundle is a program node runs (and whose npm dependencies the build
// resolves), a webfrontend bundle is browser code. Validation holds them to
// their side of base_url_prefix.
func (p *plugin) bundles() ([]bundle, error) {
	web, err := p.webPrefix()
	if err != nil {
		return nil, err
	}
	out := make([]bundle, 0, len(p.Config.Server.Bundles)+len(p.Config.Webfrontend.Bundles))
	seen := map[string]bool{}
	add := func(key string, b bundle) error {
		switch {
		case len(b.Sources) == 0:
			return fmt.Errorf("build.yml: %s: %q lists no sources", key, b.Out)
		case filepath.IsAbs(b.Out) || strings.HasPrefix(filepath.Clean(b.Out), ".."):
			return fmt.Errorf("build.yml: %s: out %q must be inside the plugin folder", key, b.Out)
		case seen[filepath.Clean(b.Out)]:
			return fmt.Errorf("build.yml: %q is written by two bundles", b.Out)
		}
		seen[filepath.Clean(b.Out)] = true
		out = append(out, b)
		return nil
	}

	for _, b := range p.Config.Server.Bundles {
		if b.Out == "" {
			return nil, fmt.Errorf("build.yml: server.bundles: an entry has no out")
		}
		if web != "" && underDir(b.Out, web) {
			return nil, fmt.Errorf("build.yml: server.bundles: out %q is inside base_url_prefix %q — that is a webfrontend bundle", b.Out, web)
		}
		if err := add("server.bundles", b); err != nil {
			return nil, err
		}
	}

	for _, b := range p.Config.Webfrontend.Bundles {
		if b.Out == "" {
			if web == "" {
				return nil, fmt.Errorf("build.yml lists webfrontend.bundles but manifest.yml has no webfrontend — set plugin.webfrontend.url and base_url_prefix")
			}
			if p.Manifest.Plugin.Webfrontend.URL == "" {
				return nil, fmt.Errorf("build.yml: webfrontend.bundles: an entry has no out and manifest.yml plugin.webfrontend.url is empty — one of the two has to name the file")
			}
			b.Out = filepath.Join(web, p.Manifest.Plugin.Webfrontend.URL)
		} else if web != "" && !underDir(b.Out, web) {
			return nil, fmt.Errorf("build.yml: webfrontend.bundles: out %q is outside base_url_prefix %q — that is a server bundle", b.Out, web)
		}
		if err := add("webfrontend.bundles", b); err != nil {
			return nil, err
		}
	}
	return out, nil
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
