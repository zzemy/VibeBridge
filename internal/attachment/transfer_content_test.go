package attachment

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTransferManagerAcceptsSupportedBinaryMagic(t *testing.T) {
	tests := []struct {
		name        string
		extension   string
		contentType string
		content     []byte
	}{
		{name: "PNG", extension: ".png", contentType: "image/png", content: minimalPNGHeader()},
		{name: "JPEG", extension: ".jpg", contentType: "image/jpeg", content: []byte{0xff, 0xd8, 0xff, 0xe0}},
		{name: "GIF87a", extension: ".gif", contentType: "image/gif", content: minimalGIFHeader("GIF87a")},
		{name: "GIF89a", extension: ".gif", contentType: "image/gif", content: minimalGIFHeader("GIF89a")},
		{name: "WebP VP8", extension: ".webp", contentType: "image/webp", content: minimalWebPHeader("VP8 ")},
		{name: "WebP VP8L", extension: ".webp", contentType: "image/webp", content: minimalWebPHeader("VP8L")},
		{name: "WebP VP8X", extension: ".webp", contentType: "image/webp", content: minimalWebPHeader("VP8X")},
		{name: "PDF", extension: ".pdf", contentType: "application/pdf", content: []byte("%PDF-1.7")},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manager, _ := newTestTransferManager(t, managerLimits{
				maxFileBytes:    64,
				maxSessionBytes: 64,
				maxChunkBytes:   64,
				maxActive:       1,
			})
			request := BeginRequest{
				TransferID:          []byte("binary"),
				DisplayName:         "binary" + test.extension,
				DeclaredContentType: test.contentType,
				DeclaredExtension:   test.extension,
				TotalSizeBytes:      uint64(len(test.content)),
				TotalSHA256:         hashBytes(test.content),
			}
			beginTransfer(t, manager, request)
			writeTransferChunk(t, manager, request.TransferID, 0, test.content)
			completed, err := manager.Complete(request.TransferID)
			if err != nil {
				t.Fatalf("complete %s attachment: %v", test.name, err)
			}
			if completed.ContentType != test.contentType || completed.Extension != test.extension {
				t.Fatalf("completed policy = %q %q, want %q %q", completed.ContentType, completed.Extension, test.contentType, test.extension)
			}
		})
	}
}

func TestTransferManagerAcceptsSupportedTextDeclarations(t *testing.T) {
	tests := []struct {
		extension   string
		contentType string
		canonical   string
	}{
		{extension: ".txt", contentType: "text/plain", canonical: "text/plain"},
		{extension: ".log", contentType: "text/plain; charset=UTF-8", canonical: "text/plain"},
		{extension: ".md", contentType: "text/markdown", canonical: "text/markdown"},
		{extension: ".markdown", contentType: "text/plain", canonical: "text/markdown"},
		{extension: ".json", contentType: "application/json", canonical: "application/json"},
		{extension: ".yaml", contentType: "application/yaml", canonical: "application/yaml"},
		{extension: ".yml", contentType: "application/x-yaml", canonical: "application/yaml"},
		{extension: ".toml", contentType: "application/toml", canonical: "application/toml"},
		{extension: ".csv", contentType: "text/csv", canonical: "text/csv"},
	}
	content := []byte("not syntax validated, only safe UTF-8 text")

	for _, test := range tests {
		t.Run(test.extension+"_"+test.contentType, func(t *testing.T) {
			manager, _ := newTestTransferManager(t, managerLimits{
				maxFileBytes:    64,
				maxSessionBytes: 64,
				maxChunkBytes:   64,
				maxActive:       1,
			})
			request := BeginRequest{
				TransferID:          []byte("text"),
				DisplayName:         "text" + test.extension,
				DeclaredContentType: test.contentType,
				DeclaredExtension:   test.extension,
				TotalSizeBytes:      uint64(len(content)),
				TotalSHA256:         hashBytes(content),
			}
			beginTransfer(t, manager, request)
			writeTransferChunk(t, manager, request.TransferID, 0, content)
			completed, err := manager.Complete(request.TransferID)
			if err != nil {
				t.Fatalf("complete text attachment: %v", err)
			}
			if completed.ContentType != test.canonical {
				t.Fatalf("completed content type = %q, want %q", completed.ContentType, test.canonical)
			}
		})
	}
}

