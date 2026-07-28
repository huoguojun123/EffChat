//go:build linux

package handler

import "syscall"

func statfsFragmentSize(stat syscall.Statfs_t) uint64 {
	if stat.Frsize > 0 {
		return uint64(stat.Frsize)
	}
	return uint64(stat.Bsize)
}
