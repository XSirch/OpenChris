package codex

import (
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

var lockFileEx = syscall.NewLazyDLL("kernel32.dll").NewProc("LockFileEx")
var unlockFileEx = syscall.NewLazyDLL("kernel32.dll").NewProc("UnlockFileEx")

func lockAccount(dir string) (func(), error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, failure("auth_storage_failed")
	}
	file, err := os.OpenFile(filepath.Join(dir, ".account.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, failure("auth_storage_failed")
	}
	overlapped := &syscall.Overlapped{}
	ok, _, callErr := lockFileEx.Call(file.Fd(), 3, 0, 1, 0, uintptr(unsafe.Pointer(overlapped)))
	if ok == 0 {
		file.Close()
		if callErr == syscall.Errno(33) {
			return nil, failure("busy")
		}
		return nil, failure("auth_storage_failed")
	}
	return func() { unlockFileEx.Call(file.Fd(), 0, 1, 0, uintptr(unsafe.Pointer(overlapped))); file.Close() }, nil
}

// Production runs on Linux; Windows supports local verification.
func syncDir(string) error { return nil }
