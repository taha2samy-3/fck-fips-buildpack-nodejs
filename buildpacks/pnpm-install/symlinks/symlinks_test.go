package symlinks

import (
	"os"
	"path/filepath"
	"testing"
)

// Ported from yarn-install's cmd/setup-symlinks tests.
func TestRestore(t *testing.T) {
	setup := func(t *testing.T) (layer, project, tmpLink string) {
		layer, project = t.TempDir(), t.TempDir()
		os.MkdirAll(filepath.Join(layer, "node_modules"), 0o755)
		tmpLink = filepath.Join(t.TempDir(), "node_modules")
		if err := os.Symlink(tmpLink, filepath.Join(project, "node_modules")); err != nil {
			t.Fatal(err)
		}
		return layer, project, tmpLink
	}

	t.Run("recreates the dangling tmp link", func(t *testing.T) {
		layer, project, tmpLink := setup(t)
		if err := Restore(layer, project); err != nil {
			t.Fatal(err)
		}
		if target, _ := os.Readlink(tmpLink); target != filepath.Join(layer, "node_modules") {
			t.Errorf("tmp link -> %q", target)
		}
	})

	t.Run("leaves a working link alone", func(t *testing.T) {
		layer, project, tmpLink := setup(t)
		other := t.TempDir()
		os.Symlink(other, tmpLink)
		if err := Restore(layer, project); err != nil {
			t.Fatal(err)
		}
		if target, _ := os.Readlink(tmpLink); target != other {
			t.Errorf("working link was replaced: %q", target)
		}
	})

	t.Run("leaves a real node_modules directory alone", func(t *testing.T) {
		project := t.TempDir()
		os.MkdirAll(filepath.Join(project, "node_modules"), 0o755)
		if err := Restore(t.TempDir(), project); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("no node_modules at all", func(t *testing.T) {
		if err := Restore(t.TempDir(), t.TempDir()); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("relative link", func(t *testing.T) {
		layer, project := t.TempDir(), t.TempDir()
		os.Symlink("cache/node_modules", filepath.Join(project, "node_modules"))
		if err := Restore(layer, project); err != nil {
			t.Fatal(err)
		}
		if target, _ := os.Readlink(filepath.Join(project, "cache", "node_modules")); target != filepath.Join(layer, "node_modules") {
			t.Errorf("relative link not resolved against the project: %q", target)
		}
	})
}