func TestTransferManagerValidatesTextAcrossAllChunks(t *testing.T) {
	t.Run("split UTF-8 rune", func(t *testing.T) {
		content := []byte("A界B")
		manager, _ := newTestTransferManager(t, managerLimits{maxFileBytes: 16, maxSessionBytes: 16, maxChunkBytes: 2, maxActive: 1})
		request := validBeginRequest([]byte("utf8"), content)
		beginTransfer(t, manager, request)
		writeTransferChunk(t, manager, request.TransferID, 0, content[:2])
		writeTransferChunk(t, manager, request.TransferID, 2, content[2:4])
		writeTransferChunk(t, manager, request.TransferID, 4, content[4:])
		if _, err := manager.Complete(request.TransferID); err != nil {
			t.Fatalf("complete split UTF-8 attachment: %v", err)
		}
	})

	tests := []struct {
		name    string
		content []byte
	}{
		{name: "NUL after sniff window", content: append(bytes.Repeat([]byte("a"), contentSniffBytes+8), 0)},
		{name: "incomplete UTF-8 tail", content: append([]byte("text"), 0xe2, 0x82)},
		{name: "HTML masquerading as text", content: []byte("<!doctype html><title>unsafe</title>")},
		{name: "SVG masquerading as text", content: []byte("<svg xmlns=\"http://www.w3.org/2000/svg\"></svg>")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manager, staging := newTestTransferManagerWithStaging(t, managerLimits{
				maxFileBytes:    1024,
				maxSessionBytes: 1024,
				maxChunkBytes:   1024,
				maxActive:       1,
			})
			request := validBeginRequest([]byte("invalid-text"), test.content)
			beginTransfer(t, manager, request)
			writeTransferChunk(t, manager, request.TransferID, 0, test.content)
			if _, err := manager.Complete(request.TransferID); !errors.Is(err, ErrContentTypeMismatch) {
				t.Fatalf("Complete() error = %v, want %v", err, ErrContentTypeMismatch)
			}
			assertStagingEntryCount(t, staging, 0)
		})
	}
}

func TestTransferManagerRejectsTruncatedOrAmbiguousBinaryHeaders(t *testing.T) {
	tests := []struct {
		name        string
		extension   string
		contentType string
		content     []byte
	}{
		{name: "PNG signature only", extension: ".png", contentType: "image/png", content: []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}},
		{name: "JPEG prefix only", extension: ".jpg", contentType: "image/jpeg", content: []byte{0xff, 0xd8, 0xff}},
		{name: "GIF signature only", extension: ".gif", contentType: "image/gif", content: []byte("GIF89a")},
		{name: "WebP unsupported variant", extension: ".webp", contentType: "image/webp", content: minimalWebPHeader("VPzz")},
		{name: "RIFF WAVE", extension: ".webp", contentType: "image/webp", content: []byte("RIFF\x04\x00\x00\x00WAVE")},
		{name: "PDF prefix only", extension: ".pdf", contentType: "application/pdf", content: []byte("%PDF-")},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manager, staging := newTestTransferManagerWithStaging(t, managerLimits{maxFileBytes: 64, maxSessionBytes: 64, maxChunkBytes: 64, maxActive: 1})
			request := BeginRequest{
				TransferID:          []byte("bad-binary"),
				DisplayName:         "binary" + test.extension,
				DeclaredContentType: test.contentType,
				DeclaredExtension:   test.extension,
				TotalSizeBytes:      uint64(len(test.content)),
				TotalSHA256:         hashBytes(test.content),
			}
			beginTransfer(t, manager, request)
			writeTransferChunk(t, manager, request.TransferID, 0, test.content)
			if _, err := manager.Complete(request.TransferID); !errors.Is(err, ErrContentTypeMismatch) {
				t.Fatalf("Complete() error = %v, want %v", err, ErrContentTypeMismatch)
			}
			assertStagingEntryCount(t, staging, 0)
		})
	}
}

