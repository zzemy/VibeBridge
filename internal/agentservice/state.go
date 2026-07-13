package agentservice

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"
)

const (
	CurrentRuntimeStateVersion = 1
	maxRuntimeStateBytes       = 64 * 1024
)

type RuntimeState struct {
	Version       int       `json:"version"`
	PID           int       `json:"pid"`
	StartedAt     time.Time `json:"started_at"`
	ListenAddress string    `json:"listen_address"`
	SessionToken  string    `json:"session_token"`
}

func WriteRuntimeState(path string, state RuntimeState) error {
	if err := state.validate(); err != nil {
		return err
	}
	if !filepath.IsAbs(path) {
		return errors.New("runtime state path must be absolute")
	}
	return withRuntimeStateLock(path, func() error {
		return writeRuntimeState(path, state)
	})
}

func writeRuntimeState(path string, state RuntimeState) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create runtime state directory: %w", err)
	}

	file, err := os.CreateTemp(filepath.Dir(path), ".runtime-*.json")
	if err != nil {
		return fmt.Errorf("create temporary runtime state: %w", err)
	}
	temporaryPath := file.Name()
	defer os.Remove(temporaryPath)

	encoder := json.NewEncoder(file)
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return fmt.Errorf("restrict runtime state permissions: %w", err)
	}
	if err := encoder.Encode(state); err != nil {
		_ = file.Close()
		return fmt.Errorf("encode runtime state: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("flush runtime state: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close runtime state: %w", err)
	}

	if err := replaceFile(temporaryPath, path); err != nil {
		return fmt.Errorf("publish runtime state: %w", err)
	}
	return nil
}

func LoadRuntimeState(path string) (RuntimeState, error) {
	file, err := os.Open(path)
	if err != nil {
		return RuntimeState{}, err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return RuntimeState{}, fmt.Errorf("stat runtime state: %w", err)
	}
	if info.Size() > maxRuntimeStateBytes {
		return RuntimeState{}, fmt.Errorf("runtime state exceeds the %d byte limit", maxRuntimeStateBytes)
	}

	var state RuntimeState
	decoder := json.NewDecoder(io.LimitReader(file, maxRuntimeStateBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		return RuntimeState{}, fmt.Errorf("decode runtime state: %w", err)
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return RuntimeState{}, errors.New("decode runtime state: multiple JSON values are not allowed")
		}
		return RuntimeState{}, fmt.Errorf("decode runtime state: %w", err)
	}
	if err := state.validate(); err != nil {
		return RuntimeState{}, err
	}
	return state, nil
}

func RemoveRuntimeState(path string) error {
	if !filepath.IsAbs(path) {
		return errors.New("runtime state path must be absolute")
	}
	return withRuntimeStateLock(path, func() error {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove runtime state: %w", err)
		}
		return nil
	})
}

func ClearRuntimeState(path string, pid int) error {
	if !filepath.IsAbs(path) {
		return errors.New("runtime state path must be absolute")
	}
	return withRuntimeStateLock(path, func() error {
		state, err := LoadRuntimeState(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if state.PID != pid {
			return nil
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove runtime state: %w", err)
		}
		return nil
	})
}

func (state RuntimeState) validate() error {
	if state.Version != CurrentRuntimeStateVersion {
		return fmt.Errorf("unsupported runtime state version %d", state.Version)
	}
	if state.PID <= 0 {
		return errors.New("runtime state PID must be positive")
	}
	if state.StartedAt.IsZero() {
		return errors.New("runtime state start time must not be empty")
	}
	if _, _, err := net.SplitHostPort(state.ListenAddress); err != nil {
		return fmt.Errorf("runtime state listen address is invalid: %w", err)
	}
	if state.SessionToken == "" {
		return errors.New("runtime state session token must not be empty")
	}
	return nil
}
