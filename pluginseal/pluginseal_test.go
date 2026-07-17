package pluginseal

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"io"
	"path"
	"strings"
	"testing"
)

func makeZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	buf := &bytes.Buffer{}
	zw := zip.NewWriter(buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create %q: %v", name, err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("zip write %q: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

// pluginZip builds a fylr plugin zip: <name>/manifest.yml plus optional extra files
// (given relative to the plugin folder).
func pluginZip(t *testing.T, name string, files map[string]string) []byte {
	t.Helper()
	all := map[string]string{name + "/manifest.yml": "plugin:\n  name: " + name + "\n"}
	for k, v := range files {
		all[name+"/"+k] = v
	}
	return makeZip(t, all)
}

func TestSealOpenRoundTrip(t *testing.T) {
	pub, priv, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	pz := pluginZip(t, "my-plugin", map[string]string{"web/hello.json": "{}"})

	sealed, err := SealPluginZip(pz, pub)
	if err != nil {
		t.Fatalf("SealPluginZip: %v", err)
	}
	got, wasSealed, err := OpenPluginZip(sealed, priv)
	if err != nil {
		t.Fatalf("OpenPluginZip: %v", err)
	}
	if !wasSealed {
		t.Fatal("OpenPluginZip must report sealed=true")
	}
	if !bytes.Equal(got, pz) {
		t.Fatal("round trip mismatch")
	}
}

// TestOpenPicksMatchingKey: the envelope names its recipient, so an opener
// holding several keys picks the matching one — regardless of order.
func TestOpenPicksMatchingKey(t *testing.T) {
	pub, priv, _ := GenerateKey()
	_, otherPriv1, _ := GenerateKey()
	_, otherPriv2, _ := GenerateKey()
	pz := pluginZip(t, "my-plugin", nil)
	sealed, err := SealPluginZip(pz, pub)
	if err != nil {
		t.Fatalf("SealPluginZip: %v", err)
	}
	got, _, err := OpenPluginZip(sealed, otherPriv1, priv, otherPriv2)
	if err != nil {
		t.Fatalf("OpenPluginZip with key ring: %v", err)
	}
	if !bytes.Equal(got, pz) {
		t.Fatal("round trip mismatch")
	}
}

// TestSealedArtifactStructure: the sealed plugin keeps the plugin-folder convention
// — <name>/manifest.yml (plaintext) beside <name>/fylr-sealed-plugin.enc.
func TestSealedArtifactStructure(t *testing.T) {
	pub, _, _ := GenerateKey()
	sealed, err := SealPluginZip(pluginZip(t, "my-plugin", nil), pub)
	if err != nil {
		t.Fatalf("SealPluginZip: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(sealed), int64(len(sealed)))
	if err != nil {
		t.Fatalf("sealed artifact is not a valid zip: %v", err)
	}
	want := map[string]bool{"my-plugin/manifest.yml": false, "my-plugin/" + contentEntryName: false}
	for _, f := range zr.File {
		if _, ok := want[f.Name]; !ok {
			t.Errorf("unexpected entry %q", f.Name)
		}
		want[f.Name] = true
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("missing entry %q", name)
		}
	}
}

// TestManifestReadableWithoutKey: the outer manifest is plaintext and matches the
// input, so a plugin's identity is readable without the key.
func TestManifestReadableWithoutKey(t *testing.T) {
	pub, _, _ := GenerateKey()
	pz := pluginZip(t, "my-plugin", nil)
	want := readEntryExact(t, pz, "my-plugin/manifest.yml")

	sealed, err := SealPluginZip(pz, pub)
	if err != nil {
		t.Fatalf("SealPluginZip: %v", err)
	}
	got := readEntryExact(t, sealed, "my-plugin/manifest.yml")
	if !bytes.Equal(got, want) {
		t.Fatal("outer manifest must be the plaintext plugin manifest")
	}
}

// TestEnvelopeFormatIsStable guards against drifting from the format fylr opens:
// magic, version 2, then the 32-byte recipient public key.
func TestEnvelopeFormatIsStable(t *testing.T) {
	pub, _, _ := GenerateKey()
	sealed, err := SealPluginZip(pluginZip(t, "p", nil), pub)
	if err != nil {
		t.Fatalf("SealPluginZip: %v", err)
	}
	payload := readSealedPayload(t, sealed)
	if !bytes.HasPrefix(payload, []byte("FYLRPLG")) {
		t.Fatalf("payload does not start with magic FYLRPLG: %x", payload[:8])
	}
	if payload[7] != 2 {
		t.Fatalf("envelope version = %d, want 2", payload[7])
	}
	if !bytes.Equal(payload[8:40], pub[:]) {
		t.Fatal("envelope must carry the recipient public key at bytes 8..39")
	}
}

// TestOpenWrongKeyFails: no key in the ring matches the envelope's recipient.
func TestOpenWrongKeyFails(t *testing.T) {
	pub, _, _ := GenerateKey()
	_, otherPriv, _ := GenerateKey()
	sealed, _ := SealPluginZip(pluginZip(t, "p", nil), pub)
	if _, _, err := OpenPluginZip(sealed, otherPriv); err == nil {
		t.Fatal("opening with only a non-matching key must fail")
	}
}

func TestOpenNoKeyFails(t *testing.T) {
	pub, _, _ := GenerateKey()
	sealed, _ := SealPluginZip(pluginZip(t, "p", nil), pub)
	if _, _, err := OpenPluginZip(sealed); err == nil {
		t.Fatal("opening with no key available must fail")
	}
}

// TestOpenTamperedFails corrupts the sealed payload (keeping the outer zip valid)
// and proves the authenticated box rejects it.
func TestOpenTamperedFails(t *testing.T) {
	pub, priv, _ := GenerateKey()
	sealed, _ := SealPluginZip(pluginZip(t, "p", nil), pub)

	payload := readSealedPayload(t, sealed)
	payload[len(payload)-1] ^= 0xff // flip a byte in the sealed box
	tampered := rezip(t, "p/"+contentEntryName, payload)

	if _, _, err := OpenPluginZip(tampered, priv); err == nil {
		t.Fatal("opening a tampered sealed plugin must fail")
	}
}

// TestSealRejectsNonPlugin: sealing needs a fylr plugin zip (files under a
// top-level folder).
func TestSealRejectsNonPlugin(t *testing.T) {
	pub, _, _ := GenerateKey()
	if _, err := SealPluginZip(makeZip(t, map[string]string{"readme.txt": "hi"}), pub); err == nil {
		t.Fatal("sealing a zip without a <name>/ folder must fail")
	}
}

func TestPlainZipPassesThrough(t *testing.T) {
	plain := pluginZip(t, "p", nil)
	got, wasSealed, err := OpenPluginZip(plain)
	if err != nil {
		t.Fatalf("OpenPluginZip: %v", err)
	}
	if wasSealed {
		t.Fatal("a plain plugin zip must not be reported as sealed")
	}
	if !bytes.Equal(got, plain) {
		t.Fatal("a plain plugin zip must pass through unchanged")
	}
}

func rezip(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	buf := &bytes.Buffer{}
	zw := zip.NewWriter(buf)
	w, err := zw.Create(name)
	if err != nil {
		t.Fatalf("rezip create: %v", err)
	}
	if _, err := w.Write(content); err != nil {
		t.Fatalf("rezip write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("rezip close: %v", err)
	}
	return buf.Bytes()
}

// readSealedPayload returns the bytes of the <name>/fylr-sealed-plugin.enc entry.
func readSealedPayload(t *testing.T, zipBytes []byte) []byte {
	t.Helper()
	return findEntry(t, zipBytes, func(n string) bool { return path.Base(n) == contentEntryName })
}

// readEntryExact returns the bytes of the entry with the exact given name.
func readEntryExact(t *testing.T, zipBytes []byte, name string) []byte {
	t.Helper()
	return findEntry(t, zipBytes, func(n string) bool { return n == name })
}

func findEntry(t *testing.T, zipBytes []byte, match func(string) bool) []byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	for _, f := range zr.File {
		if !match(f.Name) {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open entry: %v", err)
		}
		defer rc.Close()
		data, err := io.ReadAll(rc)
		if err != nil {
			t.Fatalf("read entry: %v", err)
		}
		return data
	}
	t.Fatalf("entry not found")
	return nil
}

// TestSealedZipCommentNamesRecipient: the artifact carries a human-readable hint
// (zip archive comment) naming the recipient key it is sealed for.
func TestSealedZipCommentNamesRecipient(t *testing.T) {
	pub, _, _ := GenerateKey()
	sealed, err := SealPluginZip(pluginZip(t, "p", nil), pub)
	if err != nil {
		t.Fatalf("SealPluginZip: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(sealed), int64(len(sealed)))
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	want := base64.StdEncoding.EncodeToString(pub[:])
	if !strings.Contains(zr.Comment, want) {
		t.Fatalf("zip comment %q does not name the recipient %q", zr.Comment, want)
	}
}

// TestInspect reads name and recipient from a sealed artifact without a key, and
// reports a plain zip as unsealed.
func TestInspect(t *testing.T) {
	pub, _, _ := GenerateKey()
	pz := pluginZip(t, "my-plugin", nil)
	sealed, err := SealPluginZip(pz, pub)
	if err != nil {
		t.Fatalf("SealPluginZip: %v", err)
	}
	info, err := Inspect(sealed)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if info.Name != "my-plugin" || !info.Sealed || info.RecipientPub == nil || *info.RecipientPub != *pub {
		t.Fatalf("Inspect = %+v, want name=my-plugin sealed recipient=%x", info, pub[:])
	}
	plain, err := Inspect(pz)
	if err != nil {
		t.Fatalf("Inspect(plain): %v", err)
	}
	if plain.Sealed || plain.RecipientPub != nil {
		t.Fatalf("Inspect(plain) = %+v, want unsealed", plain)
	}
}
