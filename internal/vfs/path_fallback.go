package vfs

import (
	"errors"
	"os"
	"syscall"

	"golang.org/x/text/unicode/norm"
)

// lstatSyscall performs syscall.Lstat with a transparent Unicode normalization fallback on ENOENT,
// ensuring files stored in either NFC or NFD on byte-exact filesystems (Linux ext4, exFAT, NFS, SMB)
// are found regardless of the caller's normalization form.
func lstatSyscall(path string, st *syscall.Stat_t) error {
	err := syscall.Lstat(path, st)
	if err != nil && errors.Is(err, syscall.ENOENT) {
		nfd := norm.NFD.String(path)
		if nfd != path {
			if errNFD := syscall.Lstat(nfd, st); errNFD == nil {
				return nil
			}
		}
		nfc := norm.NFC.String(path)
		if nfc != path {
			if errNFC := syscall.Lstat(nfc, st); errNFC == nil {
				return nil
			}
		}
	}
	return err
}

// openRealFile opens an existing real file with transparent NFC/NFD fallback on ErrNotExist.
func openRealFile(path string) (*os.File, error) {
	f, err := os.Open(path)
	if err != nil && errors.Is(err, os.ErrNotExist) {
		nfd := norm.NFD.String(path)
		if nfd != path {
			if fNFD, errNFD := os.Open(nfd); errNFD == nil {
				return fNFD, nil
			}
		}
		nfc := norm.NFC.String(path)
		if nfc != path {
			if fNFC, errNFC := os.Open(nfc); errNFC == nil {
				return fNFC, nil
			}
		}
	}
	return f, err
}

// statPath performs os.Stat with transparent NFC/NFD fallback on ErrNotExist.
func statPath(path string) (os.FileInfo, error) {
	fi, err := os.Stat(path)
	if err != nil && errors.Is(err, os.ErrNotExist) {
		nfd := norm.NFD.String(path)
		if nfd != path {
			if fiNFD, errNFD := os.Stat(nfd); errNFD == nil {
				return fiNFD, nil
			}
		}
		nfc := norm.NFC.String(path)
		if nfc != path {
			if fiNFC, errNFC := os.Stat(nfc); errNFC == nil {
				return fiNFC, nil
			}
		}
	}
	return fi, err
}

// readDir reads directory entries with transparent NFC/NFD fallback on ErrNotExist.
func readDir(dir string) ([]os.DirEntry, error) {
	entries, err := os.ReadDir(dir)
	if err != nil && errors.Is(err, os.ErrNotExist) {
		nfd := norm.NFD.String(dir)
		if nfd != dir {
			if entriesNFD, errNFD := os.ReadDir(nfd); errNFD == nil {
				return entriesNFD, nil
			}
		}
		nfc := norm.NFC.String(dir)
		if nfc != dir {
			if entriesNFC, errNFC := os.ReadDir(nfc); errNFC == nil {
				return entriesNFC, nil
			}
		}
	}
	return entries, err
}
