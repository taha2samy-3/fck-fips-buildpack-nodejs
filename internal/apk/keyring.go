package apk

import (
	"crypto"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
)

// Keyring holds the RSA keys a repository may be signed with, by the file
// name apk uses for them ("wolfi-signing.rsa.pub").
type Keyring map[string]*rsa.PublicKey

var errUnknownKey = errors.New("signed with a key that is not trusted")

// Add parses a PEM encoded RSA public key and trusts it under name.
func (k Keyring) Add(name string, pemData []byte) error {
	block, _ := pem.Decode(pemData)
	if block == nil {
		return fmt.Errorf("key %s: no PEM data", name)
	}

	var pub any
	var err error
	switch block.Type {
	case "PUBLIC KEY":
		pub, err = x509.ParsePKIXPublicKey(block.Bytes)
	case "RSA PUBLIC KEY":
		pub, err = x509.ParsePKCS1PublicKey(block.Bytes)
	default:
		return fmt.Errorf("key %s: unexpected PEM block %q", name, block.Type)
	}
	if err != nil {
		return fmt.Errorf("key %s: %w", name, err)
	}

	rsaKey, ok := pub.(*rsa.PublicKey)
	if !ok {
		return fmt.Errorf("key %s: not an RSA key", name)
	}
	k[name] = rsaKey
	return nil
}

// signatureHash maps a signature entry name (".SIGN.RSA256.<key>") to the
// digest it covers and the key it names. SHA-1 signatures (".SIGN.RSA.") are
// refused: apk-tools still accepts them, but they no longer protect anything.
func signatureHash(entry string) (crypto.Hash, string, error) {
	rest, ok := strings.CutPrefix(entry, ".SIGN.")
	if !ok {
		return 0, "", fmt.Errorf("%q is not a signature entry", entry)
	}
	algo, key, ok := strings.Cut(rest, ".")
	if !ok || key == "" {
		return 0, "", fmt.Errorf("malformed signature entry %q", entry)
	}
	switch algo {
	case "RSA256":
		return crypto.SHA256, key, nil
	case "RSA512":
		return crypto.SHA512, key, nil
	case "RSA":
		return 0, "", fmt.Errorf("signature %q uses SHA-1, which is not accepted", entry)
	default:
		return 0, "", fmt.Errorf("signature %q uses unsupported algorithm %s", entry, algo)
	}
}

// verify checks sig against digest with the key named in entry.
func (k Keyring) verify(entry string, sig []byte, h crypto.Hash, digest []byte) error {
	_, keyName, err := signatureHash(entry)
	if err != nil {
		return err
	}
	key, ok := k[keyName]
	if !ok {
		return fmt.Errorf("%w: %s", errUnknownKey, keyName)
	}
	if err := rsa.VerifyPKCS1v15(key, h, digest, sig); err != nil {
		return fmt.Errorf("signature by %s does not verify: %w", keyName, err)
	}
	return nil
}
