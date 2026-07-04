package secret

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

const (
	protectedKeyFile = ".twitch-stream-key.dpapi"
	legacyKeyFile    = ".twitch-stream-key"
)

func Exists(root string) bool {
	return fileHasContent(filepath.Join(root, protectedKeyFile)) || fileHasContent(filepath.Join(root, legacyKeyFile))
}

func Read(root string) (string, error) {
	protectedPath := filepath.Join(root, protectedKeyFile)
	if fileHasContent(protectedPath) {
		data, err := os.ReadFile(protectedPath)
		if err != nil {
			return "", err
		}
		key, err := unprotect(data)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(key), nil
	}

	legacyPath := filepath.Join(root, legacyKeyFile)
	data, err := os.ReadFile(legacyPath)
	if err != nil {
		return "", err
	}
	key := strings.TrimSpace(string(data))
	if key == "" {
		return "", errors.New("stream key vazia")
	}
	_ = Save(root, key)
	_ = os.Remove(legacyPath)
	return key, nil
}

func Save(root string, key string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return errors.New("stream key vazia")
	}
	if err := os.MkdirAll(root, 0755); err != nil {
		return err
	}
	data, err := protect(key)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(root, protectedKeyFile), data, 0600); err != nil {
		return err
	}
	_ = os.Remove(filepath.Join(root, legacyKeyFile))
	return nil
}

func Remove(root string) error {
	var firstErr error
	for _, name := range []string{protectedKeyFile, legacyKeyFile} {
		err := os.Remove(filepath.Join(root, name))
		if err != nil && !os.IsNotExist(err) && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func fileHasContent(path string) bool {
	data, err := os.ReadFile(path)
	return err == nil && strings.TrimSpace(string(data)) != ""
}
