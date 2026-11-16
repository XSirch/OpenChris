//go:build !linux && !windows

package codex

func lockAccount(string) (func(), error) { return nil, failure("unsupported_runtime") }
func syncDir(string) error               { return failure("unsupported_runtime") }
