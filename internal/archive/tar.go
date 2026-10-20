// Package archive extracts tar streams without letting an entry escape the
// destination directory.
//
// Every write goes through an os.Root, so neither "../" names nor symlinks
// planted by an earlier entry can redirect a later entry outside the
// destination. That protection does not depend on the archive being trusted.
package archive

import (
	"archive/tar"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
)

// Mapper rewrites an archive entry name (slash separated, relative, already
// cleaned) to its destination name. Returning false skips the entry.
type Mapper func(name string) (string, bool)

// Identity keeps every entry where the archive puts it.
func Identity(name string) (string, bool) { return name, true }

// StripComponents drops the first n path elements, like tar --strip-components.
func StripComponents(n int) Mapper {
	return func(name string) (string, bool) {
		parts := strings.SplitN(name, "/", n+1)
		if len(parts) <= n {
			return "", false
		}
		return parts[n], true
	}
}

// Extract writes the entries of tr below dest, which must already exist.
func Extract(tr *tar.Reader, dest string, mapName Mapper) error {
	root, err := os.OpenRoot(dest)
	if err != nil {
		return err
	}
	defer root.Close()

	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("reading archive: %w", err)
		}

		name, ok := cleanName(hdr.Name)
		if !ok {
			continue
		}
		target, ok := mapName(name)
		if !ok || target == "" || target == "." {
			continue
		}

		if err := extractEntry(root, tr, hdr, name, target, mapName); err != nil {
			return fmt.Errorf("extracting %s: %w", hdr.Name, err)
		}
	}
}

func extractEntry(root *os.Root, r io.Reader, hdr *tar.Header, name, target string, mapName Mapper) error {
	// Only permission bits: no setuid/setgid/sticky bits in a layer.
	mode := fs.FileMode(hdr.Mode & 0o777)

	switch hdr.Typeflag {
	case tar.TypeDir:
		if err := root.MkdirAll(target, 0o755); err != nil {
			return err
		}
		return root.Chmod(target, mode|0o700)

	case tar.TypeReg:
		if err := prepare(root, target); err != nil {
			return err
		}
		f, err := root.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode|0o600)
		if err != nil {
			return err
		}
		if _, err := io.Copy(f, r); err != nil {
			return errors.Join(err, f.Close())
		}
		if err := f.Close(); err != nil {
			return err
		}
		// OpenFile applies the umask; set the archived mode explicitly.
		return root.Chmod(target, mode|0o600)

	case tar.TypeSymlink:
		if err := prepare(root, target); err != nil {
			return err
		}
		return root.Symlink(relink(name, target, hdr.Linkname, mapName), target)

	case tar.TypeLink:
		old, ok := cleanName(hdr.Linkname)
		if !ok {
			return fmt.Errorf("invalid hard link target %q", hdr.Linkname)
		}
		oldTarget, ok := mapName(old)
		if !ok {
			return fmt.Errorf("hard link target %q was not extracted", hdr.Linkname)
		}
		if err := prepare(root, target); err != nil {
			return err
		}
		return root.Link(oldTarget, target)

	default:
		// Devices, FIFOs and PAX/GNU metadata entries have no place in a layer.
		return nil
	}
}

// prepare creates the parent directory of name and removes whatever is
// already at name, so a later package can replace a file from an earlier one.
func prepare(root *os.Root, name string) error {
	if dir := path.Dir(name); dir != "." {
		if err := root.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	if err := root.Remove(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// relink rewrites a symlink so it still points at the right file after the
// mapper has moved both the link and its target. Absolute targets become
// relative ones, because the destination is not the filesystem root.
func relink(name, target, linkname string, mapName Mapper) string {
	var original string
	if path.IsAbs(linkname) {
		original = path.Clean(strings.TrimPrefix(linkname, "/"))
	} else {
		original = path.Join(path.Dir(name), linkname)
		if original == ".." || strings.HasPrefix(original, "../") {
			// Points outside the archive; leave it for the image to resolve.
			return linkname
		}
	}

	mapped, ok := mapName(original)
	if !ok {
		mapped = original
	}
	return relPath(path.Dir(target), mapped)
}

// relPath is filepath.Rel for slash-separated relative paths.
func relPath(fromDir, to string) string {
	from := splitPath(fromDir)
	dst := splitPath(to)
	i := 0
	for i < len(from) && i < len(dst) && from[i] == dst[i] {
		i++
	}
	parts := make([]string, 0, len(from)-i+len(dst)-i)
	for range from[i:] {
		parts = append(parts, "..")
	}
	parts = append(parts, dst[i:]...)
	if len(parts) == 0 {
		return "."
	}
	return strings.Join(parts, "/")
}

func splitPath(p string) []string {
	if p == "." || p == "" {
		return nil
	}
	return strings.Split(p, "/")
}

// cleanName normalises an entry name and rejects names that are absolute or
// climb out of the archive root.
func cleanName(name string) (string, bool) {
	name = path.Clean(strings.TrimPrefix(name, "./"))
	if name == "." || path.IsAbs(name) || name == ".." || strings.HasPrefix(name, "../") {
		return "", false
	}
	return name, true
}
