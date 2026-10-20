package pnpminstall

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/paketo-buildpacks/packit/v2/servicebindings"
)

// BindingResolver finds service bindings of a type.
type BindingResolver interface {
	Resolve(typ, provider, platformDir string) ([]servicebindings.Binding, error)
}

// bindingFile returns the path of entry in the single binding of type typ,
// or "" when there is no such binding.
func bindingFile(resolver BindingResolver, typ, platformDir, entry string) (string, error) {
	bindings, err := resolver.Resolve(typ, "", platformDir)
	if err != nil {
		return "", err
	}
	switch len(bindings) {
	case 0:
		return "", nil
	case 1:
		if _, ok := bindings[0].Entries[entry]; !ok {
			return "", fmt.Errorf("binding of type %q has no %q entry", typ, entry)
		}
		return filepath.Join(bindings[0].Path, entry), nil
	default:
		return "", fmt.Errorf("found %d bindings of type %q; expected at most one", len(bindings), typ)
	}
}

// linkConfig links npmrc/pnpmrc bindings into the home directory for the
// duration of the build, so registry credentials never land in a layer.
// pnpm reads its settings from .npmrc, so a lone pnpmrc binding is linked
// there as well. The returned function removes the links.
func linkConfig(resolver BindingResolver, platformDir, home string) (func() error, error) {
	npmrc, err := bindingFile(resolver, "npmrc", platformDir, ".npmrc")
	if err != nil {
		return nil, err
	}
	pnpmrc, err := bindingFile(resolver, "pnpmrc", platformDir, ".pnpmrc")
	if err != nil {
		return nil, err
	}

	links := map[string]string{}
	if npmrc != "" {
		links[filepath.Join(home, ".npmrc")] = npmrc
	}
	if pnpmrc != "" {
		links[filepath.Join(home, ".pnpmrc")] = pnpmrc
		if npmrc == "" {
			links[filepath.Join(home, ".npmrc")] = pnpmrc
		}
	}

	var created []string
	cleanup := func() error {
		var errs []error
		for _, link := range created {
			if err := removeSymlink(link); err != nil {
				errs = append(errs, err)
			}
		}
		return errors.Join(errs...)
	}
	for link, target := range links {
		if err := os.Symlink(target, link); err != nil {
			return nil, errors.Join(fmt.Errorf("linking %s: %w", link, err), cleanup())
		}
		created = append(created, link)
	}
	return cleanup, nil
}

func removeSymlink(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return fmt.Errorf("refusing to remove %s: not a symlink", path)
	}
	return os.Remove(path)
}

// linkNodeModules points <project>/node_modules at the layer through a link
// in tmpDir: the project directory is exported as it is, and the
// setup-symlinks exec.d helper re-creates the tmpDir link at launch so it
// targets the launch layer.
func linkNodeModules(projectDir, layerDir, tmpDir string) error {
	projectLink := filepath.Join(projectDir, "node_modules")
	tmpLink := filepath.Join(tmpDir, "node_modules")
	for _, p := range []string{projectLink, tmpLink} {
		if err := os.RemoveAll(p); err != nil {
			return err
		}
	}
	if err := os.Symlink(tmpLink, projectLink); err != nil {
		return err
	}
	return os.Symlink(filepath.Join(layerDir, "node_modules"), tmpLink)
}
