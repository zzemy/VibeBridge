package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// Definition is a locally configured workspace root. Root is canonical and
// absolute after NewRegistry validates it.
type Definition struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Root  string `json:"root"`
}

// Registry owns the validated local workspace roots available to the Agent.
type Registry struct {
	definitions []Definition
	byID        map[string]Definition
}

// NewRegistry validates definitions and canonicalizes roots. Relative roots
// are resolved from baseDirectory.
func NewRegistry(definitions []Definition, baseDirectory string) (Registry, error) {
	registry := Registry{
		definitions: make([]Definition, 0, len(definitions)),
		byID:        make(map[string]Definition, len(definitions)),
	}
	seenRoots := make(map[string]string, len(definitions))

	for index, definition := range definitions {
		if !idPattern.MatchString(definition.ID) {
			return Registry{}, fmt.Errorf("workspaces[%d]: id %q must match %s", index, definition.ID, idPattern)
		}
		if _, exists := registry.byID[definition.ID]; exists {
			return Registry{}, fmt.Errorf("duplicate workspace id %q", definition.ID)
		}
		definition.Label = strings.TrimSpace(definition.Label)
		if definition.Label == "" {
			return Registry{}, fmt.Errorf("workspaces[%d]: label must not be empty", index)
		}

		root, err := canonicalDirectory(definition.Root, baseDirectory)
		if err != nil {
			return Registry{}, fmt.Errorf("workspaces[%d] %q: invalid root: %w", index, definition.ID, err)
		}
		definition.Root = root
		rootKey := filepath.Clean(root)
		if existingID, exists := seenRoots[rootKey]; exists {
			return Registry{}, fmt.Errorf("workspace %q duplicates the canonical root of workspace %q", definition.ID, existingID)
		}
		seenRoots[rootKey] = definition.ID
		registry.definitions = append(registry.definitions, definition)
		registry.byID[definition.ID] = definition
	}

	return registry, nil
}

// RevalidateDirectory resolves a previously validated launch directory against
// its canonical workspace root immediately before use. It rejects a root whose
// current filesystem target differs from the registry snapshot.
func RevalidateDirectory(root string, path string) (string, error) {
	currentRoot, err := canonicalDirectory(root, "")
	if err != nil {
		return "", fmt.Errorf("revalidate workspace root: %w", err)
	}
	if filepath.Clean(currentRoot) != filepath.Clean(root) {
		return "", errors.New("workspace root changed since configuration")
	}
	if path == "" || filepath.Clean(path) == filepath.Clean(root) {
		return currentRoot, nil
	}
	currentDirectory, err := canonicalDirectory(path, "")
	if err != nil {
		return "", fmt.Errorf("revalidate launch directory: %w", err)
	}
	if !containsCanonicalPath(currentRoot, currentDirectory) {
		return "", errors.New("launch directory must remain within workspace")
	}
	return currentDirectory, nil
}

// Definitions returns a copy of the validated workspace definitions.
func (r Registry) Definitions() []Definition {
	return append([]Definition(nil), r.definitions...)
}

// Lookup returns a validated workspace by its local configuration ID.
func (r Registry) Lookup(id string) (Definition, bool) {
	definition, ok := r.byID[id]
	return definition, ok
}

// ResolveDirectory resolves an existing directory under a registered
// workspace. Relative paths are interpreted from the workspace root.
func (r Registry) ResolveDirectory(id string, path string) (string, error) {
	definition, ok := r.Lookup(id)
	if !ok {
		return "", fmt.Errorf("workspace %q is not configured", id)
	}
	if path == "" {
		return definition.Root, nil
	}

	candidate := path
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(definition.Root, candidate)
	}
	canonical, err := canonicalDirectory(candidate, "")
	if err != nil {
		return "", fmt.Errorf("resolve directory in workspace %q: %w", id, err)
	}
	if !containsCanonicalPath(definition.Root, canonical) {
		return "", fmt.Errorf("directory must remain within workspace %q", id)
	}
	return canonical, nil
}

func canonicalDirectory(path string, baseDirectory string) (string, error) {
	if path == "" {
		return "", errors.New("root must not be empty")
	}
	if strings.ContainsRune(path, '\x00') {
		return "", errors.New("path contains a NUL byte")
	}
	if baseDirectory != "" && !filepath.IsAbs(path) {
		path = filepath.Join(baseDirectory, path)
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", newPathOperationError("make path absolute", err)
	}
	canonical, err := canonicalizePath(absolute)
	if err != nil {
		return "", newPathOperationError("resolve path", err)
	}
	canonical, err = filepath.Abs(canonical)
	if err != nil {
		return "", newPathOperationError("make canonical path absolute", err)
	}
	canonical = filepath.Clean(canonical)
	info, err := os.Stat(canonical)
	if err != nil {
		return "", newPathOperationError("inspect path", err)
	}
	if !info.IsDir() {
		return "", errors.New("path is not a directory")
	}
	return canonical, nil
}

func containsCanonicalPath(root string, candidate string) bool {
	root = filepath.Clean(root)
	candidate = filepath.Clean(candidate)
	if candidate == root {
		return true
	}
	if !strings.HasSuffix(root, string(filepath.Separator)) {
		root += string(filepath.Separator)
	}
	return strings.HasPrefix(candidate, root)
}

type pathOperationError struct {
	operation string
	err       error
}

func newPathOperationError(operation string, err error) error {
	return pathOperationError{operation: operation, err: err}
}

func (e pathOperationError) Error() string {
	return e.operation + " failed"
}

func (e pathOperationError) Unwrap() error {
	return e.err
}
