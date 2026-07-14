package securestore

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

const (
	currentEnvelopeVersion = 1
	maxPlaintextBytes      = 1024 * 1024
	maxEnvelopeBytes       = 2 * 1024 * 1024
)

var processLock sync.Mutex

type envelope struct {
	Version    int    `json:"version"`
	Protection string `json:"protection"`
	Purpose    []byte `json:"purpose"`
	Ciphertext []byte `json:"ciphertext"`
}

// Write atomically stores plaintext protected for the current OS user and purpose.
func Write(path string, purpose string, plaintext []byte) error {
	if err := validateArguments(path, purpose); err != nil {
		return err
	}
	if len(plaintext) == 0 {
		return errors.New("secure-store plaintext must not be empty")
	}
	if len(plaintext) > maxPlaintextBytes {
		return fmt.Errorf("secure-store plaintext exceeds the %d byte limit", maxPlaintextBytes)
	}
	processLock.Lock()
	defer processLock.Unlock()

	protected, err := protect(plaintext, purpose)
	if err != nil {
		return fmt.Errorf("protect secure-store data: %w", err)
	}
	defer zero(protected)
	encoded, err := json.Marshal(envelope{currentEnvelopeVersion, protectionName(), purposeDigest(purpose), protected})
	if err != nil {
		return fmt.Errorf("encode secure-store envelope: %w", err)
	}
	defer zero(encoded)
	if len(encoded) > maxEnvelopeBytes {
		return fmt.Errorf("secure-store envelope exceeds the %d byte limit", maxEnvelopeBytes)
	}
	return writeAtomic(path, encoded)
}

// Read loads and unprotects data for the current OS user and exact purpose.
func Read(path string, purpose string) ([]byte, error) {
	if err := validateArguments(path, purpose); err != nil {
		return nil, err
	}
	processLock.Lock()
	defer processLock.Unlock()

	encoded, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	defer zero(encoded)
	if len(encoded) == 0 {
		return nil, errors.New("secure-store envelope is empty")
	}
	if len(encoded) > maxEnvelopeBytes {
		return nil, fmt.Errorf("secure-store envelope exceeds the %d byte limit", maxEnvelopeBytes)
	}
	var value envelope
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("decode secure-store envelope: %w", err)
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("decode secure-store envelope: multiple JSON values are not allowed")
		}
		return nil, fmt.Errorf("decode secure-store envelope: %w", err)
	}
	if value.Version != currentEnvelopeVersion {
		return nil, fmt.Errorf("unsupported secure-store envelope version %d", value.Version)
	}
	if value.Protection != protectionName() {
		return nil, fmt.Errorf("unsupported secure-store protection %q on this platform", value.Protection)
	}
	if !equalPurpose(value.Purpose, purposeDigest(purpose)) {
		return nil, errors.New("secure-store purpose does not match")
	}
	if len(value.Ciphertext) == 0 {
		return nil, errors.New("secure-store ciphertext must not be empty")
	}
	plaintext, err := unprotect(value.Ciphertext, purpose)
	zero(value.Ciphertext)
	if err != nil {
		return nil, fmt.Errorf("unprotect secure-store data: %w", err)
	}
	if len(plaintext) == 0 || len(plaintext) > maxPlaintextBytes {
		zero(plaintext)
		return nil, errors.New("secure-store plaintext has an invalid size")
	}
	return plaintext, nil
}

func validateArguments(path string, purpose string) error {
	if !filepath.IsAbs(path) {
		return errors.New("secure-store path must be absolute")
	}
	if purpose == "" {
		return errors.New("secure-store purpose must not be empty")
	}
	if len(purpose) > 256 {
		return errors.New("secure-store purpose is too long")
	}
	return nil
}

func writeAtomic(path string, encoded []byte) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create secure-store directory: %w", err)
	}
	file, err := os.CreateTemp(directory, ".secure-store-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary secure-store file: %w", err)
	}
	temporaryPath := file.Name()
	defer os.Remove(temporaryPath)
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return fmt.Errorf("restrict secure-store permissions: %w", err)
	}
	if _, err := file.Write(encoded); err != nil {
		_ = file.Close()
		return fmt.Errorf("write secure-store envelope: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("flush secure-store envelope: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close secure-store envelope: %w", err)
	}
	if err := replaceFile(temporaryPath, path); err != nil {
		return fmt.Errorf("publish secure-store envelope: %w", err)
	}
	return syncDirectory(directory)
}

func zero(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
