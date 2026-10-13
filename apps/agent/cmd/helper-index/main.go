// Command helper-index manages the signing key and signatures for the helper
// mod's build index (versions.json). Agents install only builds named by an
// index signed with a key their operator pinned in MCSM_HELPER_INDEX_PUBKEY.
//
//	helper-index keygen -out release            # writes release.key, prints the public key
//	helper-index sign -key release.key versions.json   # writes versions.json.sig
//	helper-index verify -pubkey <base64> versions.json # checks versions.json.sig
//
// Keep the private key out of the repository: store it as a CI secret and
// sign in the release job, after the index is generated from the build.
package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/mcsm/agent/internal/helpermod"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "keygen":
		err = keygen(os.Args[2:])
	case "sign":
		err = sign(os.Args[2:])
	case "verify":
		err = verify(os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "helper-index:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: helper-index keygen -out <name> | sign -key <file> <index> | verify -pubkey <base64> <index>")
	os.Exit(2)
}

func keygen(args []string) error {
	fs := flag.NewFlagSet("keygen", flag.ExitOnError)
	out := fs.String("out", "helper-index", "file name prefix for the private key (<out>.key)")
	_ = fs.Parse(args)
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		return err
	}
	path := *out + ".key"
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%s already exists; refusing to overwrite a signing key", path)
	}
	if err := os.WriteFile(path, []byte(base64.StdEncoding.EncodeToString(priv.Seed())+"\n"), 0o600); err != nil {
		return err
	}
	fmt.Printf("private key written to %s (keep it secret)\n", path)
	fmt.Printf("MCSM_HELPER_INDEX_PUBKEY=%s\n", base64.StdEncoding.EncodeToString(pub))
	return nil
}

func sign(args []string) error {
	fs := flag.NewFlagSet("sign", flag.ExitOnError)
	keyPath := fs.String("key", "", "private key file from keygen")
	_ = fs.Parse(args)
	if *keyPath == "" || fs.NArg() != 1 {
		usage()
	}
	raw, err := os.ReadFile(*keyPath)
	if err != nil {
		return err
	}
	seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil || len(seed) != ed25519.SeedSize {
		return fmt.Errorf("%s is not a key written by keygen", *keyPath)
	}
	index, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		return err
	}
	if _, err := helpermod.ParseIndex(index); err != nil {
		return fmt.Errorf("refusing to sign an index agents cannot read: %w", err)
	}
	out := fs.Arg(0) + helpermod.SignatureSuffix
	if err := os.WriteFile(out, helpermod.SignIndex(ed25519.NewKeyFromSeed(seed), index), 0o644); err != nil {
		return err
	}
	fmt.Printf("signature written to %s\n", out)
	return nil
}

func verify(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ExitOnError)
	pub := fs.String("pubkey", "", "base64 public key(s), comma-separated")
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		usage()
	}
	keys, err := helpermod.ParsePublicKeys(*pub)
	if err != nil || len(keys) == 0 {
		return fmt.Errorf("-pubkey: a base64 ed25519 public key is required")
	}
	index, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		return err
	}
	sig, err := os.ReadFile(fs.Arg(0) + helpermod.SignatureSuffix)
	if err != nil {
		return err
	}
	if !helpermod.VerifyIndex(keys, index, sig) {
		return fmt.Errorf("signature does not verify")
	}
	fmt.Println("signature OK")
	return nil
}
