package attachment

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
)

const (
	generatedFileIDBytes     = 16
	generatedFileIDHexLength = generatedFileIDBytes * 2
)

// stagingDirectory binds attachment operations to the directory identity that
// was validated when it was opened. Callers never pass display names here;
// names are Agent-generated lowercase hex identifiers plus a policy suffix.
type stagingDirectory struct {
	staging  *SessionStaging
	root     *os.Root
	identity os.FileInfo

	mu     sync.Mutex
	closed bool
}

func openStagingDirectory(staging *SessionStaging) (*stagingDirectory, error) {
	if staging == nil {
		return nil, errors.New("session staging is required")
	}

	staging.mu.Lock()
	defer staging.mu.Unlock()
	if staging.cleaned {
		return nil, errors.New("session staging is already cleaned")
	}
	if err := validateCanonicalDirectory(staging.workspaceRoot, staging.path); err != nil {
		return nil, errors.New("session staging boundary is no longer valid")
	}

	root, err := os.OpenRoot(staging.path)
	if err != nil {
		return nil, newPathOperationError("open session staging directory", err)
	}
	identity, err := root.Stat(".")
	if err != nil {
		_ = root.Close()
		return nil, newPathOperationError("inspect opened session staging directory", err)
	}
	directory := &stagingDirectory{
		staging:  staging,
		root:     root,
		identity: identity,
	}
	if err := directory.validateBoundDirectory(); err != nil {
		_ = root.Close()
		return nil, err
	}
	if err := probeHardLinkPublication(root); err != nil {
		_ = root.Close()
		return nil, err
	}
	staging.openDirectories++
	return directory, nil
}

// probeHardLinkPublication rejects filesystems that cannot provide the atomic
// no-replace primitive used to publish verified attachments. Random generated
// names avoid colliding with abandoned probes; all entries are best-effort
// removed before an error is returned and session cleanup handles crash residue.
func probeHardLinkPublication(root *os.Root) error {
	identifier := make([]byte, generatedFileIDBytes)
	if _, err := rand.Read(identifier); err != nil {
		return errors.New("generate staging publication probe failed")
	}
	prefix := hex.EncodeToString(identifier)
	sourceName := prefix + ".partial"
	destinationName := prefix + ".txt"

	source, err := root.OpenFile(sourceName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return newPathOperationError("create staging publication probe", err)
	}
	if err := source.Close(); err != nil {
		_ = root.Remove(sourceName)
		return newPathOperationError("close staging publication probe", err)
	}
	if err := root.Link(sourceName, destinationName); err != nil {
		_ = root.Remove(sourceName)
		return errors.New("staging filesystem does not support safe attachment publication")
	}
	removeDestinationErr := root.Remove(destinationName)
	removeSourceErr := root.Remove(sourceName)
	if removeDestinationErr != nil {
		return newPathOperationError("remove staging publication probe destination", removeDestinationErr)
	}
	if removeSourceErr != nil {
		return newPathOperationError("remove staging publication probe source", removeSourceErr)
	}
	return nil
}

// create reserves a new empty staged file without following or replacing an
// existing entry.
func (d *stagingDirectory) create(name string) error {
	if !validGeneratedStagingName(name) {
		return errors.New("staged filename is invalid")
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.validateOperation(); err != nil {
		return err
	}
	file, err := d.root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return newPathOperationError("create staged file", err)
	}
	if err := file.Close(); err != nil {
		_ = d.root.Remove(name)
		return newPathOperationError("close created staged file", err)
	}
	return nil
}

// writeAt writes one bounded chunk through a freshly opened root-relative file
// handle. Reopening allows the workspace and staging identities to be checked
// before every chunk operation.
func (d *stagingDirectory) writeAt(name string, offset int64, data []byte) error {
	if !validGeneratedStagingName(name) {
		return errors.New("staged filename is invalid")
	}
	if offset < 0 {
		return errors.New("staged file offset must not be negative")
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.validateOperation(); err != nil {
		return err
	}
	entryInfo, err := d.root.Lstat(name)
	if err != nil {
		return newPathOperationError("inspect staged file before write", err)
	}
	if !entryInfo.Mode().IsRegular() {
		return errors.New("staged file must remain a regular file")
	}

	file, err := d.root.OpenFile(name, os.O_WRONLY, 0)
	if err != nil {
		return newPathOperationError("open staged file for write", err)
	}
	openedInfo, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return newPathOperationError("inspect opened staged file", err)
	}
	if !os.SameFile(entryInfo, openedInfo) {
		_ = file.Close()
		return errors.New("staged file changed while opening")
	}
	written, writeErr := file.WriteAt(data, offset)
	closeErr := file.Close()
	if writeErr != nil {
		return newPathOperationError("write staged file", writeErr)
	}
	if written != len(data) {
		return newPathOperationError("write staged file", io.ErrShortWrite)
	}
	if closeErr != nil {
		return newPathOperationError("close written staged file", closeErr)
	}
	return nil
}

