package logging

import (
	"fmt"
	"os"
	"path/filepath"
)

const (
	DefaultMaxBytes = 2 * 1024 * 1024
	DefaultBackups  = 3
)

func Rotate(path string, maxBytes int64, backups int) error {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	if backups <= 0 {
		backups = DefaultBackups
	}

	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if info.Size() < maxBytes {
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}

	_ = os.Remove(fmt.Sprintf("%s.%d", path, backups))
	for i := backups - 1; i >= 1; i-- {
		oldPath := fmt.Sprintf("%s.%d", path, i)
		newPath := fmt.Sprintf("%s.%d", path, i+1)
		if _, err := os.Stat(oldPath); err == nil {
			_ = os.Rename(oldPath, newPath)
		}
	}
	return os.Rename(path, path+".1")
}
