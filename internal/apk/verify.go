package apk

import (
	"archive/tar"
	"bufio"
	"bytes"
	"crypto"
	"crypto/sha1" //nolint:gosec // apk's control checksum is SHA-1; the index that carries it is RSA/SHA-256 signed.
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"strings"

	"github.com/taha2samy-3/fck-fips-buildpack-nodejs/internal/archive"
)

const maxSignatureSize = 64 * 1024

// ReadSignedIndex verifies the signature of an APKINDEX.tar.gz against keys
// and returns its packages. An unsigned index, or one signed by a key that is
// not in keys, is rejected.
func ReadSignedIndex(r io.Reader, keys Keyring) ([]Package, error) {
	s := newSegments(r)

	gz, err := s.next()
	if err != nil {
		return nil, fmt.Errorf("reading index signature: %w", err)
	}
	sigName, sig, err := readSignature(tar.NewReader(gz))
	if err != nil {
		return nil, fmt.Errorf("index is not signed: %w", err)
	}
	if err := s.finish(); err != nil {
		return nil, err
	}
	h, _, err := signatureHash(sigName)
	if err != nil {
		return nil, err
	}

	digest := h.New()
	gz, err = s.next(digest)
	if err != nil {
		return nil, fmt.Errorf("reading index: %w", err)
	}
	var pkgs []Package
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading index: %w", err)
		}
		if hdr.Name == "APKINDEX" {
			if pkgs, err = ParseIndex(tr); err != nil {
				return nil, err
			}
		}
	}
	if err := s.finish(); err != nil {
		return nil, err
	}

	if err := keys.verify(sigName, sig, h, digest.Sum(nil)); err != nil {
		return nil, fmt.Errorf("index: %w", err)
	}
	if _, err := s.next(); !errors.Is(err, io.EOF) {
		return nil, errors.New("index has data after the signed segment")
	}
	if pkgs == nil {
		return nil, errors.New("index has no APKINDEX entry")
	}
	return pkgs, nil
}

// readSignature reads the first entry of a signature segment.
func readSignature(tr *tar.Reader) (string, []byte, error) {
	hdr, err := tr.Next()
	if err != nil {
		return "", nil, err
	}
	if !strings.HasPrefix(hdr.Name, ".SIGN.") {
		return "", nil, fmt.Errorf("first entry is %q, not a signature", hdr.Name)
	}
	sig, err := io.ReadAll(io.LimitReader(tr, maxSignatureSize))
	if err != nil {
		return "", nil, err
	}
	return hdr.Name, sig, nil
}

