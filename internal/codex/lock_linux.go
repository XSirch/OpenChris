package codex

import (
	"os"
	"path/filepath"
	"syscall"
)

func lockAccount(dir string) (func(), error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, failure("auth_storage_failed")
	}
	file, err := os.OpenFile(filepath.Join(dir, ".account.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, failure("auth_storage_failed")
	}
	if err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		if err == syscall.EWOULDBLOCK {
			return nil, failure("busy")
		}
		return nil, failure("auth_storage_failed")
	}
	return func() { syscall.Flock(int(file.Fd()), syscall.LOCK_UN); file.Close() }, nil
}
func syncDir(dir string) error {
	file, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}
