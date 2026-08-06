// Sealing (the former fylr-seal-plugin, merged here): a sealed plugin is still
// a valid zip that keeps the plugin folder with a plaintext manifest, the
// whole plugin encrypted to fylr's public key beside it. Sealing uses a PUBLIC
// key only, so it runs in any (public) build pipeline without a secret; the
// private key lives in fylr. fylr imports the pluginseal package to open
// sealed plugins — one source of truth, no drift.
package main

import (
	"encoding/base64"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/programmfabrik/fylr-build-plugin/pluginseal"
)

// devPublicKeyB64 is the fylr dev/CI plugin key (public half). A plugin sealed
// with it opens ONLY on a fylr built with -tags licensetest.
const devPublicKeyB64 = "U+r3S0M+V6otFismIZP9mjFuWR5wJjusz6mjeCw+NGw="

func runSeal(args []string) error {
	fs := flag.NewFlagSet("seal", flag.ContinueOnError)
	in := fs.String("in", "", "plugin zip to seal (default: the zip this repo builds; built first if missing)")
	out := fs.String("out", "", `output path (default: "<in>_sealed.zip")`)
	release := fs.String("release", "", "release tag written to build-info.json when the zip is built first")
	pubKey := fs.String("pubkey", "", "recipient public key: base64 (32-byte X25519) or a file path; default = fylr dev/CI key (opens only on -tags licensetest builds)")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, "Usage: fylr-build-plugin seal [flags]\n\n"+
			"Seal a plugin zip for the fylr Plugin Marketplace. Needs only the\n"+
			"recipient's PUBLIC key — safe in public CI. Without -in, builds the\n"+
			"plugin zip first when it is not there yet.\n\nFlags:\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	inPath := *in
	if inPath == "" {
		p, err := loadPlugin()
		if err != nil {
			return err
		}
		inPath = defaultZipPath(p)
		if _, err := os.Stat(inPath); err != nil {
			if err := build(p, *release); err != nil {
				return err
			}
			if err := zipPlugin(p); err != nil {
				return err
			}
		}
	}
	outPath := *out
	if outPath == "" {
		outPath = sealedName(inPath)
	}

	pub, err := loadPubKey(*pubKey)
	if err != nil {
		return err
	}
	plaintext, err := os.ReadFile(inPath)
	if err != nil {
		return err
	}
	sealed, err := pluginseal.SealPluginZip(plaintext, pub)
	if err != nil {
		return err
	}
	if err := os.WriteFile(outPath, sealed, 0o644); err != nil {
		return err
	}
	fmt.Printf("sealed %s -> %s (%d bytes, a valid zip; recipient %s)\n",
		inPath, outPath, len(sealed), base64.StdEncoding.EncodeToString(pub[:]))
	return nil
}

// runGenkey prints a fresh keypair: seal plugins with the public half, give the
// private half to fylr (embedded, or on a marketplace source in fylr.yml).
func runGenkey(args []string) error {
	fs := flag.NewFlagSet("genkey", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	pub, priv, err := pluginseal.GenerateKey()
	if err != nil {
		return err
	}
	fmt.Printf("public  (seal with -pubkey): %s\n", base64.StdEncoding.EncodeToString(pub[:]))
	fmt.Printf("private (give to fylr):      %s\n", base64.StdEncoding.EncodeToString(priv[:]))
	return nil
}

// runInfo prints a plugin artifact's identity and, when sealed, the recipient
// public key needed to open it. No key material required.
func runInfo(args []string) error {
	fs := flag.NewFlagSet("info", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, "Usage: fylr-build-plugin info <plugin.zip>\n")
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return fmt.Errorf("exactly one zip path expected")
	}
	data, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		return err
	}
	pi, err := pluginseal.Inspect(data)
	if err != nil {
		return err
	}
	fmt.Printf("plugin:    %s\n", pi.Name)
	if !pi.Sealed {
		fmt.Printf("sealed:    no (plain plugin zip)\n")
		return nil
	}
	fmt.Printf("sealed:    yes\n")
	fmt.Printf("recipient: %s\n", base64.StdEncoding.EncodeToString(pi.RecipientPub[:]))
	return nil
}

// loadPubKey reads the recipient public key from a base64 string, a file
// containing base64, or falls back to the dev/CI key when spec is empty.
func loadPubKey(spec string) (*[32]byte, error) {
	b64 := spec
	if b64 == "" {
		b64 = devPublicKeyB64
	} else if fi, err := os.Stat(spec); err == nil && !fi.IsDir() {
		data, err := os.ReadFile(spec)
		if err != nil {
			return nil, fmt.Errorf("reading -pubkey file: %w", err)
		}
		b64 = string(data)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil {
		return nil, fmt.Errorf("decoding public key: %w", err)
	}
	if len(raw) != 32 {
		return nil, fmt.Errorf("public key must be 32 bytes, got %d", len(raw))
	}
	var pub [32]byte
	copy(pub[:], raw)
	return &pub, nil
}