// Verify checks a downloaded .apk against its entry in a signed index:
//
//   - the SHA-1 of the control segment must equal the index checksum (C:),
//   - a package signature, when present and made with a trusted key, must
//     verify,
//   - the SHA-256 of the data segment must equal the "datahash" recorded in
//     the control segment.
//
// Together with ReadSignedIndex this is the same chain of trust apk-tools
// applies before it installs anything.
func Verify(r io.Reader, pkg Package, keys Keyring) error {
	wantControl, err := pkg.controlSHA1()
	if err != nil {
		return err
	}

	s := newSegments(r)
	newHashes := func() (hash.Hash, hash.Hash, hash.Hash) {
		return sha1.New(), sha256.New(), sha512.New() //nolint:gosec
	}

	controlSHA1, controlSHA256, controlSHA512 := newHashes()
	gz, err := s.next(controlSHA1, controlSHA256, controlSHA512)
	if err != nil {
		return fmt.Errorf("%s: %w", pkg, err)
	}
	tr := tar.NewReader(gz)
	hdr, err := tr.Next()
	if err != nil {
		return fmt.Errorf("%s: reading first segment: %w", pkg, err)
	}

	var sigName string
	var sig []byte
	if strings.HasPrefix(hdr.Name, ".SIGN.") {
		sigName = hdr.Name
		if sig, err = io.ReadAll(io.LimitReader(tr, maxSignatureSize)); err != nil {
			return fmt.Errorf("%s: reading signature: %w", pkg, err)
		}
		if err := s.finish(); err != nil {
			return fmt.Errorf("%s: %w", pkg, err)
		}

		controlSHA1, controlSHA256, controlSHA512 = newHashes()
		if gz, err = s.next(controlSHA1, controlSHA256, controlSHA512); err != nil {
			return fmt.Errorf("%s: reading control segment: %w", pkg, err)
		}
		tr = tar.NewReader(gz)
		if hdr, err = tr.Next(); err != nil {
			return fmt.Errorf("%s: reading control segment: %w", pkg, err)
		}
	}

	var info map[string]string
	for {
		if hdr.Name == ".PKGINFO" {
			if info, err = parsePkgInfo(tr); err != nil {
				return fmt.Errorf("%s: %w", pkg, err)
			}
		}
		if hdr, err = tr.Next(); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return fmt.Errorf("%s: reading control segment: %w", pkg, err)
		}
	}
	if err := s.finish(); err != nil {
		return fmt.Errorf("%s: %w", pkg, err)
	}

	if !bytes.Equal(controlSHA1.Sum(nil), wantControl) {
		return fmt.Errorf("%s: control segment does not match the checksum in the signed index", pkg)
	}
	if sigName != "" {
		h, _, err := signatureHash(sigName)
		if err == nil {
			digest := controlSHA256.Sum(nil)
			if h == crypto.SHA512 {
				digest = controlSHA512.Sum(nil)
			}
			err = keys.verify(sigName, sig, h, digest)
		}
		// The signed index already vouches for this control segment, so a
		// signature by a key we do not hold is not an error; a bad one is.
		if err != nil && !errors.Is(err, errUnknownKey) {
			return fmt.Errorf("%s: package %w", pkg, err)
		}
	}

	if info == nil {
		return fmt.Errorf("%s: control segment has no .PKGINFO", pkg)
	}
	if info["pkgname"] != pkg.Name || info["pkgver"] != pkg.Version {
		return fmt.Errorf("%s: package identifies itself as %s-%s", pkg, info["pkgname"], info["pkgver"])
	}
	wantData, err := hex.DecodeString(info["datahash"])
	if err != nil || len(wantData) != sha256.Size {
		return fmt.Errorf("%s: .PKGINFO has no valid datahash", pkg)
	}

	dataSHA256 := sha256.New()
	if _, err := s.next(dataSHA256); err != nil {
		return fmt.Errorf("%s: reading data segment: %w", pkg, err)
	}
	if err := s.finish(); err != nil {
		return fmt.Errorf("%s: reading data segment: %w", pkg, err)
	}
	if !bytes.Equal(dataSHA256.Sum(nil), wantData) {
		return fmt.Errorf("%s: data segment does not match its datahash", pkg)
	}
	if _, err := s.next(); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%s: unexpected data after the data segment", pkg)
	}
	return nil
}

// Extract writes the data segment of an .apk below dest. Call it only on a
// file Verify has accepted.
func Extract(r io.Reader, dest string, mapName archive.Mapper) error {
	s := newSegments(r)

	// The data segment follows the control segment, which is itself preceded
	// by an optional signature segment.
	gz, err := s.next()
	if err != nil {
		return fmt.Errorf("reading package: %w", err)
	}
	hdr, err := tar.NewReader(gz).Next()
	if err != nil {
		return fmt.Errorf("reading package: %w", err)
	}
	skip := 1
	if strings.HasPrefix(hdr.Name, ".SIGN.") {
		skip = 2
	}
	if err := s.finish(); err != nil {
		return err
	}
	for range skip - 1 {
		if _, err := s.next(); err != nil {
			return fmt.Errorf("reading package: %w", err)
		}
		if err := s.finish(); err != nil {
			return err
		}
	}

	if gz, err = s.next(); err != nil {
		return fmt.Errorf("package has no data segment: %w", err)
	}
	if err := archive.Extract(tar.NewReader(gz), dest, mapName); err != nil {
		return err
	}
	return s.finish()
}

func parsePkgInfo(r io.Reader) (map[string]string, error) {
	info := make(map[string]string)
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if _, dup := info[key]; !dup {
			info[key] = value
		}
	}
	return info, scanner.Err()
}