// rename publishes a completed file without replacing an existing destination.
// Root.Link atomically creates the final directory entry, and identity checks
// bind it to the regular source file that was opened before publication. The
// partial name is removed only after those checks succeed.
func (d *stagingDirectory) rename(oldName string, newName string) error {
	if !validGeneratedStagingName(oldName) || !validGeneratedStagingName(newName) {
		return errors.New("staged filename is invalid")
	}
	if oldName == newName {
		return errors.New("staged rename requires distinct names")
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.validateOperation(); err != nil {
		return err
	}
	sourceEntryInfo, err := d.root.Lstat(oldName)
	if err != nil {
		return newPathOperationError("inspect staged rename source", err)
	}
	if !sourceEntryInfo.Mode().IsRegular() {
		return errors.New("staged rename source must be a regular file")
	}
	source, err := d.root.Open(oldName)
	if err != nil {
		return newPathOperationError("open staged rename source", err)
	}
	sourceInfo, err := source.Stat()
	if err != nil {
		_ = source.Close()
		return newPathOperationError("inspect opened staged rename source", err)
	}
	if !sourceInfo.Mode().IsRegular() || !os.SameFile(sourceEntryInfo, sourceInfo) {
		_ = source.Close()
		return errors.New("staged rename source changed while opening")
	}

	if err := d.root.Link(oldName, newName); err != nil {
		_ = source.Close()
		return newPathOperationError("publish staged file", err)
	}
	rollbackDestination := func() {
		_ = d.root.Remove(newName)
	}
	destinationInfo, err := d.root.Lstat(newName)
	if err != nil {
		_ = source.Close()
		rollbackDestination()
		return newPathOperationError("inspect published staged file", err)
	}
	if !destinationInfo.Mode().IsRegular() || !os.SameFile(sourceInfo, destinationInfo) {
		_ = source.Close()
		rollbackDestination()
		return errors.New("published staged file identity does not match source")
	}
	if err := source.Close(); err != nil {
		rollbackDestination()
		return newPathOperationError("close staged rename source", err)
	}
	currentSourceInfo, err := d.root.Lstat(oldName)
	if err != nil {
		rollbackDestination()
		return newPathOperationError("reinspect staged rename source", err)
	}
	if !currentSourceInfo.Mode().IsRegular() || !os.SameFile(sourceInfo, currentSourceInfo) {
		rollbackDestination()
		return errors.New("staged rename source changed before removal")
	}
	if err := d.root.Remove(oldName); err != nil {
		rollbackDestination()
		return newPathOperationError("remove published staged source", err)
	}
	return nil
}

// remove deletes one generated staging entry. Missing files are already in the
// desired state; directories are left for session cleanup rather than removed
// through the file API.
func (d *stagingDirectory) remove(name string) error {
	if !validGeneratedStagingName(name) {
		return errors.New("staged filename is invalid")
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.validateOperation(); err != nil {
		return err
	}
	info, err := d.root.Lstat(name)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return newPathOperationError("inspect staged file before removal", err)
	}
	if info.IsDir() {
		return errors.New("staged file entry must not be a directory")
	}
	if err := d.root.Remove(name); err != nil && !os.IsNotExist(err) {
		return newPathOperationError("remove staged file", err)
	}
	return nil
}

func (d *stagingDirectory) close() error {
	if d == nil {
		return nil
	}

	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil
	}
	d.closed = true
	err := d.root.Close()
	d.mu.Unlock()

	d.staging.mu.Lock()
	if d.staging.openDirectories > 0 {
		d.staging.openDirectories--
	}
	d.staging.mu.Unlock()

	if err != nil {
		return newPathOperationError("close session staging directory", err)
	}
	return nil
}

func (d *stagingDirectory) validateOperation() error {
	if d == nil || d.closed {
		return errors.New("session staging directory is closed")
	}
	return d.validateBoundDirectory()
}

func (d *stagingDirectory) validateBoundDirectory() error {
	if err := validateCanonicalDirectory(d.staging.workspaceRoot, d.staging.path); err != nil {
		return errors.New("session staging boundary is no longer valid")
	}
	currentPathInfo, err := os.Stat(d.staging.path)
	if err != nil {
		return newPathOperationError("inspect current session staging directory", err)
	}
	currentHandleInfo, err := d.root.Stat(".")
	if err != nil {
		return newPathOperationError("inspect bound session staging directory", err)
	}
	if !os.SameFile(d.identity, currentHandleInfo) || !os.SameFile(d.identity, currentPathInfo) {
		return errors.New("session staging directory identity changed")
	}
	return nil
}

func validGeneratedStagingName(name string) bool {
	if strings.ContainsAny(name, "/\\\x00") {
		return false
	}
	identifier, suffix, found := strings.Cut(name, ".")
	if !found || len(identifier) != generatedFileIDHexLength || identifier != strings.ToLower(identifier) {
		return false
	}
	if _, err := hex.DecodeString(identifier); err != nil {
		return false
	}
	return allowedStagingSuffix(suffix)
}

func allowedStagingSuffix(suffix string) bool {
	switch suffix {
	case "partial", "png", "jpg", "jpeg", "webp", "gif", "txt", "log", "md", "markdown", "json", "yaml", "yml", "toml", "csv", "pdf":
		return true
	default:
		return false
	}
}
