package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// writeDist fakes one distribution as `uv pip install --target` leaves it:
// the package files, a dist-info with METADATA, WHEEL and a RECORD listing
// everything, a console script under bin/.
func writeDist(t *testing.T, target, name, version, tag string, files map[string]string) {
	t.Helper()
	distInfo := strings.ReplaceAll(name, "-", "_") + "-" + version + ".dist-info"
	pure := "true"
	if !strings.HasSuffix(tag, "-none-any") {
		pure = "false"
	}
	files[distInfo+"/METADATA"] = "Metadata-Version: 2.1\nName: " + name + "\nVersion: " + version + "\n\nLong: description\n"
	files[distInfo+"/WHEEL"] = "Wheel-Version: 1.0\nRoot-Is-Purelib: " + pure + "\nTag: " + tag + "\n"
	files["bin/"+name] = "#!/bin/sh\n"
	var record strings.Builder
	for path, content := range files {
		write(t, filepath.Join(target, path), content)
		record.WriteString(path + ",sha256=x,1\n")
	}
	record.WriteString(distInfo + "/RECORD,,\n")
	write(t, filepath.Join(target, distInfo, "RECORD"), record.String())
}

// Pure packages ship file by file, console scripts stay behind, and a
// compiled package the fylr image provides is not delivered at all.
func TestPythonDeliversPureAndLeavesImagePackages(t *testing.T) {
	t.Chdir(t.TempDir())
	writeDist(t, "stage/target", "python-slugify", "9.1.3", "py3-none-any",
		map[string]string{"slugify/__init__.py": "x = 1\n"})
	writeDist(t, "stage/target", "numpy", "2.4.6", "cp314-cp314-manylinux_2_28_x86_64",
		map[string]string{"numpy/__init__.py": "", "numpy/core.so": ""})
	write(t, "dst/program.py", "import slugify\n")

	if err := deliverPython("stage", "dst", "server.python[0]"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"slugify/__init__.py", "python_slugify-9.1.3.dist-info/METADATA", "python_slugify-9.1.3.dist-info/RECORD"} {
		if _, err := os.Stat(filepath.Join("dst", want)); err != nil {
			t.Errorf("%s not delivered: %v", want, err)
		}
	}
	for _, unwanted := range []string{"bin", "numpy", "numpy-2.4.6.dist-info"} {
		if _, err := os.Stat(filepath.Join("dst", unwanted)); err == nil {
			t.Errorf("%s delivered, want it left behind", unwanted)
		}
	}
}

// A compiled package the image does not provide fails the build: it would
// only run on the Python and platform it was built for.
func TestPythonRefusesCompiledPackage(t *testing.T) {
	t.Chdir(t.TempDir())
	writeDist(t, "stage/target", "shapely", "2.1.0", "cp314-cp314-manylinux_2_28_x86_64",
		map[string]string{"shapely/__init__.py": ""})
	if err := os.MkdirAll("dst", 0o755); err != nil {
		t.Fatal(err)
	}
	err := deliverPython("stage", "dst", "server.python[0]")
	if err == nil || !strings.Contains(err.Error(), "shapely 2.1.0") {
		t.Fatalf("err = %v, want the compiled package refused", err)
	}
	if _, err := os.Stat("dst/shapely"); err == nil {
		t.Error("the refused package was delivered anyway")
	}
}

// A package file must not replace one the plugin delivers itself.
func TestPythonCollisionFails(t *testing.T) {
	t.Chdir(t.TempDir())
	writeDist(t, "stage/target", "python-slugify", "9.1.3", "py3-none-any",
		map[string]string{"slugify/__init__.py": "x = 1\n"})
	write(t, "dst/slugify/__init__.py", "# the plugin's own module\n")
	if err := deliverPython("stage", "dst", "server.python[0]"); err == nil {
		t.Fatal("a package overwrote a delivered file")
	}
}

func TestPythonNames(t *testing.T) {
	for in, want := range map[string]string{"Python_Slugify": "python-slugify", "zope.interface": "zope-interface"} {
		if got := normalizePythonName(in); got != want {
			t.Errorf("normalizePythonName(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{"numpy<2.5": "numpy", "opencv-python-headless<4.11": "opencv-python-headless", " trimesh": "trimesh"} {
		if got := requirementName(in); got != want {
			t.Errorf("requirementName(%q) = %q, want %q", in, got, want)
		}
	}
}

// writePythonPlugin scaffolds a plugin with one python3 extension in
// server/extension and the given server.python block.
func writePythonPlugin(t *testing.T, python string) *plugin {
	t.Helper()
	t.Chdir(t.TempDir())
	write(t, "manifest.yml", `plugin:
  name: python-plugin
extensions:
  dump/dep:
    exec:
      service: "python3"
      commands:
        - prog: "python3"
          args:
            - type: "value"
              value: "%_exec.pluginDir%/server/extension/dep.py"
`)
	write(t, "build.yml", "server:\n  install:\n    - server\n"+python)
	write(t, "server/extension/dep.py", "from slugify import slugify\nprint(slugify('Grüße aus Berlin'))\n")
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

// build.yml mistakes fail before anything is resolved.
func TestPythonEntryValidation(t *testing.T) {
	for name, python := range map[string]string{
		"no dir":        "  python:\n    - packages: [python-slugify]\n",
		"outside":       "  python:\n    - dir: ../elsewhere\n      packages: [python-slugify]\n",
		"no packages":   "  python:\n    - dir: server/extension\n",
		"not delivered": "  python:\n    - dir: server/other\n      packages: [python-slugify]\n",
		"twice":         "  python:\n    - dir: server/extension\n      packages: [a]\n    - dir: server/extension/\n      packages: [b]\n",
	} {
		t.Run(name, func(t *testing.T) {
			p := writePythonPlugin(t, python)
			if err := buildPythonDeps(p); err == nil || !strings.Contains(err.Error(), "server.python[") {
				t.Fatalf("err = %v, want a build.yml error", err)
			}
		})
	}
}

// The real thing: uv resolves python-slugify for the fylr image, its
// dependency text-unidecode comes along unlisted, and the program imports
// both from its own folder without any setup.
func TestPythonResolvesWithUV(t *testing.T) {
	if testing.Short() {
		t.Skip("resolves from PyPI")
	}
	if _, err := exec.LookPath("uv"); err != nil {
		t.Skip("uv not installed")
	}
	p := writePythonPlugin(t, "  python:\n    - dir: server/extension\n      packages: [python-slugify]\n")
	if err := buildPythonDeps(p); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(p.Dir(), "server/extension")
	for _, want := range []string{"slugify/__init__.py", "text_unidecode/__init__.py"} {
		if _, err := os.Stat(filepath.Join(dir, want)); err != nil {
			t.Errorf("%s not delivered: %v", want, err)
		}
	}
	if _, err := exec.LookPath("python3"); err != nil {
		return
	}
	out, err := exec.Command("python3", filepath.Join(dir, "dep.py")).CombinedOutput()
	if err != nil {
		t.Fatalf("python3 dep.py: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "grusse-aus-berlin" {
		t.Errorf("dep.py printed %q, want grusse-aus-berlin", got)
	}
}
