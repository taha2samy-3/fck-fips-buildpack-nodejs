package apk

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"strings"
	"sync"
	"testing"
)

const testKeyName = "test.rsa.pub"

var (
	keyOnce sync.Once
	testKey *rsa.PrivateKey
)

// signingKey returns one RSA key shared by all tests; generating keys is slow.
func signingKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	keyOnce.Do(func() {
		var err error
		if testKey, err = rsa.GenerateKey(rand.Reader, 2048); err != nil {
			panic(err)
		}
	})
	return testKey
}

func publicPEM(t *testing.T, key *rsa.PrivateKey) []byte {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
}

func testKeyring(t *testing.T) Keyring {
	t.Helper()
	k := Keyring{}
	if err := k.Add(testKeyName, publicPEM(t, signingKey(t))); err != nil {
		t.Fatal(err)
	}
	return k
}

type entry struct {
	name     string
	body     string
	typeflag byte
	linkname string
	mode     int64
}

// member gzips a tar stream holding entries, as one apk segment.
func member(t *testing.T, entries ...entry) []byte {
	t.Helper()
	var tarBuf bytes.Buffer
	tw := tar.NewWriter(&tarBuf)
	for _, e := range entries {
		hdr := &tar.Header{Name: e.name, Mode: e.mode, Size: int64(len(e.body)), Typeflag: e.typeflag, Linkname: e.linkname}
		if hdr.Typeflag == 0 {
			hdr.Typeflag = tar.TypeReg
		}
		if hdr.Mode == 0 {
			hdr.Mode = 0o644
		}
		if hdr.Typeflag != tar.TypeReg {
			hdr.Size = 0
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if hdr.Typeflag == tar.TypeReg {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	zw.Write(tarBuf.Bytes())
	zw.Close()
	return gz.Bytes()
}

func sign(t *testing.T, key *rsa.PrivateKey, data []byte) []byte {
	t.Helper()
	sum := sha256.Sum256(data)
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	return member(t, entry{name: ".SIGN.RSA256." + testKeyName, body: string(sig)})
}

// buildPackage returns a signed .apk and the index entry that describes it.
func buildPackage(t *testing.T, name, version string, files ...entry) ([]byte, Package) {
	t.Helper()
	data := member(t, files...)
	dataHash := sha256.Sum256(data)
	info := fmt.Sprintf("# generated\npkgname = %s\npkgver = %s\ndatahash = %s\n", name, version, hex.EncodeToString(dataHash[:]))
	control := member(t, entry{name: ".PKGINFO", body: info})
	controlHash := sha1.Sum(control)

	raw := append(append(sign(t, signingKey(t), control), control...), data...)
	return raw, Package{
		Name:     name,
		Version:  version,
		Checksum: "Q1" + base64.StdEncoding.EncodeToString(controlHash[:]),
	}
}

// buildIndex returns a signed APKINDEX.tar.gz listing pkgs.
func buildIndex(t *testing.T, pkgs ...Package) []byte {
	t.Helper()
	var b strings.Builder
	for _, p := range pkgs {
		fmt.Fprintf(&b, "C:%s\nP:%s\nV:%s\nA:x86_64\nL:MIT\n", p.Checksum, p.Name, p.Version)
		if p.Origin != "" {
			fmt.Fprintf(&b, "o:%s\n", p.Origin)
		}
		if len(p.Depends) > 0 {
			fmt.Fprintf(&b, "D:%s\n", strings.Join(p.Depends, " "))
		}
		if len(p.Provides) > 0 {
			fmt.Fprintf(&b, "p:%s\n", strings.Join(p.Provides, " "))
		}
		b.WriteString("\n")
	}
	index := member(t, entry{name: "DESCRIPTION"}, entry{name: "APKINDEX", body: b.String()})
	return append(sign(t, signingKey(t), index), index...)
}
