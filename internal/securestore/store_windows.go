//go:build windows

package securestore

import (
	"errors"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

func protectionName() string { return "dpapi-current-user" }

func protect(plaintext []byte, purpose string) ([]byte, error) {
	return cryptProtectData(plaintext, purposeDigest(purpose))
}

func unprotect(ciphertext []byte, purpose string) ([]byte, error) {
	return cryptUnprotectData(ciphertext, purposeDigest(purpose))
}

func cryptProtectData(plaintext []byte, entropy []byte) ([]byte, error) {
	if len(plaintext) == 0 || len(entropy) == 0 {
		return nil, errors.New("DPAPI input must not be empty")
	}
	input := windows.DataBlob{Size: uint32(len(plaintext)), Data: &plaintext[0]}
	optionalEntropy := windows.DataBlob{Size: uint32(len(entropy)), Data: &entropy[0]}
	var output windows.DataBlob
	if err := windows.CryptProtectData(&input, nil, &optionalEntropy, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &output); err != nil {
		return nil, err
	}
	defer freeDataBlob(&output)
	protected := append([]byte(nil), unsafe.Slice(output.Data, output.Size)...)
	runtime.KeepAlive(plaintext)
	runtime.KeepAlive(entropy)
	return protected, nil
}

func cryptUnprotectData(ciphertext []byte, entropy []byte) ([]byte, error) {
	if len(ciphertext) == 0 || len(entropy) == 0 {
		return nil, errors.New("DPAPI input must not be empty")
	}
	input := windows.DataBlob{Size: uint32(len(ciphertext)), Data: &ciphertext[0]}
	optionalEntropy := windows.DataBlob{Size: uint32(len(entropy)), Data: &entropy[0]}
	var output windows.DataBlob
	var description *uint16
	if err := windows.CryptUnprotectData(&input, &description, &optionalEntropy, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &output); err != nil {
		return nil, err
	}
	if description != nil {
		_, _ = windows.LocalFree(windows.Handle(unsafe.Pointer(description)))
	}
	defer freeDataBlob(&output)
	plaintext := append([]byte(nil), unsafe.Slice(output.Data, output.Size)...)
	runtime.KeepAlive(ciphertext)
	runtime.KeepAlive(entropy)
	return plaintext, nil
}

func freeDataBlob(blob *windows.DataBlob) {
	if blob.Data == nil {
		return
	}
	zero(unsafe.Slice(blob.Data, blob.Size))
	_, _ = windows.LocalFree(windows.Handle(unsafe.Pointer(blob.Data)))
	blob.Data = nil
	blob.Size = 0
}

func replaceFile(source string, destination string) error {
	return windows.MoveFileEx(
		windows.StringToUTF16Ptr(source),
		windows.StringToUTF16Ptr(destination),
		windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH,
	)
}

func syncDirectory(_ string) error { return nil }
