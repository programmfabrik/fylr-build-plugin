package main

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

func runLoca(args []string) error {
	fs := flag.NewFlagSet("loca", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, "Usage: fylr-build-plugin loca\n\n"+
			"Pull the plugin's loca CSV(s) from their Google Sheets master into the\n"+
			"repo. The sheets are configured in build.yml:\n\n"+
			"  loca:\n"+
			"    - csv: l10n/example-loca.csv\n"+
			"      sheet: <spreadsheet key>\n"+
			"      gid: \"<tab gid>\"\n")
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	p, err := loadPlugin()
	if err != nil {
		return err
	}
	if len(p.Config.Loca) == 0 {
		return fmt.Errorf("no loca sheets configured in build.yml")
	}
	for _, ls := range p.Config.Loca {
		if ls.Sheet == "" || ls.CSV == "" {
			return fmt.Errorf("build.yml loca entry needs sheet and csv (got sheet=%q csv=%q)", ls.Sheet, ls.CSV)
		}
		if err := pullLocaCSV(ls); err != nil {
			return fmt.Errorf("loca %s: %w", ls.CSV, err)
		}
	}
	return nil
}

// pullLocaCSV downloads one sheet tab as CSV (the sheet must be readable by
// link, like the fylr loca sheets) and writes it CR-stripped, ready for
// pflib.Localization.Load.
func pullLocaCSV(ls locaSheet) error {
	url := fmt.Sprintf("https://docs.google.com/spreadsheets/u/1/d/%s/export?format=csv&gid=%s",
		ls.Sheet, ls.GID)
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s (is the sheet shared by link?)", url, resp.Status)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	body := strings.ReplaceAll(string(data), "\r", "")
	// a private sheet answers 200 with an HTML login page
	if strings.HasPrefix(strings.TrimSpace(body), "<") {
		return fmt.Errorf("got HTML instead of CSV — the sheet is not shared by link")
	}
	header, _, _ := strings.Cut(body, "\n")
	if !strings.Contains(header, "key") {
		return fmt.Errorf("first CSV line has no \"key\" column: %q", header)
	}
	if err := os.WriteFile(ls.CSV, []byte(body), 0o644); err != nil {
		return err
	}
	fmt.Printf("loca: sheet %s gid %s -> %s (%d bytes)\n", ls.Sheet, ls.GID, ls.CSV, len(body))
	return nil
}
