// Package pluginseal seals a fylr plugin zip into a "sealed plugin" — still a
// valid zip — and opens one back.
//
// Sealing needs only the recipient's PUBLIC key, so it runs in any (public)
// plugin build pipeline without a secret. Opening needs the matching private key,
// which lives in fylr. The envelope format is defined here once so the
// fylr-seal-plugin CLI and the fylr server share a single source of truth and
// cannot drift — the same role license.DigestInput plays for the license signer
// and verifier.
//
// A sealed plugin keeps the fylr plugin-folder convention — it is an ordinary zip
// containing the plugin's folder with a plaintext manifest and the sealed plugin
// beside it:
//
//	<name>/manifest.yml             plaintext — the plugin's identity, readable without a key
//	<name>/fylr-sealed-plugin.enc   magic "FYLRPLG" | version(1) | recipient public key(32) | NaCl sealed box(<the plugin zip>)
//
// The recipient public key travels in the envelope (like an age recipient
// stanza or a PGP key id), so an opener holding several private keys picks the
// matching one deterministically instead of trying them all.
//
// fylr imports this package to open sealed plugins (supplying its private keys);
// the CLI imports it to seal them.
package pluginseal

import (
	"archive/zip"
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"path"
	"strings"
	"time"

	"golang.org/x/crypto/curve25519"
	"golang.org/x/crypto/nacl/box"
)

// magic prefixes the sealed payload. It is stable across format versions so a
// reader can still recognise a package it cannot open (newer version, or a key it
// lacks).
var magic = []byte("FYLRPLG")

// envelopeVersion is the current payload format: magic, version byte, the
// 32-byte recipient public key, then the sealed box.
const envelopeVersion byte = 2

// contentEntryName is the sealed payload's filename, written inside the plugin's
// folder (<name>/fylr-sealed-plugin.enc) beside the plaintext manifest.
const contentEntryName = "fylr-sealed-plugin.enc"

// GenerateKey returns a fresh recipient keypair: seal plugins to pub, and give
// priv to fylr (embedded, or on a marketplace source in fylr.yml) to open them.
func GenerateKey() (pub, priv *[32]byte, err error) {
	return box.GenerateKey(rand.Reader)
}

// PublicKey derives the public key of a private key.
func PublicKey(priv *[32]byte) (*[32]byte, error) {
	pubBytes, err := curve25519.X25519(priv[:], curve25519.Basepoint)
	if err != nil {
		return nil, fmt.Errorf("bad key: %w", err)
	}
	var pub [32]byte
	copy(pub[:], pubBytes)
	return &pub, nil
}

// IsEncrypted reports whether data is a sealed payload (the content entry).
func IsEncrypted(data []byte) bool {
	return bytes.HasPrefix(data, magic)
}

// Seal produces the sealed payload: plaintext sealed anonymously to recipientPub,
// prefixed with the envelope header carrying that public key. Callers usually
// want SealPluginZip.
func Seal(plaintext []byte, recipientPub *[32]byte) ([]byte, error) {
	sealed, err := box.SealAnonymous(nil, plaintext, recipientPub, rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("sealing plugin: %w", err)
	}
	out := make([]byte, 0, len(magic)+1+32+len(sealed))
	out = append(out, magic...)
	out = append(out, envelopeVersion)
	out = append(out, recipientPub[:]...)
	out = append(out, sealed...)
	return out, nil
}

// SealPluginZip wraps a plaintext plugin zip into a sealed plugin: an ordinary zip
// that keeps the plugin's folder, with the manifest left plaintext (readable
// identity) and the whole plugin sealed to recipientPub beside it — see the
// package doc. The input must be a fylr plugin zip: files under a single top-level
// folder <name>/, with a <name>/manifest.yml.
func SealPluginZip(pluginZip []byte, recipientPub *[32]byte) ([]byte, error) {
	name, manifest, err := folderAndManifest(pluginZip)
	if err != nil {
		return nil, err
	}
	sealed, err := Seal(pluginZip, recipientPub)
	if err != nil {
		return nil, err
	}
	buf := &bytes.Buffer{}
	zw := zip.NewWriter(buf)
	// human-readable hint for anyone inspecting the artifact (unzip -z): which
	// recipient key opens it — the same public key the envelope carries
	if err := zw.SetComment(fmt.Sprintf("fylr sealed plugin (envelope v%d), sealed for recipient %s",
		envelopeVersion, base64.StdEncoding.EncodeToString(recipientPub[:]))); err != nil {
		return nil, fmt.Errorf("sealing plugin zip: %w", err)
	}
	sealedAt := time.Now()
	if err := writeEntry(zw, name+"/manifest.yml", manifest, sealedAt); err != nil {
		return nil, fmt.Errorf("sealing plugin zip: %w", err)
	}
	if err := writeEntry(zw, name+"/"+contentEntryName, sealed, sealedAt); err != nil {
		return nil, fmt.Errorf("sealing plugin zip: %w", err)
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("sealing plugin zip: %w", err)
	}
	return buf.Bytes(), nil
}

