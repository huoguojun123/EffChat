package service

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestReadFilePreviewChunkPreservesUTF8Boundaries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preview.md")
	if err := os.WriteFile(path, []byte("甲乙A\n丙丁B"), 0o600); err != nil {
		t.Fatal(err)
	}

	first, err := ReadFilePreviewChunk(path, "", 3)
	if err != nil {
		t.Fatal(err)
	}
	if first.Content != "甲乙A" || !first.HasMore || first.NextCursor == "" {
		t.Fatalf("first chunk = %+v", first)
	}
	second, err := ReadFilePreviewChunk(path, first.NextCursor, 3)
	if err != nil {
		t.Fatal(err)
	}
	if second.Content != "\n丙丁" || !second.HasMore || second.NextCursor == "" {
		t.Fatalf("second chunk = %+v", second)
	}
	third, err := ReadFilePreviewChunk(path, second.NextCursor, 3)
	if err != nil {
		t.Fatal(err)
	}
	if third.Content != "B" || third.HasMore || third.NextCursor != "" {
		t.Fatalf("third chunk = %+v", third)
	}
}

func TestReadFilePreviewChunkRejectsInvalidCursor(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preview.md")
	if err := os.WriteFile(path, []byte("中文"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, cursor := range []string{"not-base64", encodePreviewCursor(1), encodePreviewCursor(99)} {
		if _, err := ReadFilePreviewChunk(path, cursor, 1); !errors.Is(err, ErrInvalidPreviewCursor) {
			t.Fatalf("cursor %q error = %v", cursor, err)
		}
	}
}
