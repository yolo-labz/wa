//go:build unix

// Package main is the wad daemon composition root.
//
// migrate_syscall_unix.go implements the unix-specific primitives the
// migration transaction needs: EXDEV pre-flight via stat.Dev, and
// euid ownership check via stat.Uid. Same implementation works on Linux
// and darwin because both expose syscall.Stat_t the same way for these
// fields.
package main

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// sameFilesystem returns nil if src and dst parent directories share a
// filesystem, or an error naming the two devices if they don't.
// rename(2) fails with EXDEV across filesystems and we want to detect
// that up-front.
func sameFilesystem(srcParent, dstParent string) error {
	srcFI, err := os.Stat(srcParent)
	if err != nil {
		return fmt.Errorf("stat src %s: %w", srcParent, err)
	}
	dstFI, err := os.Stat(dstParent)
	if err != nil {
		// If dst parent doesn't exist yet (fresh install), that's OK —
		// we'll create it with the same filesystem as the grandparent.
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("stat dst %s: %w", dstParent, err)
	}

	srcStat, ok1 := srcFI.Sys().(*syscall.Stat_t)
	dstStat, ok2 := dstFI.Sys().(*syscall.Stat_t)
	if !ok1 || !ok2 {
		return errors.New("stat: could not obtain Stat_t (non-unix filesystem?)")
	}
	if srcStat.Dev != dstStat.Dev {
		return fmt.Errorf("src dev=%d, dst dev=%d", srcStat.Dev, dstStat.Dev)
	}
	return nil
}

// ownedByEuid returns nil if path's owner uid equals the process's
// effective uid.
func ownedByEuid(path string, fi os.FileInfo) error {
	stat, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return errors.New("stat: could not obtain Stat_t")
	}
	euid := uint32(os.Geteuid()) //nolint:gosec // bounded by OS uid range
	if stat.Uid != euid {
		return fmt.Errorf("%s owned by uid %d, expected %d", path, stat.Uid, euid)
	}
	return nil
}
