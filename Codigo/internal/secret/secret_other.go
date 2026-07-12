//go:build !windows

package secret

func protect(value string) ([]byte, error) {
	return []byte(value), nil
}

func unprotect(data []byte) (string, error) {
	return string(data), nil
}
