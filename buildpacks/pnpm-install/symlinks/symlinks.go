// Package symlinks restores the node_modules link chain at launch.
package symlinks

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// Restore makes <project>/node_modules resolve to <layer>/node_modules.
// It does nothing when node_modules is a real directory or its link target
// already exists.
func Restore(layer, project string) error {
	nodeModules := filepath.Join(project, "node_modules")
	info, err := os.Lstat(nodeModules)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return nil
	}

	target, err := os.Readlink(nodeModules)
	if err != nil {
		return err
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(project, target)
	}
	if info, err := os.Stat(target); err == nil && info.IsDir() {
		return nil
	}

	if err := os.RemoveAll(target); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	return os.Symlink(filepath.Join(layer, "node_modules"), target)
}
