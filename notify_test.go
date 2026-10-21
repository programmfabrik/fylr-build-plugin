package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestNotifyReleaseDispatch(t *testing.T) {
	var got struct {
		path, auth string
		body       map[string]any
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.path = r.Method + " " + r.URL.Path
		got.auth = r.Header.Get("Authorization")
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &got.body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	t.Setenv("GH_TOKEN", "secret")

	if err := runNotifyRelease([]string{"-api", srv.URL, "-repo", "programmfabrik/fylr-plugin-geo-json", "-tag", "v1.3.10"}); err != nil {
		t.Fatal(err)
	}
	if got.path != "POST /repos/"+notifyRepo+"/dispatches" {
		t.Errorf("request = %q", got.path)
	}
	if got.auth != "Bearer secret" {
		t.Errorf("Authorization = %q", got.auth)
	}
	if got.body["event_type"] != notifyEvent {
		t.Errorf("event_type = %v", got.body["event_type"])
	}
	payload, _ := got.body["client_payload"].(map[string]any)
	want := map[string]any{"owner": "programmfabrik", "repo": "fylr-plugin-geo-json", "tag": "v1.3.10"}
	for k, v := range want {
		if payload[k] != v {
			t.Errorf("client_payload.%s = %v, want %v", k, payload[k], v)
		}
	}
}

func TestNotifyReleaseWithoutTokenSkips(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("no request expected without GH_TOKEN")
	}))
	defer srv.Close()
	t.Setenv("GH_TOKEN", "")

	if err := runNotifyRelease([]string{"-api", srv.URL, "-repo", "someone/fylr-plugin-x", "-tag", "v1.0.0"}); err != nil {
		t.Fatal(err)
	}
}

func TestNotifyReleaseRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
	}))
	defer srv.Close()
	t.Setenv("GH_TOKEN", "secret")

	err := runNotifyRelease([]string{"-api", srv.URL, "-repo", "programmfabrik/fylr-plugin-x", "-tag", "v1.0.0"})
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("err = %v, want the 404", err)
	}
	if err := runNotifyRelease([]string{"-api", srv.URL, "-repo", "no-owner"}); err == nil {
		t.Error("a repository without owner must be rejected")
	}
}

func TestWarnWithoutNotify(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.MkdirAll(".github/workflows", 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		workflow string
		warns    bool
	}{
		{"jobs:\n  build:\n    steps: []\n", true},
		{"jobs:\n  marketplace:\n    steps:\n      - run: go run github.com/programmfabrik/fylr-build-plugin@latest notify-release\n", false},
	} {
		if err := os.WriteFile(".github/workflows/release.yaml", []byte(tc.workflow), 0o644); err != nil {
			t.Fatal(err)
		}
		if out := captureStderr(t, warnWithoutNotify); strings.Contains(out, "WARNING") != tc.warns {
			t.Errorf("workflow %q: stderr = %q, want warning %v", tc.workflow, out, tc.warns)
		}
	}
}

// the templates are what plugin repos copy: both must run notify-release
func TestReleaseTemplatesNotify(t *testing.T) {
	for _, fn := range []string{"templates/release-public.yaml", "templates/release-private.yaml"} {
		data, err := scaffoldFS.ReadFile(fn)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), notifyMarker) {
			t.Errorf("%s does not run %s", fn, notifyMarker)
		}
	}
}

func captureStderr(t *testing.T, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = w
	f()
	os.Stderr = old
	w.Close()
	out, _ := io.ReadAll(r)
	return string(out)
}
