package main

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writePNG(t *testing.T, path string) {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestInlineReadme(t *testing.T) {
	dir := t.TempDir()
	writePNG(t, filepath.Join(dir, "logo.png"))
	if err := os.MkdirAll(filepath.Join(dir, "doc"), 0o755); err != nil {
		t.Fatal(err)
	}
	writePNG(t, filepath.Join(dir, "doc", "shot.png"))

	md := strings.Join([]string{
		"# Plugin",
		"",
		"![logo](logo.png)",
		"a subdir image ![shot](doc/shot.png \"a title\")",
		"an html one <img src='logo.png' alt='x'> works too.",
		"",
		"absolute image ![remote](https://example.com/y.png) stays.",
		"a [link to docs](https://example.com) stays.",
		"a ![missing](nope.png) is left alone.",
		"a data uri ![d](data:image/png;base64,AAAA) stays.",
	}, "\n")

	out, n := InlineReadme([]byte(md), dir, 512*1024, &bytes.Buffer{})
	s := string(out)

	if n != 3 { // logo.png (md) + doc/shot.png (md) + logo.png (html)
		t.Fatalf("expected 3 inlined images, got %d\n%s", n, s)
	}
	for _, gone := range []string{"(logo.png)", "(doc/shot.png", `src='logo.png'`} {
		if strings.Contains(s, gone) {
			t.Errorf("relative image reference %q was not inlined:\n%s", gone, s)
		}
	}
	if c := strings.Count(s, "data:image/png;base64,"); c < 4 { // 3 inlined + 1 pre-existing data uri
		t.Errorf("expected at least 4 data URIs, got %d:\n%s", c, s)
	}
	for _, keep := range []string{"https://example.com/y.png", "(https://example.com)", "nope.png", "data:image/png;base64,AAAA"} {
		if !strings.Contains(s, keep) {
			t.Errorf("expected %q to be preserved:\n%s", keep, s)
		}
	}
}

func TestInlineReadmeSizeCap(t *testing.T) {
	dir := t.TempDir()
	big := filepath.Join(dir, "big.png")
	if err := os.WriteFile(big, make([]byte, 2048), 0o644); err != nil {
		t.Fatal(err)
	}
	warn := &bytes.Buffer{}
	out, n := InlineReadme([]byte("![big](big.png)"), dir, 1024, warn)
	if n != 0 {
		t.Fatalf("oversized image should not be inlined, got %d", n)
	}
	if !strings.Contains(string(out), "(big.png)") {
		t.Errorf("oversized image should be left as a relative link:\n%s", out)
	}
	if !strings.Contains(warn.String(), "big.png") {
		t.Errorf("expected a size warning, got %q", warn.String())
	}
}
