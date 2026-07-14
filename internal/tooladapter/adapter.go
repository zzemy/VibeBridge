// Package tooladapter converts verified Agent-owned attachment references into
// terminal input for a locally selected AI CLI integration.
package tooladapter

import (
	"errors"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

const Generic = "generic"

var (
	ErrUnknownAdapter      = errors.New("tool adapter is not supported")
	ErrInvalidPromptAction = errors.New("attachment prompt action is invalid")
)

// AttachmentPromptRequest contains only an end-user prompt and paths already
// resolved and verified by the Agent. Remote clients must never supply paths.
type AttachmentPromptRequest struct {
	Prompt        string
	RelativePaths []string
	Submit        bool
}

// PreparedAction is the exact preview and immutable terminal input produced by
// an adapter. TerminalInput may include the final Enter key; Preview does not.
type PreparedAction struct {
	Preview       string
	TerminalInput []byte
}

// Adapter prepares attachment-aware terminal input.
type Adapter struct{}

func New(name string) (Adapter, error) {
	if name != Generic {
		return Adapter{}, ErrUnknownAdapter
	}
	return Adapter{}, nil
}

func IsSupported(name string) bool {
	return name == Generic
}

// Prepare implements the generic prompt-path fallback. Paths are rendered in
// backticks, so generated references containing that delimiter are rejected.
func (Adapter) Prepare(request AttachmentPromptRequest) (PreparedAction, error) {
	if !validPrompt(request.Prompt) || len(request.RelativePaths) == 0 {
		return PreparedAction{}, ErrInvalidPromptAction
	}
	for _, path := range request.RelativePaths {
		if !validRelativeReference(path) {
			return PreparedAction{}, ErrInvalidPromptAction
		}
	}

	var preview strings.Builder
	preview.Grow(len(request.Prompt) + len(request.RelativePaths)*64)
	preview.WriteString(request.Prompt)
	preview.WriteString("\n\nUse the following local files:")
	for _, path := range request.RelativePaths {
		preview.WriteString("\n- `")
		preview.WriteString(path)
		preview.WriteByte('`')
	}

	previewText := preview.String()
	terminalInput := make([]byte, len(previewText), len(previewText)+1)
	copy(terminalInput, previewText)
	if request.Submit {
		terminalInput = append(terminalInput, '\r')
	}
	return PreparedAction{Preview: previewText, TerminalInput: terminalInput}, nil
}

func validPrompt(prompt string) bool {
	if strings.TrimSpace(prompt) == "" || !utf8.ValidString(prompt) {
		return false
	}
	for _, value := range prompt {
		if value != '\n' && value != '\t' && (unicode.IsControl(value) || unicode.Is(unicode.Cf, value)) {
			return false
		}
	}
	return true
}

func validRelativeReference(path string) bool {
	if path == "" || !utf8.ValidString(path) || filepath.IsAbs(path) || filepath.VolumeName(path) != "" || hasWindowsVolumePrefix(path) || strings.HasPrefix(path, `\`) || strings.HasPrefix(path, "/") || strings.ContainsRune(path, '`') {
		return false
	}
	for _, value := range path {
		if unicode.IsControl(value) || unicode.Is(unicode.Cf, value) {
			return false
		}
	}
	return true
}

func hasWindowsVolumePrefix(path string) bool {
	if len(path) < 2 || path[1] != ':' {
		return false
	}
	first := path[0]
	return first >= 'A' && first <= 'Z' || first >= 'a' && first <= 'z'
}
