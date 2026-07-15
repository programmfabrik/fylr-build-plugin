# fylr-build-plugin

Build helpers for [fylr](https://fylr.io) plugins, distributed as a versioned Go
module so a plugin's build can use it without a git submodule:

```sh
go run github.com/programmfabrik/fylr-build-plugin@latest readme --out build/README.md
```

The name follows the fylr convention: `fylr-plugin-*` is reserved for plugins, so
tooling is named `fylr-<verb>-plugin` — this one builds plugins (the sealer is
`fylr-seal-plugin`).

## `readme`

Writes a **self-contained** README: every *relative* image reference is inlined
as a `data:` URI, so the document renders with no external files. This matters
because most plugin repositories are private — once a README leaves the repo (into
the plugin zip, then the marketplace) its relative asset paths are unreachable.

```
fylr-build-plugin readme [flags]
  --in               input markdown file (default "README.md")
  --out              output self-contained markdown file (default "build/README.md")
  --max-image-bytes  leave images larger than this many bytes as relative links (default 524288)
```

- Inlined: relative `![](...)` and `<img src="...">` images (png, jpg, jpeg, gif,
  svg, webp, bmp, ico, avif).
- Left untouched: absolute/protocol-relative/`data:` references, non-image
  extensions, missing files, and images over `--max-image-bytes` (each logged).

The result is committed into the plugin zip next to `manifest.yml` (outside the
seal for sealed plugins), where the marketplace can show it without cloning the
repo.

## Roadmap

`readme` is the first subcommand. The intent is to grow this into the plugin build
driver that replaces the deprecated `easydb-library` make machinery.
