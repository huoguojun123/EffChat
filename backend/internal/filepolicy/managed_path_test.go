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

func TestWriteFileUsesPrivateAttachmentModes(t *testing.T) {
	tempRoot, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve temp root: %v", err)
	}
	root := filepath.Join(tempRoot, "storage")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatalf("mkdir storage root: %v", err)
	}
	path := filepath.Join(root, "attachments", "user", "month", "fixture.png")
	if err := writeFileUnder(root, path, []byte("fixture"), 0o600); err != nil {
		t.Fatalf("writeFileUnder: %v", err)
	}

	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat file: %v", err)
	}
	if got := fileInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("file mode=%#o, want 0600", got)
	}
	for _, dir := range []string{
		filepath.Join(root, "attachments"),
		filepath.Join(root, "attachments", "user"),
		filepath.Join(root, "attachments", "user", "month"),
	} {
		info, statErr := os.Stat(dir)
		if statErr != nil {
			t.Fatalf("stat directory %s: %v", dir, statErr)
		}
		if got := info.Mode().Perm(); got != 0o700 {
			t.Fatalf("directory %s mode=%#o, want 0700", dir, got)
		}
	}
}
