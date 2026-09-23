//go:build darwin

package vfs

import (
	"syscall"

	"github.com/winfsp/cgofuse/fuse"
)

func copyStat(dst *fuse.Stat_t, src *syscall.Stat_t) {
	dst.Dev = uint64(src.Dev)
	dst.Mode = uint32(src.Mode)
	dst.Nlink = uint32(src.Nlink)
	dst.Uid = uint32(src.Uid)
	dst.Gid = uint32(src.Gid)
	dst.Rdev = uint64(src.Rdev)
	dst.Size = int64(src.Size)
	dst.Atim = fuse.Timespec{Sec: src.Atimespec.Sec, Nsec: src.Atimespec.Nsec}
	dst.Mtim = fuse.Timespec{Sec: src.Mtimespec.Sec, Nsec: src.Mtimespec.Nsec}
	dst.Ctim = fuse.Timespec{Sec: src.Ctimespec.Sec, Nsec: src.Ctimespec.Nsec}
	dst.Birthtim = fuse.Timespec{Sec: src.Birthtimespec.Sec, Nsec: src.Birthtimespec.Nsec}
	dst.Flags = uint32(src.Flags)
	dst.Blksize = int64(src.Blksize)
	dst.Blocks = int64(src.Blocks)
}

func fillStatfs(dst *fuse.Statfs_t, src *syscall.Statfs_t) {
	dst.Bsize = uint64(src.Bsize)
	dst.Frsize = 1
	dst.Blocks = uint64(src.Blocks)
	dst.Bfree = uint64(src.Bfree)
	dst.Bavail = uint64(src.Bavail)
	dst.Files = uint64(src.Files)
	dst.Ffree = uint64(src.Ffree)
	dst.Favail = uint64(src.Bavail)
	dst.Namemax = 255
}
