package main

import (
	"crypto/sha1" // #nosec G505 -- the fingerprint OpenPGP itself gives a key of this version
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The key a stranger compares is the key the installer carries: the README
// prints the signer line of settings.txt and its fingerprint, and the
// fingerprint of the key apt is given.
func TestTheReadmePrintsTheKeysAReleaseIsMadeWith(t *testing.T) {
	root := filepath.Join("..", "..")
	set, err := settings(filepath.Join(root, "build", "release", "settings.txt"))
	if err != nil {
		t.Fatal(err)
	}
	readme, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !signerRE.MatchString(set["signer"]) || !strings.Contains(string(readme), "\n"+set["signer"]+"\n") {
		t.Errorf("the README does not print the signer line of settings.txt: %q", set["signer"])
	}
	key, err := base64.StdEncoding.DecodeString(set["signer"][strings.LastIndex(set["signer"], " ")+1:])
	if err != nil {
		t.Fatal(err)
	}
	print := sha256.Sum256(key)
	if want := "\nSHA256:" + base64.RawStdEncoding.EncodeToString(print[:]) + "\n"; !strings.Contains(string(readme), want) {
		t.Errorf("the README does not print the release key's fingerprint%s", want)
	}
	// The apt key: the first packet of the armoured file is the public key,
	// and its fingerprint is the SHA-1 of a mark, its length and its bytes.
	armour, err := os.ReadFile(filepath.Join(root, "build", "release", aptKey))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(armour), "-----BEGIN PGP PUBLIC KEY BLOCK-----\n") || strings.Contains(string(armour), "PRIVATE") {
		t.Fatalf("%s is not the public half of a key", aptKey)
	}
	var body string
	for _, line := range strings.Split(string(armour), "\n")[1:] {
		if line != "" && !strings.HasPrefix(line, "=") && !strings.HasPrefix(line, "-") && !strings.Contains(line, ":") {
			body += line
		}
	}
	packets, err := base64.StdEncoding.DecodeString(body)
	if err != nil || len(packets) < 3 || packets[0] != 0x98 {
		t.Fatalf("%s does not begin with a public key: %v", aptKey, err)
	}
	n := int(packets[1])
	mark := sha1.Sum(append([]byte{0x99, 0, packets[1]}, packets[2:2+n]...))
	var spaced []string
	for i := 0; i < len(mark); i += 2 {
		spaced = append(spaced, fmt.Sprintf("%02X%02X", mark[i], mark[i+1]))
	}
	for _, name := range []string{"README.md", filepath.Join("build", "release", "README.md")} {
		text, _ := os.ReadFile(filepath.Join(root, name))
		if !strings.Contains(string(text), strings.Join(spaced, " ")) {
			t.Errorf("%s does not print the fingerprint of %s: %s", name, aptKey, strings.Join(spaced, " "))
		}
	}
}
