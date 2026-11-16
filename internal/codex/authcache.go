package codex

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
)

type authCache struct {
	path string
	key  []byte
}

func (c authCache) aead() (cipher.AEAD, error) {
	if len(c.key) != 32 {
		return nil, failure("invalid_key")
	}
	block, err := aes.NewCipher(c.key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
func readLimited(path string, limit int) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, int64(limit+1)))
	if err != nil || len(raw) > limit {
		return nil, failure("output_limit")
	}
	return raw, nil
}
func (c authCache) load() ([]byte, error) {
	raw, err := readLimited(c.path, 65536+64)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, failure("auth_storage_failed")
	}
	aead, err := c.aead()
	if err != nil {
		return nil, err
	}
	n := aead.NonceSize()
	if len(raw) < 3+n || string(raw[:3]) != "CV1" {
		return nil, failure("auth_storage_failed")
	}
	plain, err := aead.Open(nil, raw[3:3+n], raw[3+n:], []byte("conselho.codex.auth.v1"))
	if err != nil || !json.Valid(plain) {
		return nil, failure("auth_storage_failed")
	}
	return plain, nil
}
func (c authCache) save(plain []byte) error {
	if len(plain) > 65536 || !json.Valid(plain) {
		return failure("auth_storage_failed")
	}
	aead, err := c.aead()
	if err != nil {
		return err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return err
	}
	raw := append([]byte("CV1"), nonce...)
	raw = aead.Seal(raw, nonce, plain, []byte("conselho.codex.auth.v1"))
	if err = os.MkdirAll(filepath.Dir(c.path), 0700); err != nil {
		return failure("auth_storage_failed")
	}
	file, err := os.CreateTemp(filepath.Dir(c.path), ".account-*")
	if err != nil {
		return failure("auth_storage_failed")
	}
	name := file.Name()
	defer os.Remove(name)
	if err = file.Chmod(0600); err == nil {
		_, err = file.Write(raw)
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(name, c.path)
	}
	if err==nil{err=syncDir(filepath.Dir(c.path))}
	if err != nil {
		return failure("auth_storage_failed")
	}
	return nil
}