func TestTransferManagerRejectsUnsupportedDeclarationParameters(t *testing.T) {
	tests := []BeginRequest{
		withContentType(validBeginRequest([]byte("charset"), []byte("text")), "text/plain; charset=utf-16"),
		withContentType(withExtension(validBeginRequest([]byte("binary-param"), minimalPNGHeader()), ".png"), "image/png; charset=utf-8"),
		withContentType(withExtension(validBeginRequest([]byte("json-plain"), []byte("{}")), ".json"), "text/plain"),
		withContentType(withExtension(validBeginRequest([]byte("yaml-plain"), []byte("key: value")), ".yaml"), "text/plain"),
		withContentType(withExtension(validBeginRequest([]byte("toml-plain"), []byte("key = 1")), ".toml"), "text/plain"),
		withContentType(withExtension(validBeginRequest([]byte("csv-plain"), []byte("a,b")), ".csv"), "text/plain"),
		withExtension(validBeginRequest([]byte("uppercase"), []byte("text")), ".TXT"),
		withExtension(validBeginRequest([]byte("double-dot"), []byte("text")), "..txt"),
	}
	for _, request := range tests {
		manager, staging := newTestTransferManagerWithStaging(t, managerLimits{maxFileBytes: 64, maxSessionBytes: 64, maxChunkBytes: 64, maxActive: 1})
		if err := manager.Begin(request); !errors.Is(err, ErrUnsupportedContent) {
			t.Fatalf("Begin() error = %v, want %v", err, ErrUnsupportedContent)
		}
		assertStagingEntryCount(t, staging, 0)
	}
}

