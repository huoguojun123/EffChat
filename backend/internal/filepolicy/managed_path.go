package filepolicy

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	StorageRoot             = "./storage"
	AttachmentOriginalsRoot = StorageRoot + "/attachments/originals"
	AttachmentExtractedRoot = StorageRoot + "/attachments/extracted"
	AttachmentOCRRoot       = StorageRoot + "/attachments/ocr-staging"
	AvatarRoot              = StorageRoot + "/avatars"
	FontRoot                = StorageRoot + "/fonts"
	SkillRoot               = StorageRoot + "/skills"
)

const (
	MaxVisionImageBytes   int64 = 8 << 20
	MaxVisionRequestBytes int64 = 12 << 20
	MaxVisionImages             = 4
)

func ExistingPath(path string) (string, error) {
	return existingPathUnder(StorageRoot, path, false)
}

func ManagedPath(path string) (string, error) {
	return managedPathUnder(StorageRoot, path, false)
}

func managedPathUnder(rootPath, path string, allowRoot bool) (string, error) {
	root, err := filepath.Abs(rootPath)
	if err != nil {
		return "", err
	}
	target, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if !isWithin(root, target, allowRoot) {
		return "", fmt.Errorf("path is outside managed storage")
	}
	return target, nil
}

func WriteFile(path string, content []byte, permission os.FileMode) error {
	return writeFileUnder(StorageRoot, path, content, permission)
}

func writeFileUnder(rootPath, path string, content []byte, permission os.FileMode) error {
	clean, err := managedPathUnder(rootPath, path, false)
	if err != nil {
		return err
	}
	// Managed attachments contain user-controlled content. Keep every directory
	// private to the backend owner; API authorization is the product boundary,
	// while filesystem mode is the host-level defense-in-depth boundary.
	if err := os.MkdirAll(filepath.Dir(clean), 0o700); err != nil {
		return err
	}
	parent, err := existingPathUnder(rootPath, filepath.Dir(clean), true)
	if err != nil {
		return err
	}
	if !isWithin(parent, clean, false) {
		return fmt.Errorf("path is outside managed storage")
	}
	if info, err := os.Lstat(clean); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refuse to write through symlink")
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.WriteFile(clean, content, permission)
}

func Remove(path string) error {
	clean, err := ManagedPath(path)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(clean); err != nil {
		return err
	}
	parent, err := existingPathUnder(StorageRoot, filepath.Dir(clean), true)
	if err != nil {
		return err
	}
	if !isWithin(parent, clean, false) {
		return fmt.Errorf("path is outside managed storage")
	}
	return os.Remove(clean)
}

func existingPathUnder(rootPath, path string, allowRoot bool) (string, error) {
	clean, err := managedPathUnder(rootPath, path, allowRoot)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(clean)
	if err != nil {
		return "", err
	}
	root, err := filepath.Abs(rootPath)
	if err != nil {
		return "", err
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	if !isWithin(resolvedRoot, resolved, allowRoot) {
		return "", fmt.Errorf("path is outside managed storage")
	}
	return resolved, nil
}

func isWithin(root, target string, allowRoot bool) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return false
	}
	return allowRoot || rel != "."
}
