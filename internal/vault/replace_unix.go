//go:build !windows

package vault

import "os"

func replaceFile(from, to string) error { return os.Rename(from, to) }
