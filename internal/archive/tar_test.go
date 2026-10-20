package archive

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

type entry struct {
	name, body, link string
	typeflag         byte
	mode             int64
}

func tarOf(t *testing.T, entries ...entry) *tar.Reader {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, e := range entries {
		hdr := &tar.Header{Name: e.name, Linkname: e.link, Typeflag: e.typeflag, Mode: e.mode}
		if hdr.Typeflag == 0 {
			hdr.Typeflag = tar.TypeReg
		}
		if hdr.Mode == 0 {
			hdr.Mode = 0o644
		}
		if hdr.Typeflag == tar.TypeReg {
			hdr.Size = int64(len(e.body))
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		tw.Write([]byte(e.body))
	}
	tw.Close()
	return tar.NewReader(&buf)
}

func TestExtract(t *testing.T) {
	dest := t.TempDir()
	err := Extract(tarOf(t,
		entry{name: "./a/b.txt", body: "b", mode: 0o640},
		entry{name: "a/run", body: "x", mode: 0o755},
		entry{name: "a/hard", typeflag: tar.TypeLink, link: "a/b.txt"},
		entry{name: "a/sym", typeflag: tar.TypeSymlink, link: "b.txt"},
		entry{name: "a/b.txt.new", body: "1"},
		entry{name: "a/b.txt.new", body: "2"}, // later entries replace earlier ones
	), dest, Identity)
	if err != nil {
		t.Fatal(err)
	}

	if b, _ := os.ReadFile(filepath.Join(dest, "a/hard")); string(b) != "b" {
		t.Errorf("hard link content = %q", b)
	}
	if l, _ := os.Readlink(filepath.Join(dest, "a/sym")); l != "b.txt" {
		t.Errorf("symlink = %q", l)
	}
	if b, _ := os.ReadFile(filepath.Join(dest, "a/b.txt.new")); string(b) != "2" {
		t.Errorf("replaced file = %q", b)
	}
	if info, _ := os.Stat(filepath.Join(dest, "a/run")); info.Mode().Perm() != 0o755 {
		t.Errorf("mode = %v", info.Mode())
	}
}

func TestExtractRejectsEscapes(t *testing.T) {
	outside := t.TempDir()
	dest := t.TempDir()

	// "../" and absolute names are dropped.
	if err := Extract(tarOf(t,
		entry{name: "../escape.txt", body: "x"},
		entry{name: "/etc/escape.txt", body: "x"},
	), dest, Identity); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(dest); len(entries) != 0 {
		t.Errorf("unexpected files: %v", entries)
	}

	// A symlink pointing outside followed by a write through it must fail.
	err := Extract(tarOf(t,
		entry{name: "link", typeflag: tar.TypeSymlink, link: outside},
		entry{name: "link/pwned.txt", body: "x"},
	), dest, Identity)
	if err == nil {
		t.Error("write through an escaping symlink succeeded")
	}
	if _, err := os.Stat(filepath.Join(outside, "pwned.txt")); err == nil {
		t.Fatal("file written outside the destination")
	}
}

func TestStripComponents(t *testing.T) {
	strip := StripComponents(1)
	for in, want := range map[string]string{"package/bin/x": "bin/x", "package/a": "a"} {
		if got, ok := strip(in); !ok || got != want {
			t.Errorf("strip(%q) = %q, %t", in, got, ok)
		}
	}
	if _, ok := strip("package"); ok {
		t.Error("top-level directory should be skipped")
	}
}

func TestRelPath(t *testing.T) {
	tests := []struct{ from, to, want string }{
		{"bin", "bin/node", "node"},
		{"bin", "lib/node_modules/npm/bin/npm-cli.js", "../lib/node_modules/npm/bin/npm-cli.js"},
		{".", "lib/x", "lib/x"},
		{"a/b", "a/b", "."},
	}
	for _, tt := range tests {
		if got := relPath(tt.from, tt.to); got != tt.want {
			t.Errorf("relPath(%q, %q) = %q, want %q", tt.from, tt.to, got, tt.want)
		}
	}
}
