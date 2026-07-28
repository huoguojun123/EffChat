//go:build !linux

package handler

import "syscall"

func statfsFragmentSize(stat syscall.Statfs_t) uint64 {
	return uint64(stat.Bsize)
}
