// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package evidence

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/RhythmShahriar/rhythm/kit/cache"
)

// ErrInsecure is Sign's refusal of a run made with -k: a signature
// would vouch for pages fetched without checking whose they were.
var ErrInsecure = errors.New("can't sign a run made with -k: TLS wasn't verified")

// maxKey bounds the key file Sign reads.
const maxKey = 64 << 10

// Keygen writes a new ed25519 signing key to path as a PKCS #8 PEM file
// readable by its owner only (0600) and returns the public key, base64,
// for the owner to publish. It never replaces a file, a symbolic link
// included.
func Keygen(path string) (pub string, err error) {
	pk, sk, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", err
	}
	der, err := x509.MarshalPKCS8PrivateKey(sk)
	if err != nil {
		return "", err
	}
	// O_EXCL fails on any existing name, a link too, so the key never
	// lands where a link points.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // the path the owner gave
	if err != nil {
		return "", err
	}
	if err := pem.Encode(f, &pem.Block{Type: "PRIVATE KEY", Bytes: der}); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return base64.StdEncoding.EncodeToString(pk), nil
}

// Sign signs the evidence pack in dir with the private key in keyFile
// (from Keygen): eaa/evidence.sig holds the ed25519 signature of the
// evidence.json bytes and eaa/evidence.pub the public key, both base64.
// It refuses a run made with -k (ErrInsecure).
func Sign(dir, keyFile string) error {
	data, p, err := readPack(dir)
	if err != nil {
		return err
	}
	if p.Insecure {
		return ErrInsecure
	}
	sk, err := readKey(keyFile)
	if err != nil {
		return err
	}
	for _, rel := range []string{SigFile, PubFile} {
		if err := noSymlink(dir, rel); err != nil {
			return err
		}
	}
	sig := ed25519.Sign(sk, data)
	pub := sk.Public().(ed25519.PublicKey)
	if err := cache.WriteFile(filepath.Join(dir, filepath.FromSlash(PubFile)), []byte(base64.StdEncoding.EncodeToString(pub)+"\n")); err != nil {
		return err
	}
	return cache.WriteFile(filepath.Join(dir, filepath.FromSlash(SigFile)), []byte(base64.StdEncoding.EncodeToString(sig)+"\n"))
}

// readKey reads an ed25519 private key in PKCS #8 PEM.
func readKey(path string) (ed25519.PrivateKey, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if fi.Size() > maxKey {
		return nil, fmt.Errorf("%s isn't a signing key (too big)", path)
	}
	data, err := os.ReadFile(path) //nolint:gosec // the path the owner gave
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "PRIVATE KEY" {
		return nil, fmt.Errorf("%s isn't a signing key; make one with rhythm soundcheck evidence keygen", path)
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	sk, ok := k.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("%s isn't an ed25519 key", path)
	}
	return sk, nil
}

// checkSignature verifies eaa/evidence.sig against evidence.json and
// the key in eaa/evidence.pub, when the pack is signed. Whether the key
// is the owner's is for the reader to check against the one the owner
// published.
func checkSignature(dir, prefix string) error {
	sig, err := readB64(dir, SigFile)
	if errors.Is(err, fs.ErrNotExist) {
		if _, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(PubFile))); err == nil {
			return fmt.Errorf("evidence: %s%s is there without %s", prefix, PubFile, SigFile)
		}
		return nil // not signed
	}
	if err != nil {
		return fmt.Errorf("evidence: %s%s: %w", prefix, SigFile, err)
	}
	pub, err := readB64(dir, PubFile)
	if err != nil {
		return fmt.Errorf("evidence: %s%s: %w", prefix, PubFile, err)
	}
	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(File))) //nolint:gosec // the run folder
	if err != nil {
		return err
	}
	if len(pub) != ed25519.PublicKeySize || !ed25519.Verify(pub, data, sig) {
		return fmt.Errorf("evidence: %s%s doesn't match %s (signature invalid)", prefix, SigFile, File)
	}
	return nil
}

// SigningKey returns the public key, base64, in the pack's
// eaa/evidence.pub: the key Verify checked the signature with. Verify
// trusts it as found, so anyone who re-signs a changed pack passes;
// comparing it with the key the owner published is what tells. It is
// "" for a pack that isn't signed.
func SigningKey(dir string) (string, error) {
	pub, err := readB64(dir, PubFile)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("evidence: %s: %w", PubFile, err)
	}
	if len(pub) != ed25519.PublicKeySize {
		return "", fmt.Errorf("evidence: %s isn't an ed25519 public key", PubFile)
	}
	return base64.StdEncoding.EncodeToString(pub), nil
}

// readB64 reads a small base64 file of the pack.
func readB64(dir, rel string) ([]byte, error) {
	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel))) //nolint:gosec // the run folder
	if err != nil {
		return nil, err
	}
	if len(data) > maxKey {
		return nil, errors.New("too big")
	}
	return base64.StdEncoding.DecodeString(strings.TrimSpace(string(data)))
}
