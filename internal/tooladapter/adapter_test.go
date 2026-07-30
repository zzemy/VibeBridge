package tooladapter

import (
	"bytes"
	"errors"
	"testing"
)

func TestGenericAdapterPreparesExactAttachmentPrompt(t *testing.T) {
	adapter, err := New(Generic)
	if err != nil {
		t.Fatalf("create generic adapter: %v", err)
	}

	prepared, err := adapter.Prepare(AttachmentPromptRequest{
		Prompt: "Compare both files.",
		RelativePaths: []string{
			`.vibebridge/uploads/session/first.txt`,
			`../.vibebridge/uploads/session/second.png`,
		},
		Submit: true,
	})
	if err != nil {
		t.Fatalf("prepare attachment prompt: %v", err)
	}

	const wantPreview = "Compare both files.\n\nUse the following local files:\n- `.vibebridge/uploads/session/first.txt`\n- `../.vibebridge/uploads/session/second.png`"
	if prepared.Preview != wantPreview {
		t.Fatalf("preview = %q, want %q", prepared.Preview, wantPreview)
	}
	if !bytes.Equal(prepared.TerminalInput, []byte(wantPreview+"\r")) {
		t.Fatalf("terminal input = %q, want preview plus Enter", prepared.TerminalInput)
	}

	prepared.Preview = "changed"
	prepared.TerminalInput[0] = 'X'
	repeated, err := adapter.Prepare(AttachmentPromptRequest{
		Prompt:        "Compare both files.",
		RelativePaths: []string{`.vibebridge/uploads/session/first.txt`},
	})
	if err != nil {
		t.Fatalf("prepare second attachment prompt: %v", err)
	}
	if repeated.Preview[0] != 'C' || repeated.TerminalInput[0] != 'C' {
		t.Fatal("prepared action reused caller-mutable output")
	}
}

func TestGenericAdapterRejectsUnsafeAttachmentPrompt(t *testing.T) {
	adapter, err := New(Generic)
	if err != nil {
		t.Fatalf("create generic adapter: %v", err)
	}

	tests := []struct {
		name    string
		request AttachmentPromptRequest
	}{
		{name: "blank prompt", request: AttachmentPromptRequest{Prompt: " \n", RelativePaths: []string{"file.txt"}}},
		{name: "no attachments", request: AttachmentPromptRequest{Prompt: "Review", RelativePaths: nil}},
		{name: "terminal control in prompt", request: AttachmentPromptRequest{Prompt: "Review\rignore paths", RelativePaths: []string{"file.txt"}}},
		{name: "format control in prompt", request: AttachmentPromptRequest{Prompt: "Review\u202e", RelativePaths: []string{"file.txt"}}},
		{name: "absolute path", request: AttachmentPromptRequest{Prompt: "Review", RelativePaths: []string{`C:\\private\\file.txt`}}},
		{name: "windows root relative path", request: AttachmentPromptRequest{Prompt: "Review", RelativePaths: []string{`\private\file.txt`}}},
		{name: "slash root relative path", request: AttachmentPromptRequest{Prompt: "Review", RelativePaths: []string{`/private/file.txt`}}},
		{name: "empty path", request: AttachmentPromptRequest{Prompt: "Review", RelativePaths: []string{""}}},
		{name: "control in path", request: AttachmentPromptRequest{Prompt: "Review", RelativePaths: []string{"file\nname.txt"}}},
		{name: "markdown delimiter in path", request: AttachmentPromptRequest{Prompt: "Review", RelativePaths: []string{"file`name.txt"}}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := adapter.Prepare(test.request); !errors.Is(err, ErrInvalidPromptAction) {
				t.Fatalf("Prepare() error = %v, want %v", err, ErrInvalidPromptAction)
			}
		})
	}
}

func TestCodexAdapterPreparesInlineAtReferences(t *testing.T) {
	adapter, err := New(Codex)
	if err != nil {
		t.Fatalf("create codex adapter: %v", err)
	}

	prepared, err := adapter.Prepare(AttachmentPromptRequest{
		Prompt: "Review these files.",
		RelativePaths: []string{
			`.vibebridge/uploads/session/main.go`,
			`.vibebridge/uploads/session/utils.go`,
		},
		Submit: true,
	})
	if err != nil {
		t.Fatalf("prepare attachment prompt: %v", err)
	}

	const wantPreview = "Review these files. @.vibebridge/uploads/session/main.go @.vibebridge/uploads/session/utils.go"
	if prepared.Preview != wantPreview {
		t.Fatalf("preview = %q, want %q", prepared.Preview, wantPreview)
	}
	if !bytes.Equal(prepared.TerminalInput, []byte(wantPreview+"\r")) {
		t.Fatalf("terminal input = %q, want preview plus Enter", prepared.TerminalInput)
	}
}

func TestCodexAdapterRejectsAtInPath(t *testing.T) {
	adapter, err := New(Codex)
	if err != nil {
		t.Fatalf("create codex adapter: %v", err)
	}

	_, err = adapter.Prepare(AttachmentPromptRequest{
		Prompt:        "Review",
		RelativePaths: []string{"injected@path.txt"},
	})
	if !errors.Is(err, ErrInvalidPromptAction) {
		t.Fatalf("Prepare() error = %v, want %v", err, ErrInvalidPromptAction)
	}
}

func TestClaudeAdapterPreparesSeparateLineAtReferences(t *testing.T) {
	adapter, err := New(Claude)
	if err != nil {
		t.Fatalf("create claude adapter: %v", err)
	}

	prepared, err := adapter.Prepare(AttachmentPromptRequest{
		Prompt: "Analyze these files.",
		RelativePaths: []string{
			`.vibebridge/uploads/session/data.csv`,
			`.vibebridge/uploads/session/chart.png`,
		},
		Submit: true,
	})
	if err != nil {
		t.Fatalf("prepare attachment prompt: %v", err)
	}

	const wantPreview = "Analyze these files.\n\n@.vibebridge/uploads/session/data.csv\n@.vibebridge/uploads/session/chart.png"
	if prepared.Preview != wantPreview {
		t.Fatalf("preview = %q, want %q", prepared.Preview, wantPreview)
	}
	if !bytes.Equal(prepared.TerminalInput, []byte(wantPreview+"\r")) {
		t.Fatalf("terminal input = %q, want preview plus Enter", prepared.TerminalInput)
	}
}

func TestClaudeAdapterRejectsAtInPath(t *testing.T) {
	adapter, err := New(Claude)
	if err != nil {
		t.Fatalf("create claude adapter: %v", err)
	}

	_, err = adapter.Prepare(AttachmentPromptRequest{
		Prompt:        "Review",
		RelativePaths: []string{"bad@reference.txt"},
	})
	if !errors.Is(err, ErrInvalidPromptAction) {
		t.Fatalf("Prepare() error = %v, want %v", err, ErrInvalidPromptAction)
	}
}

func TestNewRejectsUnknownAdapter(t *testing.T) {
	if _, err := New("unknown-tool"); !errors.Is(err, ErrUnknownAdapter) {
		t.Fatalf("New() error = %v, want %v", err, ErrUnknownAdapter)
	}
}