// folderAndManifest returns a fylr plugin zip's top-level folder <name> and the
// bytes of its manifest, read from the known path <name>/manifest.yml (not
// searched for).
func folderAndManifest(pluginZip []byte) (name string, manifest []byte, err error) {
	zr, err := zip.NewReader(bytes.NewReader(pluginZip), int64(len(pluginZip)))
	if err != nil {
		return "", nil, fmt.Errorf("reading plugin zip: %w", err)
	}
	for _, f := range zr.File {
		if i := strings.IndexByte(f.Name, '/'); i > 0 {
			name = f.Name[:i]
			break
		}
	}
	if name == "" {
		return "", nil, fmt.Errorf("plugin zip has no <name>/ folder")
	}
	f, err := zr.Open(name + "/manifest.yml")
	if err != nil {
		return "", nil, fmt.Errorf("plugin zip: %w", err)
	}
	defer f.Close()
	manifest, err = io.ReadAll(f)
	if err != nil {
		return "", nil, fmt.Errorf("reading manifest: %w", err)
	}
	return name, manifest, nil
}

// writeEntry adds a deflated entry stamped with modified. The explicit timestamp
// matters: zip.Writer.Create leaves Modified zero, which archivers render as the
// MS-DOS zero date ("1979 Nov 30") — so a sealed plugin would show garbage dates
// in Finder. The sealed box is randomised per run, so the artifact is never
// byte-reproducible anyway; a real timestamp costs nothing.
func writeEntry(zw *zip.Writer, name string, data []byte, modified time.Time) error {
	w, err := zw.CreateHeader(&zip.FileHeader{
		Name:     name,
		Method:   zip.Deflate,
		Modified: modified,
	})
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

// PluginInfo describes a sealed plugin artifact, read without any key.
type PluginInfo struct {
	Name         string    // plugin name (the folder / manifest identity)
	Sealed       bool      // false for a plain plugin zip
	RecipientPub *[32]byte // recipient public key from the envelope; nil when not sealed
}

// Inspect reads a plugin artifact's identity and — for a sealed plugin — the
// recipient public key from the envelope. No key material is needed.
func Inspect(artifact []byte) (info PluginInfo, err error) {
	name, _, err := folderAndManifest(artifact)
	if err != nil {
		return info, err
	}
	info.Name = name
	zr, err := zip.NewReader(bytes.NewReader(artifact), int64(len(artifact)))
	if err != nil {
		return info, err
	}
	for _, f := range zr.File {
		if path.Base(f.Name) != contentEntryName {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return info, fmt.Errorf("opening sealed plugin: %w", err)
		}
		payload, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return info, fmt.Errorf("reading sealed plugin: %w", err)
		}
		recipientPub, _, err := parse(payload)
		if err != nil {
			return info, err
		}
		info.Sealed = true
		info.RecipientPub = recipientPub
		return info, nil
	}
	return info, nil
}

// OpenPluginZip returns the plaintext plugin zip from an artifact. If the
// artifact is a sealed plugin, the private key matching the envelope's recipient
// public key is picked from privKeys and the inner plugin zip is returned with
// sealed=true; a plain plugin zip (or any non-sealed artifact) is returned
// unchanged with sealed=false.
func OpenPluginZip(artifact []byte, privKeys ...*[32]byte) (pluginZip []byte, sealed bool, err error) {
	zr, zerr := zip.NewReader(bytes.NewReader(artifact), int64(len(artifact)))
	if zerr != nil {
		return artifact, false, nil
	}
	for _, f := range zr.File {
		if path.Base(f.Name) != contentEntryName {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, false, fmt.Errorf("opening sealed plugin: %w", err)
		}
		payload, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, false, fmt.Errorf("reading sealed plugin: %w", err)
		}
		plain, err := openPayload(payload, privKeys)
		if err != nil {
			return nil, false, err
		}
		return plain, true, nil
	}
	return artifact, false, nil
}

func openPayload(payload []byte, privKeys []*[32]byte) ([]byte, error) {
	recipientPub, sealed, err := parse(payload)
	if err != nil {
		return nil, err
	}
	for _, priv := range privKeys {
		if priv == nil {
			continue
		}
		pub, err := PublicKey(priv)
		if err != nil {
			continue
		}
		if *pub != *recipientPub {
			continue
		}
		opened, ok := box.OpenAnonymous(nil, sealed, recipientPub, priv)
		if !ok {
			return nil, fmt.Errorf("sealed plugin: decryption failed (tampered package)")
		}
		return opened, nil
	}
	return nil, fmt.Errorf("sealed plugin: no key available for recipient %s", base64.StdEncoding.EncodeToString(recipientPub[:]))
}

func parse(data []byte) (recipientPub *[32]byte, sealed []byte, err error) {
	if !IsEncrypted(data) {
		return nil, nil, fmt.Errorf("not a sealed plugin payload")
	}
	rest := data[len(magic):]
	if len(rest) < 1+32 {
		return nil, nil, fmt.Errorf("sealed plugin: truncated envelope")
	}
	if rest[0] != envelopeVersion {
		return nil, nil, fmt.Errorf("sealed plugin: unsupported envelope version %d", rest[0])
	}
	recipientPub = new([32]byte)
	copy(recipientPub[:], rest[1:33])
	return recipientPub, rest[33:], nil
}
