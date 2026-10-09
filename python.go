package main

import (
	"bufio"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// The fylr image the plugin's Python programs run in: Debian forky's python3
// on linux/amd64 (fylr docker/Dockerfile). Packages are resolved for it, not
// for the machine that builds, so a dependency behind a marker like
// python_version >= "3.13" or sys_platform == "linux" is decided the way it
// is on the instance. pip cannot do that — it evaluates markers against the
// interpreter running it — which is why the build uses uv.
const (
	imagePythonVersion  = "3.14"
	imagePythonPlatform = "x86_64-manylinux_2_40"
)

// imagePythonProvides are the compiled packages the fylr image installs
// explicitly, as constraints capped at the version it ships. A compiled
// package cannot travel in a plugin: it is built for one Python and one
// platform, and the image's Python moves with Debian. So these are resolved
// but not delivered, and a dependency that needs a newer one fails here
// instead of on the instance. Any other compiled package fails the build — it
// has to go into the image first, and then onto this list.
var imagePythonProvides = []string{
	"numpy<2.5",          // python3-numpy 2.4
	"opencv-python<4.11", // python3-opencv 4.10
	"opencv-python-headless<4.11",
}

// pythonDist is one installed distribution in the staging folder.
type pythonDist struct {
	Name     string // normalized (PEP 503)
	Version  string
	DistInfo string // the .dist-info folder, relative to the staging folder
	Pure     bool
	Tags     []string
}

// buildPythonDeps installs the packages build.yml lists for the plugin's
// python3 programs into the delivered tree (#81675).
//
// Every resolved package ships, dependencies included — nothing runs pip on
// an instance or an execserver. They land in the entry's dir, next to the
// programs: Python puts a script's own folder first on sys.path, so the
// programs import them without a line of setup, and the packages do not
// reach a program in any other folder.
func buildPythonDeps(p *plugin) error {
	entries := p.Config.Server.Python
	if len(entries) == 0 {
		return nil
	}
	seen := map[string]bool{}
	dsts := make([]string, len(entries))
	for i, e := range entries {
		key := fmt.Sprintf("server.python[%d]", i)
		dir := filepath.Clean(filepath.FromSlash(e.Dir))
		switch {
		case e.Dir == "":
			return fmt.Errorf("build.yml: %s needs dir", key)
		case filepath.IsAbs(dir) || dir == ".." || strings.HasPrefix(dir, ".."+string(filepath.Separator)):
			return fmt.Errorf("build.yml: %s: dir %q must be inside the plugin folder", key, e.Dir)
		case len(e.Packages) == 0:
			return fmt.Errorf("build.yml: %s: %q lists no packages", key, e.Dir)
		case seen[dir]:
			return fmt.Errorf("build.yml: %s: %q is listed twice — put its packages into one entry", key, e.Dir)
		}
		seen[dir] = true

		dsts[i] = filepath.Join(p.Dir(), dir)
		if info, err := os.Stat(dsts[i]); err != nil || !info.IsDir() {
			return fmt.Errorf("build.yml: %s: %q is not a folder of the build — deliver it with server.install", key, e.Dir)
		}
		if !p.runsPythonIn(dir) {
			fmt.Fprintf(os.Stderr, "build.yml: WARNING: %s: the manifest runs no Python program in %s — "+
				"a program finds the packages only from that folder itself\n", key, e.Dir)
		}
	}

	if err := needTool("uv", "https://docs.astral.sh/uv/getting-started/installation/"); err != nil {
		return err
	}
	for i, e := range entries {
		stage, err := os.MkdirTemp("", "fylr-build-plugin-python-")
		if err != nil {
			return err
		}
		err = resolvePython(e.Packages, stage)
		if err == nil {
			err = deliverPython(stage, dsts[i], fmt.Sprintf("server.python[%d]", i))
		}
		os.RemoveAll(stage)
		if err != nil {
			return err
		}
	}
	return nil
}

// runsPythonIn reports whether a .py program the manifest runs sits directly
// in dir.
func (p *plugin) runsPythonIn(dir string) bool {
	for _, ref := range p.ExecRefs {
		rel := filepath.FromSlash(ref)
		if filepath.Ext(rel) == ".py" && filepath.Dir(rel) == dir {
			return true
		}
	}
	return false
}

// resolvePython has uv resolve packages for the fylr image and install every
// one of them, wheels only, into stage/target. Nothing is built from source:
// a source build would compile for the machine running the build.
func resolvePython(packages []string, stage string) error {
	constraints := filepath.Join(stage, "image-constraints.txt")
	if err := os.WriteFile(constraints, []byte(strings.Join(imagePythonProvides, "\n")+"\n"), 0o644); err != nil {
		return err
	}
	args := []string{"pip", "install", "--quiet", "--no-config",
		"--target", filepath.Join(stage, "target"),
		"--python-version", imagePythonVersion,
		"--python-platform", imagePythonPlatform,
		"--only-binary", ":all:",
		"--no-compile",
		"--constraints", constraints,
	}
	args = append(args, packages...)
	if _, err := runCmd("", "uv", args...); err != nil {
		return fmt.Errorf("resolving Python packages %v for the fylr image (Python %s, %s): %w",
			packages, imagePythonVersion, imagePythonPlatform, err)
	}
	return nil
}

// deliverPython copies the pure-Python distributions installed in
// stage/target into dst, file by file as each one's RECORD lists them, and
// refuses compiled ones the fylr image does not provide.
func deliverPython(stage, dst, key string) error {
	target := filepath.Join(stage, "target")
	dists, err := readPythonDists(target)
	if err != nil {
		return err
	}
	provided := map[string]bool{}
	for _, c := range imagePythonProvides {
		provided[normalizePythonName(requirementName(c))] = true
	}

	var shipped, fromImage, refused []string
	for _, d := range dists {
		label := d.Name + " " + d.Version
		switch {
		case d.Pure:
			shipped = append(shipped, label)
		case provided[d.Name]:
			fromImage = append(fromImage, label)
		default:
			refused = append(refused, fmt.Sprintf("%s (%s)", label, strings.Join(d.Tags, ", ")))
		}
	}
	if len(refused) > 0 {
		var names []string
		for _, c := range imagePythonProvides {
			names = append(names, requirementName(c))
		}
		return fmt.Errorf("build.yml: %s: compiled Python package(s) %s — a plugin ships pure-Python packages only, "+
			"compiled ones come from the fylr image, which provides %s; anything else has to be added there "+
			"(fylr docker/Dockerfile) first",
			key, strings.Join(refused, "; "), strings.Join(names, ", "))
	}

	for _, d := range dists {
		if !d.Pure {
			continue
		}
		if err := copyPythonDist(target, dst, d); err != nil {
			return fmt.Errorf("build.yml: %s: %w", key, err)
		}
	}
	msg := fmt.Sprintf("python: %s: %s", filepath.ToSlash(dst), strings.Join(shipped, ", "))
	if len(fromImage) > 0 {
		msg += " (from the fylr image: " + strings.Join(fromImage, ", ") + ")"
	}
	fmt.Println(msg)
	return nil
}

// readPythonDists reads every *.dist-info in target.
func readPythonDists(target string) ([]pythonDist, error) {
	infos, err := filepath.Glob(filepath.Join(target, "*.dist-info"))
	if err != nil {
		return nil, err
	}
	var dists []pythonDist
	for _, info := range infos {
		d := pythonDist{DistInfo: filepath.Base(info)}
		meta, err := readHeaderFile(filepath.Join(info, "METADATA"))
		if err != nil {
			return nil, err
		}
		d.Name = normalizePythonName(first(meta["Name"]))
		d.Version = first(meta["Version"])
		wheel, err := readHeaderFile(filepath.Join(info, "WHEEL"))
		if err != nil {
			return nil, err
		}
		d.Tags = wheel["Tag"]
		// a wheel is pure when it installs into purelib and every tag it
		// carries is platform- and ABI-free (py3-none-any)
		d.Pure = first(wheel["Root-Is-Purelib"]) == "true" && len(d.Tags) > 0
		for _, tag := range d.Tags {
			if !strings.HasSuffix(tag, "-none-any") {
				d.Pure = false
			}
		}
		dists = append(dists, d)
	}
	sort.Slice(dists, func(i, j int) bool { return dists[i].Name < dists[j].Name })
	return dists, nil
}

// copyPythonDist copies the files RECORD lists for d from target to dst.
// Console scripts (bin/) stay behind — a program imports the package, it
// never runs its command line — and a file that would overwrite one already
// delivered is an error: two things would claim the same import name.
func copyPythonDist(target, dst string, d pythonDist) error {
	f, err := os.Open(filepath.Join(target, d.DistInfo, "RECORD"))
	if err != nil {
		return err
	}
	defer f.Close()
	r := csv.NewReader(f)
	r.FieldsPerRecord = -1
	for {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("%s: RECORD: %w", d.DistInfo, err)
		}
		if len(rec) == 0 || rec[0] == "" {
			continue
		}
		rel := filepath.Clean(filepath.FromSlash(rec[0]))
		if filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) ||
			strings.HasPrefix(rel, "bin"+string(filepath.Separator)) {
			continue
		}
		src := filepath.Join(target, rel)
		info, err := os.Stat(src)
		if err != nil {
			return fmt.Errorf("%s %s: %w", d.Name, d.Version, err)
		}
		out := filepath.Join(dst, rel)
		if _, err := os.Stat(out); err == nil {
			return fmt.Errorf("%s %s brings %s, which the plugin already delivers", d.Name, d.Version, filepath.ToSlash(rel))
		}
		if err := copyFile(src, out, info.Mode()); err != nil {
			return err
		}
	}
}

// readHeaderFile parses an RFC 822 style header block (METADATA, WHEEL):
// one "Key: value" per line, repeated keys collected, stopping at the first
// blank line where METADATA's long description begins.
func readHeaderFile(path string) (map[string][]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	h := map[string][]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			break
		}
		if k, v, ok := strings.Cut(line, ":"); ok && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			h[k] = append(h[k], strings.TrimSpace(v))
		}
	}
	return h, sc.Err()
}

func first(s []string) string {
	if len(s) == 0 {
		return ""
	}
	return s[0]
}

var (
	pythonNameSepRe = regexp.MustCompile(`[-_.]+`)
	requirementRe   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*`)
)

// normalizePythonName is PEP 503's normalization: Python_Slugify and
// python-slugify are one package.
func normalizePythonName(name string) string {
	return pythonNameSepRe.ReplaceAllString(strings.ToLower(name), "-")
}

// requirementName returns the package name a requirement starts with
// ("numpy<2.5" -> "numpy").
func requirementName(req string) string {
	return requirementRe.FindString(strings.TrimSpace(req))
}
