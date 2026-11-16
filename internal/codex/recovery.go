package codex

import (
	"context"
	"os"
	"path/filepath"
)

func writeSynced(path string, raw []byte) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	if _, err = file.Write(raw); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = syncDir(filepath.Dir(path))
	}
	return err
}
func (r *Runner) acquire() (func(), error) {
	select {
	case r.gate <- struct{}{}:
	default:
		return nil, &Error{Kind: "busy", Transient: true}
	}
	unlock, err := lockAccount(r.cfg.StateDir)
	if err != nil {
		<-r.gate
		return nil, err
	}
	// With the OS lock held, a pending marker can only be abandoned work.
	for _, name := range []string{".blocked", ".auth.pending"} {
		raw, readErr := readLimited(filepath.Join(r.cfg.StateDir, name), 128)
		if os.IsNotExist(readErr) {
			continue
		}
		code := "auth_storage_failed"
		if readErr == nil && string(raw) == "policy_violation" {
			code = "policy_violation"
		}
		r.mu.Lock()
		r.blocked = code
		r.mu.Unlock()
		break
	}
	return func() { unlock(); <-r.gate }, nil
}
func (r *Runner) beginOperation() error {
	if err := writeSynced(filepath.Join(r.cfg.StateDir, ".auth.pending"), []byte("pending")); err != nil {
		r.block("auth_storage_failed")
		return failure("auth_storage_failed")
	}
	return nil
}
func (r *Runner) finishOperation(home string) error {
	if err := r.persist(home); err != nil {
		return err
	}
	if r.blockedState() != "" {
		return nil
	}
	if err := os.Remove(filepath.Join(r.cfg.StateDir, ".auth.pending")); err != nil {
		r.block("auth_storage_failed")
		return failure("auth_storage_failed")
	}
	if err := syncDir(r.cfg.StateDir); err != nil {
		r.block("auth_storage_failed")
		return failure("auth_storage_failed")
	}
	return nil
}

// Operator-only recovery after reviewing an incident; never exposed over HTTP.
func (r *Runner) Recover(context.Context) error {
	release, err := r.acquire()
	if err != nil {
		return err
	}
	defer release()
	for _, name := range []string{".blocked", ".auth.pending"} {
		err = os.Remove(filepath.Join(r.cfg.StateDir, name))
		if err != nil && !os.IsNotExist(err) {
			return failure("auth_storage_failed")
		}
	}
	if err = syncDir(r.cfg.StateDir); err != nil {
		return failure("auth_storage_failed")
	}
	r.mu.Lock()
	r.blocked = ""
	r.mu.Unlock()
	return nil
}
