//go:build windows

package secret

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

func protect(value string) ([]byte, error) {
	input := []byte(value)
	inBlob := windows.DataBlob{
		Size: uint32(len(input)),
		Data: &input[0],
	}
	var outBlob windows.DataBlob
	if err := windows.CryptProtectData(&inBlob, nil, nil, 0, nil, 0, &outBlob); err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(uintptr(unsafe.Pointer(outBlob.Data))))

	output := unsafe.Slice(outBlob.Data, outBlob.Size)
	result := make([]byte, len(output))
	copy(result, output)
	return result, nil
}

func unprotect(data []byte) (string, error) {
	inBlob := windows.DataBlob{
		Size: uint32(len(data)),
		Data: &data[0],
	}
	var outBlob windows.DataBlob
	if err := windows.CryptUnprotectData(&inBlob, nil, nil, 0, nil, 0, &outBlob); err != nil {
		return "", err
	}
	defer windows.LocalFree(windows.Handle(uintptr(unsafe.Pointer(outBlob.Data))))

	output := unsafe.Slice(outBlob.Data, outBlob.Size)
	result := make([]byte, len(output))
	copy(result, output)
	return string(result), nil
}
