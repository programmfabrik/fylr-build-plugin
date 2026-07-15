package main

import (
	"encoding/base64"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// imageMediaType maps the image extensions we know how to inline to their
// data-URI media type. Anything else is left as a relative link.
var imageMediaType = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".svg":  "image/svg+xml",
	".webp": "image/webp",
	".bmp":  "image/bmp",
	".ico":  "image/x-icon",
	".avif": "image/avif",
}

var (
	// ![alt](src "title") — src may be wrapped in <>
	mdImageRe = regexp.MustCompile(`!\[([^\]]*)\]\(\s*<?([^)>\s]+)>?(\s+"[^"]*"|\s+'[^']*')?\s*\)`)
	// <img ... src="..." ...>
	htmlImageRe = regexp.MustCompile(`(?i)(<img\b[^>]*?\bsrc\s*=\s*["'])([^"']+)(["'][^>]*>)`)
)

// InlineReadme returns markdown with every *relative* image reference replaced by
// a base64 data: URI, so the document renders with no external files — the point
// being that plugin repositories are mostly private, so relative asset paths are
// unreachable once the README leaves the repo. Absolute, protocol-relative and
// already-inlined references are kept; a missing file, a non-image extension, or
// an image larger than maxImageBytes is left as-is (with a note to warnw). It
// also reports how many images were inlined.
func InlineReadme(markdown []byte, baseDir string, maxImageBytes int64, warnw io.Writer) ([]byte, int) {
	count := 0
	inline := func(src string) (string, bool) {
		s := strings.TrimSpace(src)
		if s == "" || strings.HasPrefix(s, "data:") || strings.HasPrefix(s, "//") || strings.Contains(s, "://") {
			return "", false // absolute / protocol-relative / already inlined
		}
		clean := s
		if i := strings.IndexAny(clean, "?#"); i >= 0 {
			clean = clean[:i]
		}
		mt, ok := imageMediaType[strings.ToLower(filepath.Ext(clean))]
		if !ok {
			return "", false // not an image type we inline
		}
		fp := filepath.Join(baseDir, filepath.FromSlash(clean))
		info, err := os.Stat(fp)
		if err != nil {
			fmt.Fprintf(warnw, "  warning: image %q not found, left as a relative link\n", src)
			return "", false
		}
		if info.Size() > maxImageBytes {
			fmt.Fprintf(warnw, "  warning: image %q is %d bytes (> %d), left as a relative link\n", src, info.Size(), maxImageBytes)
			return "", false
		}
		data, err := os.ReadFile(fp)
		if err != nil {
			fmt.Fprintf(warnw, "  warning: cannot read image %q: %v\n", src, err)
			return "", false
		}
		count++
		return "data:" + mt + ";base64," + base64.StdEncoding.EncodeToString(data), true
	}

	out := mdImageRe.ReplaceAllStringFunc(string(markdown), func(m string) string {
		g := mdImageRe.FindStringSubmatch(m)
		if uri, ok := inline(g[2]); ok {
			return "![" + g[1] + "](" + uri + g[3] + ")"
		}
		return m
	})
	out = htmlImageRe.ReplaceAllStringFunc(out, func(m string) string {
		g := htmlImageRe.FindStringSubmatch(m)
		if uri, ok := inline(g[2]); ok {
			return g[1] + uri + g[3]
		}
		return m
	})
	return []byte(out), count
}

func runReadme(args []string) error {
	fs := flag.NewFlagSet("readme", flag.ContinueOnError)
	in := fs.String("in", "README.md", "input markdown file")
	out := fs.String("out", "build/README.md", "output self-contained markdown file")
	maxBytes := fs.Int64("max-image-bytes", 512*1024, "leave images larger than this many bytes as relative links")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, "Usage: fylr-build-plugin readme [flags]\n\n"+
			"Inline a README's relative images as data: URIs so it is self-contained\n"+
			"and can be shown in the plugin marketplace without the (mostly private) repo.\n\nFlags:\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	markdown, err := os.ReadFile(*in)
	if err != nil {
		return err
	}
	body, n := InlineReadme(markdown, filepath.Dir(*in), *maxBytes, os.Stderr)

	if dir := filepath.Dir(*out); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	if err := os.WriteFile(*out, body, 0o644); err != nil {
		return err
	}
	fmt.Printf("fylr-build-plugin readme: %s -> %s (%d image(s) inlined)\n", *in, *out, n)
	return nil
}