func TestSessionStagingAllowsOnlyOneTransferManager(t *testing.T) {
	staging, err := CreateSessionStaging(t.TempDir(), []byte("single-manager"))
	if err != nil {
		t.Fatalf("create staging: %v", err)
	}
	t.Cleanup(func() { _ = staging.Cleanup() })

	first, err := NewManager(staging)
	if err != nil {
		t.Fatalf("create first manager: %v", err)
	}
	if _, err := NewManager(staging); err == nil {
		t.Fatal("second manager bypassed session ownership")
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close first manager: %v", err)
	}
	second, err := NewManager(staging)
	if err != nil {
		t.Fatalf("create manager after owner close: %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatalf("close second manager: %v", err)
	}
}

func TestTransferManagerRetriesInterruptedPublication(t *testing.T) {
	tests := []struct {
		name    string
		recover func(t *testing.T, manager *Manager, transferID []byte)
	}{
		{
			name: "complete",
			recover: func(t *testing.T, manager *Manager, transferID []byte) {
				t.Helper()
				if _, err := manager.Complete(transferID); err != nil {
					t.Fatalf("retry completion: %v", err)
				}
			},
		},
		{
			name: "cancel",
			recover: func(t *testing.T, manager *Manager, transferID []byte) {
				t.Helper()
				if err := manager.Cancel(transferID); err != nil {
					t.Fatalf("cancel interrupted publication: %v", err)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manager, staging := newTestTransferManagerWithStaging(t, managerLimits{maxFileBytes: 16, maxSessionBytes: 16, maxChunkBytes: 16, maxActive: 1})
			content := []byte("safe text")
			request := validBeginRequest([]byte("publish"), content)
			beginTransfer(t, manager, request)
			writeTransferChunk(t, manager, request.TransferID, 0, content)

			originalRemove := manager.directory.publicationRemove
			failedRemovals := 0
			manager.directory.publicationRemove = func(name string) error {
				if failedRemovals < 2 {
					failedRemovals++
					return fs.ErrPermission
				}
				return originalRemove(name)
			}
			if _, err := manager.Complete(request.TransferID); !errors.Is(err, fs.ErrPermission) {
				t.Fatalf("interrupted Complete() error = %v, want permission failure", err)
			}
			assertStagingEntryCount(t, staging, 2)
			manager.directory.publicationRemove = originalRemove

			test.recover(t, manager, request.TransferID)
			wantEntries := 1
			if test.name == "cancel" {
				wantEntries = 0
			}
			assertStagingEntryCount(t, staging, wantEntries)
		})
	}
}

func TestTransferManagerCloseRetriesFailedPartialCleanup(t *testing.T) {
	manager, staging := newTestTransferManagerWithStaging(t, managerLimits{maxFileBytes: 16, maxSessionBytes: 16, maxChunkBytes: 8, maxActive: 1})
	beginTransfer(t, manager, validBeginRequest([]byte("close-retry"), []byte("data")))
	entries, err := os.ReadDir(staging.Path())
	if err != nil || len(entries) != 1 {
		t.Fatalf("read active partial: entries=%d err=%v", len(entries), err)
	}
	partialPath := filepath.Join(staging.Path(), entries[0].Name())
	if err := os.Remove(partialPath); err != nil {
		t.Fatalf("replace partial file: %v", err)
	}
	if err := os.Mkdir(partialPath, 0o700); err != nil {
		t.Fatalf("create replacement directory: %v", err)
	}

	if err := manager.Close(); err == nil {
		t.Fatal("Close() succeeded despite failed partial cleanup")
	}
	if err := manager.Begin(validBeginRequest([]byte("blocked"), []byte("data"))); !errors.Is(err, ErrManagerClosed) {
		t.Fatalf("Begin() while close is pending error = %v, want %v", err, ErrManagerClosed)
	}
	if err := staging.Cleanup(); err == nil {
		t.Fatal("staging cleanup succeeded while manager close remained retryable")
	}
	if err := os.Remove(partialPath); err != nil {
		t.Fatalf("remove replacement directory: %v", err)
	}
	if err := manager.Close(); err != nil {
		t.Fatalf("retry Close(): %v", err)
	}
	if err := staging.Cleanup(); err != nil {
		t.Fatalf("cleanup staging after close retry: %v", err)
	}
}

func TestTransferManagerClosedRejectsEveryOperation(t *testing.T) {
	manager, _ := newTestTransferManager(t, managerLimits{maxFileBytes: 16, maxSessionBytes: 16, maxChunkBytes: 8, maxActive: 1})
	if err := manager.Close(); err != nil {
		t.Fatalf("close manager: %v", err)
	}
	if err := manager.Begin(validBeginRequest([]byte("closed"), []byte("data"))); !errors.Is(err, ErrManagerClosed) {
		t.Fatalf("Begin() error = %v, want %v", err, ErrManagerClosed)
	}
	if err := manager.Chunk(ChunkRequest{TransferID: []byte("closed"), Data: []byte("data"), ChunkSHA256: hashBytes([]byte("data"))}); !errors.Is(err, ErrManagerClosed) {
		t.Fatalf("Chunk() error = %v, want %v", err, ErrManagerClosed)
	}
	if _, err := manager.Complete([]byte("closed")); !errors.Is(err, ErrManagerClosed) {
		t.Fatalf("Complete() error = %v, want %v", err, ErrManagerClosed)
	}
	if err := manager.Cancel([]byte("closed")); !errors.Is(err, ErrManagerClosed) {
		t.Fatalf("Cancel() error = %v, want %v", err, ErrManagerClosed)
	}
}

func TestTransferErrorsDoNotExposeRemoteMetadata(t *testing.T) {
	manager, _ := newTestTransferManager(t, managerLimits{maxFileBytes: 16, maxSessionBytes: 16, maxChunkBytes: 8, maxActive: 1})
	request := validBeginRequest([]byte("secret-transfer-id"), []byte("data"))
	request.DisplayName = "private-name\n.txt"
	err := manager.Begin(request)
	if !errors.Is(err, ErrInvalidMetadata) {
		t.Fatalf("Begin() error = %v, want %v", err, ErrInvalidMetadata)
	}
	for _, secret := range []string{"secret-transfer-id", "private-name"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("error exposed remote metadata %q: %v", secret, err)
		}
	}
}

func minimalPNGHeader() []byte {
	data := make([]byte, 24)
	copy(data, []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'})
	binary.BigEndian.PutUint32(data[8:12], 13)
	copy(data[12:16], "IHDR")
	return data
}

func minimalGIFHeader(version string) []byte {
	data := make([]byte, 10)
	copy(data, version)
	binary.LittleEndian.PutUint16(data[6:8], 1)
	binary.LittleEndian.PutUint16(data[8:10], 1)
	return data
}

func minimalWebPHeader(variant string) []byte {
	data := make([]byte, 16)
	copy(data, "RIFF")
	binary.LittleEndian.PutUint32(data[4:8], uint32(len(data)-8))
	copy(data[8:12], "WEBP")
	copy(data[12:16], variant)
	return data
}
