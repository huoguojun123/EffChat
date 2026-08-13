package handler

import (
	"os"
	"path/filepath"
	"testing"
)

func TestManagedUploadPathRejectsOutsideUploads(t *testing.T) {
	if _, err := managedUploadPath(filepath.Join("..", "outside.txt")); err == nil {
		t.Fatal("expected path outside storage to be rejected")
	}
	if _, err := managedUploadPath(uploadDir); err == nil {
		t.Fatal("expected storage directory itself to be rejected")
	}
}

func TestRemoveManagedFilePathsDeduplicatesTextSidecar(t *testing.T) {
	userDir := filepath.Join(uploadDir, "delete-test")
	if err := os.MkdirAll(userDir, 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(userDir) })

	path := filepath.Join(userDir, "note.txt")
	if err := os.WriteFile(path, []byte("hello"), 0600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	extracted := path

	if err := removeManagedFilePaths(path, &extracted); err != nil {
		t.Fatalf("removeManagedFilePaths: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("file should be removed, stat err=%v", err)
	}
}
