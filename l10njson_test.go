package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestWriteL10nJSON pins the easydb-library l10n2json.py format: culture
// columns filtered by shape (fil/R/key ignored), {culture: {key: value}}
// wrapping, en-US-first fallback for empty cells, cultures.json in CSV
// column order.
func TestWriteL10nJSON(t *testing.T) {
	dir := t.TempDir()
	csv := filepath.Join(dir, "loca.csv")
	os.WriteFile(csv, []byte(
		"fil,key,de-DE,R,en-US,R,it-IT,R\n"+
			"#1,a.b,Hallo,FALSE,Hello,FALSE,,TRUE\n"+
			",empty.key.skipped,,,,,,\n"+
			"#1,\"c,d\",Zwei,FALSE,Two,FALSE,Due,FALSE\n"), 0o644)
	// row 2 has an empty key cell? no — key "empty.key.skipped" with empty
	// values everywhere: it-IT and de-DE fall back to en-US ("")

	out := filepath.Join(dir, "out")
	if err := writeL10nJSON(csv, out); err != nil {
		t.Fatal(err)
	}

	read := func(fn string) string {
		data, err := os.ReadFile(filepath.Join(out, fn))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	itIT := read("it-IT.json")
	want := `{
    "it-IT": {
        "a.b": "Hello",
        "c,d": "Due",
        "empty.key.skipped": ""
    }
}`
	if itIT != want {
		t.Errorf("it-IT.json:\n%s\nwant:\n%s", itIT, want)
	}
	cultures := read("cultures.json")
	wantC := `[
    {
        "code": "de-DE"
    },
    {
        "code": "en-US"
    },
    {
        "code": "it-IT"
    }
]`
	if cultures != wantC {
		t.Errorf("cultures.json:\n%s\nwant:\n%s", cultures, wantC)
	}
}
