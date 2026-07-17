package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// buildInfo is written beside the manifest as build-info.json; fylr reads it
// (all fields optional) and the plugin manager shows it.
type buildInfo struct {
	Repository  string  `json:"repository"`
	Rev         string  `json:"rev"`
	Release     *string `json:"release"`
	LastChanged string  `json:"lastchanged"`
	BuildDate   string  `json:"builddate"`
}

// writeBuildInfo collects the git facts of the checkout. The release tag is
// passed by the caller (the release workflow provides it via the Makefile),
// absent otherwise.
func writeBuildInfo(p *plugin, release string) error {
	bi := buildInfo{
		BuildDate: time.Now().Format("2006-01-02T15:04:05-0700"),
	}
	if out, err := runCmd("", "git", "remote", "get-url", "origin"); err == nil {
		repo := strings.TrimSpace(string(out))
		repo = strings.TrimSuffix(repo, ".git")
		if i := strings.LastIndexAny(repo, "/\\"); i >= 0 {
			repo = repo[i+1:]
		}
		bi.Repository = repo
	}
	if out, err := runCmd("", "git", "show", "--no-patch", "--format=%H"); err == nil {
		bi.Rev = strings.TrimSpace(string(out))
	}
	if out, err := runCmd("", "git", "show", "--no-patch", "--format=%ad",
		"--date=format:%Y-%m-%dT%T%z"); err == nil {
		bi.LastChanged = strings.TrimSpace(string(out))
	}
	if release != "" {
		bi.Release = &release
	}
	data, err := json.MarshalIndent(bi, "", "  ")
	if err != nil {
		return err
	}
	fn := filepath.Join(p.Dir(), "build-info.json")
	if err := os.WriteFile(fn, append(data, '\n'), 0o644); err != nil {
		return err
	}
	fmt.Printf("build-info: rev %.9s release %s\n", bi.Rev, orNull(bi.Release))
	return nil
}

func orNull(s *string) string {
	if s == nil {
		return "null"
	}
	return *s
}
