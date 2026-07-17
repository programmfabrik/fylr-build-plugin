package main

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// cultureRe matches l10n culture columns like "de-DE"; everything else in the
// CSV header (key, fil, R, ...) is ignored — so the raw Google Sheets export
// works as input.
var cultureRe = regexp.MustCompile(`^[a-z]{2}-[A-Z]{2}$`)

// buildL10nJSON converts the plugin's loca CSVs into the per-culture JSON
// files the webfrontend loads (the easydb-library l10n2json format, kept
// 1:1): <out>/<culture>.json = {"<culture>": {key: value}} and
// <out>/cultures.json = [{"code": "<culture>"}, ...] in CSV column order.
// Empty cells fall back to en-US, then the first non-empty culture.
func buildL10nJSON(p *plugin) error {
	specs := p.Config.Webfrontend.L10nJSON
	if len(specs) == 0 {
		return nil
	}
	web, err := p.webPrefix()
	if err != nil {
		return err
	}
	if web == "" {
		return fmt.Errorf("build.yml has webfrontend.l10n_json but manifest.yml base_url_prefix is empty")
	}
	for _, spec := range specs {
		if spec.CSV == "" {
			return fmt.Errorf("build.yml: webfrontend.l10n_json entry needs csv")
		}
		out := spec.Out
		if out == "" {
			out = "l10n"
		}
		outDir := filepath.Join(p.Dir(), web, filepath.FromSlash(out))
		if err := writeL10nJSON(spec.CSV, outDir); err != nil {
			return fmt.Errorf("l10n_json %s: %w", spec.CSV, err)
		}
		fmt.Printf("l10n_json: %s -> %s/\n", spec.CSV, outDir)
	}
	return nil
}

func writeL10nJSON(csvPath, outDir string) error {
	f, err := os.Open(csvPath)
	if err != nil {
		return err
	}
	defer f.Close()
	r := csv.NewReader(f)
	r.FieldsPerRecord = -1
	records, err := r.ReadAll()
	if err != nil {
		return err
	}
	if len(records) == 0 {
		return fmt.Errorf("empty CSV")
	}

	header := records[0]
	keyCol := -1
	var cultures []string // header order
	cultureCol := map[string]int{}
	for i, col := range header {
		switch {
		case col == "key":
			keyCol = i
		case cultureRe.MatchString(col):
			if _, dup := cultureCol[col]; !dup {
				cultures = append(cultures, col)
				cultureCol[col] = i
			}
		}
	}
	if keyCol == -1 {
		return fmt.Errorf(`no "key" column`)
	}

	// fallback order: en-US first, then the others in header order
	fallback := make([]string, 0, len(cultures))
	for _, c := range cultures {
		if c == "en-US" {
			fallback = append([]string{c}, fallback...)
		} else {
			fallback = append(fallback, c)
		}
	}

	cell := func(row []string, culture string) string {
		i := cultureCol[culture]
		if i >= len(row) {
			return ""
		}
		return strings.TrimSpace(row[i])
	}

	perCulture := map[string]map[string]string{}
	for _, row := range records[1:] {
		if keyCol >= len(row) {
			continue
		}
		key := strings.TrimSpace(row[keyCol])
		if key == "" {
			continue
		}
		for _, culture := range cultures {
			v := cell(row, culture)
			for _, fb := range fallback {
				if v != "" {
					break
				}
				v = cell(row, fb)
			}
			if perCulture[culture] == nil {
				perCulture[culture] = map[string]string{}
			}
			perCulture[culture][key] = v
		}
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	for _, culture := range cultures {
		if err := writeJSONFile(filepath.Join(outDir, culture+".json"),
			map[string]map[string]string{culture: perCulture[culture]}); err != nil {
			return err
		}
	}
	type code struct {
		Code string `json:"code"`
	}
	codes := make([]code, len(cultures))
	for i, c := range cultures {
		codes[i] = code{c}
	}
	return writeJSONFile(filepath.Join(outDir, "cultures.json"), codes)
}

// writeJSONFile writes v like l10n2json.py did: 4-space indent, unescaped
// non-ASCII/HTML, sorted object keys (Go maps marshal sorted).
func writeJSONFile(path string, v any) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "    ")
	if err := enc.Encode(v); err != nil {
		return err
	}
	return os.WriteFile(path, bytes.TrimRight(buf.Bytes(), "\n"), 0o644)
}
