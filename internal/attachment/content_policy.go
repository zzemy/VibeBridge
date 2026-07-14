package attachment

import (
	"bytes"
	"encoding/binary"
	"mime"
	"net/http"
	"strings"
)

type contentPolicy struct {
	extension   string
	contentType string
	text        bool
	binaryKind  string
}

var supportedContentPolicies = map[string]struct {
	contentTypes []string
	canonical    string
	text         bool
	binaryKind   string
}{
	"txt":      {contentTypes: []string{"text/plain"}, canonical: "text/plain", text: true},
	"log":      {contentTypes: []string{"text/plain"}, canonical: "text/plain", text: true},
	"md":       {contentTypes: []string{"text/markdown", "text/plain"}, canonical: "text/markdown", text: true},
	"markdown": {contentTypes: []string{"text/markdown", "text/plain"}, canonical: "text/markdown", text: true},
	"json":     {contentTypes: []string{"application/json"}, canonical: "application/json", text: true},
	"yaml":     {contentTypes: []string{"application/yaml", "application/x-yaml", "text/yaml", "text/x-yaml"}, canonical: "application/yaml", text: true},
	"yml":      {contentTypes: []string{"application/yaml", "application/x-yaml", "text/yaml", "text/x-yaml"}, canonical: "application/yaml", text: true},
	"toml":     {contentTypes: []string{"application/toml", "text/toml"}, canonical: "application/toml", text: true},
	"csv":      {contentTypes: []string{"text/csv"}, canonical: "text/csv", text: true},
	"png":      {contentTypes: []string{"image/png"}, canonical: "image/png", binaryKind: "png"},
	"jpg":      {contentTypes: []string{"image/jpeg"}, canonical: "image/jpeg", binaryKind: "jpeg"},
	"jpeg":     {contentTypes: []string{"image/jpeg"}, canonical: "image/jpeg", binaryKind: "jpeg"},
	"webp":     {contentTypes: []string{"image/webp"}, canonical: "image/webp", binaryKind: "webp"},
	"gif":      {contentTypes: []string{"image/gif"}, canonical: "image/gif", binaryKind: "gif"},
	"pdf":      {contentTypes: []string{"application/pdf"}, canonical: "application/pdf", binaryKind: "pdf"},
}

func parseContentPolicy(declaredExtension, declaredContentType string) (contentPolicy, error) {
	extension := strings.TrimSpace(declaredExtension)
	extension = strings.TrimPrefix(extension, ".")
	if extension == "" || extension != strings.ToLower(extension) || strings.ContainsAny(extension, ". /\\\x00") {
		return contentPolicy{}, ErrUnsupportedContent
	}
	definition, exists := supportedContentPolicies[extension]
	if !exists {
		return contentPolicy{}, ErrUnsupportedContent
	}

	mediaType, parameters, err := mime.ParseMediaType(strings.TrimSpace(declaredContentType))
	if err != nil {
		return contentPolicy{}, ErrUnsupportedContent
	}
	mediaType = strings.ToLower(mediaType)
	if !containsString(definition.contentTypes, mediaType) {
		return contentPolicy{}, ErrUnsupportedContent
	}
	for key, value := range parameters {
		if !definition.text || !strings.EqualFold(key, "charset") || !strings.EqualFold(value, "utf-8") {
			return contentPolicy{}, ErrUnsupportedContent
		}
	}

	return contentPolicy{
		extension:   "." + extension,
		contentType: definition.canonical,
		text:        definition.text,
		binaryKind:  definition.binaryKind,
	}, nil
}

func (p contentPolicy) matchesBytes(sniff []byte, totalSize uint64, validText bool) bool {
	if p.text {
		if !validText {
			return false
		}
		candidate := sniff
		if bytes.HasPrefix(candidate, []byte{0xef, 0xbb, 0xbf}) {
			candidate = candidate[3:]
		}
		detected, _, err := mime.ParseMediaType(http.DetectContentType(candidate))
		return err == nil && detected == "text/plain" && !looksLikeActiveMarkup(candidate)
	}

	detected, _, err := mime.ParseMediaType(http.DetectContentType(sniff))
	if err != nil || detected != p.contentType {
		return false
	}
	switch p.binaryKind {
	case "png":
		return validPNGHeader(sniff)
	case "jpeg":
		return validJPEGHeader(sniff)
	case "gif":
		return validGIFHeader(sniff)
	case "webp":
		return validWebPHeader(sniff, totalSize)
	case "pdf":
		return validPDFHeader(sniff)
	default:
		return false
	}
}

func looksLikeActiveMarkup(data []byte) bool {
	trimmed := bytes.TrimSpace(data)
	lower := bytes.ToLower(trimmed)
	for _, prefix := range [][]byte{
		[]byte("<!doctype html"),
		[]byte("<html"),
		[]byte("<head"),
		[]byte("<body"),
		[]byte("<script"),
		[]byte("<svg"),
		[]byte("<?xml"),
	} {
		if bytes.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

func validPNGHeader(data []byte) bool {
	return len(data) >= 24 &&
		bytes.Equal(data[:8], []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}) &&
		binary.BigEndian.Uint32(data[8:12]) == 13 && bytes.Equal(data[12:16], []byte("IHDR"))
}

func validJPEGHeader(data []byte) bool {
	return len(data) >= 4 && data[0] == 0xff && data[1] == 0xd8 && data[2] == 0xff &&
		data[3] != 0x00 && data[3] != 0xff && data[3] != 0xd8 && data[3] != 0xd9
}

func validGIFHeader(data []byte) bool {
	if len(data) < 10 || (!bytes.Equal(data[:6], []byte("GIF87a")) && !bytes.Equal(data[:6], []byte("GIF89a"))) {
		return false
	}
	return binary.LittleEndian.Uint16(data[6:8]) != 0 && binary.LittleEndian.Uint16(data[8:10]) != 0
}

func validWebPHeader(data []byte, totalSize uint64) bool {
	if len(data) < 16 || !bytes.Equal(data[:4], []byte("RIFF")) || !bytes.Equal(data[8:12], []byte("WEBP")) {
		return false
	}
	if uint64(binary.LittleEndian.Uint32(data[4:8]))+8 != totalSize {
		return false
	}
	variant := string(data[12:16])
	return variant == "VP8 " || variant == "VP8L" || variant == "VP8X"
}

func validPDFHeader(data []byte) bool {
	return len(data) >= 8 && bytes.Equal(data[:5], []byte("%PDF-")) &&
		(data[5] == '1' || data[5] == '2') && data[6] == '.' && data[7] >= '0' && data[7] <= '9'
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
