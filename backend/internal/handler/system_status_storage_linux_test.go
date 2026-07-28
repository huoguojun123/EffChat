//go:build linux

package handler

import (
	"syscall"
	"testing"
)

func TestStatfsFragmentSizePrefersFilesystemFragmentSize(t *testing.T) {
	stat := syscall.Statfs_t{Bsize: 1024 * 1024, Frsize: 4096}
	if got := statfsFragmentSize(stat); got != 4096 {
		t.Fatalf("statfsFragmentSize() = %d, want 4096", got)
	}

	stat.Frsize = 0
	if got := statfsFragmentSize(stat); got != 1024*1024 {
		t.Fatalf("statfsFragmentSize() fallback = %d, want %d", got, 1024*1024)
	}
}
