package filepolicy

import (
	"os"
	"path/filepath"
	"testing"
)

func TestManagedPathRejectsOutsideAndRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "storage")
	for _, path := range []string{"../outside.txt", root} {
		if _, err := managedPathUnder(root, path, false); err == nil {
			t.Fatalf("expected %q to be rejected", path)
		}
	}
}

func TestExistingPathRejectsSymlinkEscape(t *testing.T) {
	root := filepath.Join(t.TempDir(), "storage")
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if _, err := existingPathUnder(root, link, false); err == nil {
		t.Fatal("expected symlink outside storage to be rejected")
	}
}

func TestWriteFileRejectsSymlinkEscape(t *testing.T) {
	root := filepath.Join(t.TempDir(), "storage")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.txt")
	link := filepath.Join(root, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if err := writeFileUnder(root, link, []byte("blocked"), 0o600); err == nil {
		t.Fatal("expected symlink write to be rejected")
	}
}
