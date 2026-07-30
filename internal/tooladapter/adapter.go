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

const (
	Generic = "generic"
	Codex   = "codex"
	Claude  = "claude"
)

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

// Adapter prepares attachment-aware terminal input for a specific AI CLI.
type Adapter struct {
	kind string
}

func New(name string) (Adapter, error) {
	if !IsSupported(name) {
		return Adapter{}, ErrUnknownAdapter
	}
	return Adapter{kind: name}, nil
}

func IsSupported(name string) bool {
	return name == Generic || name == Codex || name == Claude
}

// Prepare implements the adapter-specific prompt-path formatting. All adapters
// share the same input validation; only the output format differs.
func (a Adapter) Prepare(request AttachmentPromptRequest) (PreparedAction, error) {
	if !validPrompt(request.Prompt) || len(request.RelativePaths) == 0 {
		return PreparedAction{}, ErrInvalidPromptAction
	}
	for _, path := range request.RelativePaths {
		if !validRelativeReference(path) {
			return PreparedAction{}, ErrInvalidPromptAction
		}
		// Codex and Claude use @-prefixed inline references. Reject paths
		// containing @ to prevent reference-injection through a crafted path.
		if a.kind != Generic && strings.ContainsRune(path, '@') {
			return PreparedAction{}, ErrInvalidPromptAction
		}
	}

	switch a.kind {
	case Codex:
		return a.prepareCodex(request), nil
	case Claude:
		return a.prepareClaude(request), nil
	default:
		return a.prepareGeneric(request), nil
	}
}

// prepareGeneric renders paths in a backtick-delimited list below the prompt.
// Generated references containing the backtick delimiter are rejected upstream.
func (a Adapter) prepareGeneric(request AttachmentPromptRequest) PreparedAction {
	var preview strings.Builder
	preview.Grow(len(request.Prompt) + len(request.RelativePaths)*64)
	preview.WriteString(request.Prompt)
	preview.WriteString("\n\nUse the following local files:")
	for _, path := range request.RelativePaths {
		preview.WriteString("\n- `")
		preview.WriteString(path)
		preview.WriteByte('`')
	}
	return finalizeAction(preview.String(), request.Submit)
}

// prepareCodex appends @-prefixed paths inline after the prompt, matching the
// OpenAI Codex CLI file-reference syntax.
func (a Adapter) prepareCodex(request AttachmentPromptRequest) PreparedAction {
	var preview strings.Builder
	preview.Grow(len(request.Prompt) + len(request.RelativePaths)*48)
	preview.WriteString(request.Prompt)
	for _, path := range request.RelativePaths {
		preview.WriteString(" @")
		preview.WriteString(path)
	}
	return finalizeAction(preview.String(), request.Submit)
}

// prepareClaude appends @-prefixed paths on separate lines after the prompt,
// matching the Anthropic Claude Code CLI file-reference syntax.
func (a Adapter) prepareClaude(request AttachmentPromptRequest) PreparedAction {
	var preview strings.Builder
	preview.Grow(len(request.Prompt) + len(request.RelativePaths)*48)
	preview.WriteString(request.Prompt)
	preview.WriteByte('\n')
	for _, path := range request.RelativePaths {
		preview.WriteString("\n@")
		preview.WriteString(path)
	}
	return finalizeAction(preview.String(), request.Submit)
}

func finalizeAction(preview string, submit bool) PreparedAction {
	terminalInput := make([]byte, len(preview), len(preview)+1)
	copy(terminalInput, preview)
	if submit {
		terminalInput = append(terminalInput, '\r')
	}
	return PreparedAction{Preview: preview, TerminalInput: terminalInput}
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
