package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// The marketplace catalog lists every plugin's latest release (tag, date,
// sha256). The marketplace-releases workflow of this repository keeps those
// cells current on a schedule; notify-release lets a plugin's release workflow
// start it right away, for just that plugin.
const (
	notifyRepo  = "programmfabrik/fylr-build-plugin"
	notifyEvent = "plugin-released"
)

// notifyMarker is what a release workflow contains when it runs
// notify-release; zip warns about a workflow without it.
const notifyMarker = "notify-release"

func runNotifyRelease(args []string) error {
	fs := flag.NewFlagSet("notify-release", flag.ContinueOnError)
	repo := fs.String("repo", "", "released repository, owner/name")
	tag := fs.String("tag", "", "released tag")
	api := fs.String("api", "https://api.github.com", "GitHub API base url")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, "Usage: fylr-build-plugin notify-release -repo owner/name -tag vX.Y.Z\n\n"+
			"Tell the fylr Plugin Marketplace that the repository published a\n"+
			"release, so the catalog lists its tag, date and sha256 within minutes\n"+
			"instead of after the next scheduled update. It sends the repository\n"+
			"dispatch \""+notifyEvent+"\" to "+notifyRepo+" and needs a token\n"+
			"allowed to do that in GH_TOKEN, the one input that is not a flag: a\n"+
			"secret must not show in the process list. The plugin's own GITHUB_TOKEN\n"+
			"cannot dispatch to another repository.\n\n"+
			"Without GH_TOKEN it prints a notice and succeeds, so a release outside\n"+
			"the programmfabrik organization stays green. Run it after the zip is\n"+
			"reachable (attached to the release, or deployed to Pages).\n\nFlags:\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	token := os.Getenv("GH_TOKEN")
	if token == "" {
		fmt.Println("notify-release: no GH_TOKEN, the marketplace picks up this release with its next scheduled update")
		return nil
	}
	owner, name, ok := strings.Cut(*repo, "/")
	if !ok || owner == "" || name == "" {
		return fmt.Errorf("-repo %q is not owner/name", *repo)
	}

	body, err := json.Marshal(map[string]any{
		"event_type": notifyEvent,
		"client_payload": map[string]string{
			"owner": owner,
			"repo":  name,
			"tag":   *tag,
		},
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(*api, "/")+"/repos/"+notifyRepo+"/dispatches", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Content-Type", "application/json")

	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("dispatch to %s answered %s: %s", notifyRepo, resp.Status, strings.TrimSpace(string(msg)))
	}
	fmt.Printf("notify-release: told the marketplace about %s %s\n", *repo, *tag)
	return nil
}

// warnWithoutNotify warns when the repo's release workflow does not run
// notify-release: the catalog then learns about the release only with its next
// scheduled update.
func warnWithoutNotify() {
	const fn = ".github/workflows/release.yaml"
	data, err := os.ReadFile(fn)
	if err != nil || bytes.Contains(data, []byte(notifyMarker)) {
		return
	}
	fmt.Fprintf(os.Stderr, "zip: WARNING: %s does not run \"fylr-build-plugin %s\": the marketplace "+
		"lists this release only after its next scheduled update — add the marketplace job "+
		"of the release templates in %s\n", fn, notifyMarker, notifyRepo)
}
