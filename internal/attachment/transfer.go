package attachment

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"hash"
	"io/fs"
	"math"
	"path/filepath"
	"sync"
	"unicode"
	"unicode/utf8"
)

const (
	defaultMaxFileBytes    = 25 * 1024 * 1024
	defaultMaxSessionBytes = 100 * 1024 * 1024
	defaultMaxChunkBytes   = 256 * 1024
	defaultMaxActive       = 4

	maxTransferIDBytes  = 64
	maxDisplayNameBytes = 255
	storageNameAttempts = 4
	contentSniffBytes   = 512

	maxPromptActionAttachments = 10
	maxCancelledOutcomes       = 256
)

// Stable transfer errors classify failures without exposing remote metadata or local paths.
var (
	ErrInvalidTransfer            = errors.New("attachment transfer is invalid")
	ErrInvalidMetadata            = errors.New("attachment metadata is invalid")
	ErrFileLimitExceeded          = errors.New("attachment file limit exceeded")
	ErrUnsupportedContent         = errors.New("attachment content declaration is unsupported")
	ErrActiveTransferLimit        = errors.New("active attachment transfer limit exceeded")
	ErrSessionQuotaExceeded       = errors.New("attachment session quota exceeded")
	ErrTransferExists             = errors.New("attachment transfer already exists")
	ErrTransferNotFound           = errors.New("attachment transfer was not found")
	ErrChunkChecksumMismatch      = errors.New("attachment chunk checksum mismatch")
	ErrChunkOffsetMismatch        = errors.New("attachment chunk offset mismatch")
	ErrChunkLimitExceeded         = errors.New("attachment chunk limit exceeded")
	ErrTotalSizeMismatch          = errors.New("attachment total size mismatch")
	ErrTotalChecksumMismatch      = errors.New("attachment total checksum mismatch")
	ErrContentTypeMismatch        = errors.New("attachment content does not match its declaration")
	ErrManagerClosed              = errors.New("attachment transfer manager is closed")
	ErrInvalidAttachmentSelection = errors.New("attachment selection is invalid")
	ErrAttachmentSelectionLimit   = errors.New("attachment selection limit exceeded")
	ErrDuplicateAttachment        = errors.New("attachment selection contains a duplicate")
	ErrAttachmentNotCompleted     = errors.New("attachment transfer is not completed")
)

// BeginRequest declares one attachment before any bytes are accepted.
type BeginRequest struct {
	TransferID          []byte
	DisplayName         string
	DeclaredContentType string
	DeclaredExtension   string
	TotalSizeBytes      uint64
	TotalSHA256         []byte
}

// ChunkRequest appends one checksummed, ordered byte range.
type ChunkRequest struct {
	TransferID  []byte
	OffsetBytes uint64
	Data        []byte
	ChunkSHA256 []byte
}

// TransferDisposition describes one session-local transfer outcome without exposing metadata or paths.
type TransferDisposition uint8

const (
	TransferDispositionUnknown TransferDisposition = iota
	TransferDispositionActive
	TransferDispositionCompleted
	TransferDispositionCancelled
)

// TransferOutcome is safe to return to a remote client. NextOffsetBytes is set only for active transfers.
type TransferOutcome struct {
	Disposition     TransferDisposition
	NextOffsetBytes uint64
}

// CompletedAttachment describes a verified file using a workspace-relative path.
type CompletedAttachment struct {
	TransferID   []byte
	DisplayName  string
	RelativePath string
	ContentType  string
	Extension    string
	SizeBytes    uint64
	SHA256       []byte
}

type managerLimits struct {
	maxFileBytes    uint64
	maxSessionBytes uint64
	maxChunkBytes   int
	maxActive       int
}

// Manager owns attachment transfer state and one open staging-directory handle.
// Close must run before the corresponding SessionStaging is cleaned.
type Manager struct {
	mu sync.Mutex

	directory *stagingDirectory
	staging   *SessionStaging
	limits    managerLimits

	active         map[string]*activeTransfer
	completed      map[string]CompletedAttachment
	cancelled      map[string]struct{}
	cancelledOrder []string
	reservedBytes  uint64
	closing        bool
	closed         bool
}

type activeTransfer struct {
	transferID           []byte
	displayName          string
	policy               contentPolicy
	totalSize            uint64
	expectedSHA256       [sha256.Size]byte
	receivedBytes        uint64
	hasher               hash.Hash
	sniff                []byte
	text                 utf8StreamValidator
	storagePrefix        string
	partialName          string
	publicationAttempted bool
}

// NewManager opens the session staging boundary with the default V1 limits.
func NewManager(staging *SessionStaging) (*Manager, error) {
	return newTransferManager(staging, managerLimits{
		maxFileBytes:    defaultMaxFileBytes,
		maxSessionBytes: defaultMaxSessionBytes,
		maxChunkBytes:   defaultMaxChunkBytes,
		maxActive:       defaultMaxActive,
	})
}

func newTransferManager(staging *SessionStaging, limits managerLimits) (*Manager, error) {
	if limits.maxFileBytes == 0 || limits.maxSessionBytes == 0 ||
		limits.maxFileBytes > math.MaxInt64 || limits.maxChunkBytes <= 0 || limits.maxActive <= 0 {
		return nil, errors.New("attachment transfer limits are invalid")
	}
	if err := claimTransferManager(staging); err != nil {
		return nil, fmt.Errorf("claim attachment transfer storage: %w", err)
	}
	directory, err := openStagingDirectory(staging)
	if err != nil {
		releaseTransferManager(staging)
		return nil, fmt.Errorf("open attachment transfer storage: %w", err)
	}
	return &Manager{
		directory:     directory,
		staging:       staging,
		limits:        limits,
		active:        make(map[string]*activeTransfer),
		completed:     make(map[string]CompletedAttachment),
		cancelled:     make(map[string]struct{}),
		reservedBytes: staging.completedBytes(),
	}, nil
}

// Begin validates metadata, reserves the declared size, and exclusively creates a partial file.
func (m *Manager) Begin(request BeginRequest) error {
	if m == nil {
		return ErrManagerClosed
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.closing {
		return ErrManagerClosed
	}
	if !validTransferID(request.TransferID) || len(request.TotalSHA256) != sha256.Size {
		return ErrInvalidTransfer
	}
	if !validDisplayName(request.DisplayName) {
		return ErrInvalidMetadata
	}
	if request.TotalSizeBytes == 0 || request.TotalSizeBytes > m.limits.maxFileBytes {
		return ErrFileLimitExceeded
	}
	policy, err := parseContentPolicy(request.DeclaredExtension, request.DeclaredContentType)
	if err != nil {
		return err
	}
	key := string(request.TransferID)
	if _, exists := m.active[key]; exists {
		return ErrTransferExists
	}
	if _, exists := m.completed[key]; exists {
		return ErrTransferExists
	}
	if len(m.active) >= m.limits.maxActive {
		return ErrActiveTransferLimit
	}
	if m.reservedBytes > m.limits.maxSessionBytes || request.TotalSizeBytes > m.limits.maxSessionBytes-m.reservedBytes {
		return ErrSessionQuotaExceeded
	}

	prefix, partialName, err := m.createPartialLocked()
	if err != nil {
		return err
	}
	sniffCapacity := contentSniffBytes
	if request.TotalSizeBytes < contentSniffBytes {
		sniffCapacity = int(request.TotalSizeBytes)
	}
	transfer := &activeTransfer{
		transferID:    append([]byte(nil), request.TransferID...),
		displayName:   request.DisplayName,
		policy:        policy,
		totalSize:     request.TotalSizeBytes,
		hasher:        sha256.New(),
		sniff:         make([]byte, 0, sniffCapacity),
		storagePrefix: prefix,
		partialName:   partialName,
	}
	copy(transfer.expectedSHA256[:], request.TotalSHA256)
	m.active[key] = transfer
	m.reservedBytes += request.TotalSizeBytes
	return nil
}

// Chunk accepts only the next bounded range and advances state after a successful write.
func (m *Manager) Chunk(request ChunkRequest) error {
	if m == nil {
		return ErrManagerClosed
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.closing {
		return ErrManagerClosed
	}
	if !validTransferID(request.TransferID) || len(request.ChunkSHA256) != sha256.Size || len(request.Data) == 0 {
		return ErrInvalidTransfer
	}
	transfer, exists := m.active[string(request.TransferID)]
	if !exists {
		return ErrTransferNotFound
	}
	if len(request.Data) > m.limits.maxChunkBytes {
		return ErrChunkLimitExceeded
	}
	if request.OffsetBytes != transfer.receivedBytes {
		return ErrChunkOffsetMismatch
	}
	if uint64(len(request.Data)) > transfer.totalSize-transfer.receivedBytes {
		return ErrTotalSizeMismatch
	}
	chunkHash := sha256.Sum256(request.Data)
	if !equalHash(chunkHash[:], request.ChunkSHA256) {
		return ErrChunkChecksumMismatch
	}
	if err := m.directory.writeAt(transfer.partialName, int64(request.OffsetBytes), request.Data); err != nil {
		return m.abortAfterWriteFailureLocked(string(request.TransferID), transfer, err)
	}

	_, _ = transfer.hasher.Write(request.Data)
	if remaining := contentSniffBytes - len(transfer.sniff); remaining > 0 {
		transfer.sniff = append(transfer.sniff, request.Data[:min(remaining, len(request.Data))]...)
	}
	if transfer.policy.text {
		transfer.text.Write(request.Data)
	}
	transfer.receivedBytes += uint64(len(request.Data))
	return nil
}

// Complete verifies total size, checksum, and content policy before no-replace publication.
// Repeating a successful completion returns a defensive copy of the same result.
func (m *Manager) Complete(transferID []byte) (CompletedAttachment, error) {
	if m == nil {
		return CompletedAttachment{}, ErrManagerClosed
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.closing {
		return CompletedAttachment{}, ErrManagerClosed
	}
	if !validTransferID(transferID) {
		return CompletedAttachment{}, ErrInvalidTransfer
	}
	key := string(transferID)
	if completed, exists := m.completed[key]; exists {
		return cloneCompletedAttachment(completed), nil
	}
	transfer, exists := m.active[key]
	if !exists {
		return CompletedAttachment{}, ErrTransferNotFound
	}
	if transfer.receivedBytes != transfer.totalSize {
		return CompletedAttachment{}, m.failTransferLocked(key, transfer, ErrTotalSizeMismatch)
	}
	actualHash := transfer.hasher.Sum(nil)
	if !equalHash(actualHash, transfer.expectedSHA256[:]) {
		return CompletedAttachment{}, m.failTransferLocked(key, transfer, ErrTotalChecksumMismatch)
	}
	if !transfer.policy.matchesBytes(transfer.sniff, transfer.totalSize, transfer.text.Valid()) {
		return CompletedAttachment{}, m.failTransferLocked(key, transfer, ErrContentTypeMismatch)
	}

	finalName := transfer.storagePrefix + transfer.policy.extension
	relativePath, err := filepath.Rel(m.staging.workspaceRoot, filepath.Join(m.staging.path, finalName))
	if err != nil || !filepath.IsLocal(relativePath) {
		return CompletedAttachment{}, errors.New("build published attachment reference failed")
	}
	transfer.publicationAttempted = true
	if err := m.directory.rename(transfer.partialName, finalName); err != nil {
		return CompletedAttachment{}, fmt.Errorf("publish verified attachment: %w", err)
	}
	completed := CompletedAttachment{
		TransferID:   append([]byte(nil), transfer.transferID...),
		DisplayName:  transfer.displayName,
		RelativePath: relativePath,
		ContentType:  transfer.policy.contentType,
		Extension:    transfer.policy.extension,
		SizeBytes:    transfer.totalSize,
		SHA256:       append([]byte(nil), actualHash...),
	}
	m.staging.recordCompletedBytes(transfer.totalSize)
	delete(m.active, key)
	m.completed[key] = completed
	return cloneCompletedAttachment(completed), nil
}

// Outcome reports the durable in-session state for an opaque transfer ID.
// It intentionally returns no attachment metadata or storage path.
func (m *Manager) Outcome(transferID []byte) (TransferOutcome, error) {
	if m == nil {
		return TransferOutcome{}, ErrManagerClosed
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.closing {
		return TransferOutcome{}, ErrManagerClosed
	}
	if !validTransferID(transferID) {
		return TransferOutcome{}, ErrInvalidTransfer
	}
	key := string(transferID)
	if transfer, exists := m.active[key]; exists {
		return TransferOutcome{Disposition: TransferDispositionActive, NextOffsetBytes: transfer.receivedBytes}, nil
	}
	if _, exists := m.completed[key]; exists {
		return TransferOutcome{Disposition: TransferDispositionCompleted}, nil
	}
	if _, exists := m.cancelled[key]; exists {
		return TransferOutcome{Disposition: TransferDispositionCancelled}, nil
	}
	return TransferOutcome{Disposition: TransferDispositionUnknown}, nil
}

// CompletedAttachments resolves an ordered prompt-action selection from this
// manager's completed records. Active and unknown IDs intentionally share one error.
func (m *Manager) CompletedAttachments(transferIDs [][]byte) ([]CompletedAttachment, error) {
	if m == nil {
		return nil, ErrManagerClosed
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.closing {
		return nil, ErrManagerClosed
	}
	if len(transferIDs) == 0 {
		return nil, ErrInvalidAttachmentSelection
	}
	if len(transferIDs) > maxPromptActionAttachments {
		return nil, ErrAttachmentSelectionLimit
	}

	seen := make(map[string]struct{}, len(transferIDs))
	for _, transferID := range transferIDs {
		if !validTransferID(transferID) {
			return nil, ErrInvalidTransfer
		}
		key := string(transferID)
		if _, exists := seen[key]; exists {
			return nil, ErrDuplicateAttachment
		}
		seen[key] = struct{}{}
	}

	completed := make([]CompletedAttachment, 0, len(transferIDs))
	for _, transferID := range transferIDs {
		attachment, exists := m.completed[string(transferID)]
		if !exists {
			return nil, ErrAttachmentNotCompleted
		}
		completed = append(completed, cloneCompletedAttachment(attachment))
	}
	return completed, nil
}

// Cancel idempotently removes an active partial; completed files remain for session cleanup.
func (m *Manager) Cancel(transferID []byte) error {
	if m == nil {
		return ErrManagerClosed
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.closing {
		return ErrManagerClosed
	}
	if !validTransferID(transferID) {
		return ErrInvalidTransfer
	}
	key := string(transferID)
	if _, completed := m.completed[key]; completed {
		return nil
	}
	if transfer, active := m.active[key]; active {
		if err := m.cleanupActiveTransferLocked(transfer); err != nil {
			return fmt.Errorf("cancel attachment transfer: %w", err)
		}
		m.releaseActiveLocked(key, transfer)
	}
	m.recordCancelledLocked(key)
	return nil
}

func (m *Manager) recordCancelledLocked(key string) {
	if _, exists := m.cancelled[key]; exists {
		return
	}
	m.cancelled[key] = struct{}{}
	m.cancelledOrder = append(m.cancelledOrder, key)
	if len(m.cancelledOrder) <= maxCancelledOutcomes {
		return
	}
	oldest := m.cancelledOrder[0]
	m.cancelledOrder = m.cancelledOrder[1:]
	delete(m.cancelled, oldest)
}

// Close removes active partials and releases the staging-directory handle.
func (m *Manager) Close() error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil
	}
	m.closing = true

	var cleanupErrors []error
	for key, transfer := range m.active {
		if err := m.cleanupActiveTransferLocked(transfer); err != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("remove active attachment transfer: %w", err))
			continue
		}
		m.releaseActiveLocked(key, transfer)
	}
	if len(cleanupErrors) > 0 {
		return errors.Join(cleanupErrors...)
	}
	err := m.directory.close()
	releaseTransferManager(m.staging)
	m.closed = true
	m.closing = false
	return err
}

func (m *Manager) createPartialLocked() (string, string, error) {
	for range storageNameAttempts {
		prefix, err := generatedStoragePrefix()
		if err != nil {
			return "", "", err
		}
		name := prefix + ".partial"
		if err := m.directory.create(name); err != nil {
			if errors.Is(err, fs.ErrExist) {
				continue
			}
			return "", "", fmt.Errorf("create attachment partial: %w", err)
		}
		return prefix, name, nil
	}
	return "", "", errors.New("create unique attachment partial failed")
}

func (m *Manager) failTransferLocked(key string, transfer *activeTransfer, cause error) error {
	if err := m.directory.remove(transfer.partialName); err != nil {
		return errors.Join(cause, fmt.Errorf("remove rejected attachment partial: %w", err))
	}
	m.releaseActiveLocked(key, transfer)
	return cause
}

func (m *Manager) abortAfterWriteFailureLocked(key string, transfer *activeTransfer, cause error) error {
	if err := m.directory.remove(transfer.partialName); err != nil {
		return errors.Join(fmt.Errorf("write attachment chunk: %w", cause), fmt.Errorf("remove failed attachment partial: %w", err))
	}
	m.releaseActiveLocked(key, transfer)
	return fmt.Errorf("write attachment chunk: %w", cause)
}

func (m *Manager) cleanupActiveTransferLocked(transfer *activeTransfer) error {
	if transfer.publicationAttempted {
		finalName := transfer.storagePrefix + transfer.policy.extension
		if err := m.directory.removePublishedLink(transfer.partialName, finalName); err != nil {
			return err
		}
	}
	return m.directory.remove(transfer.partialName)
}

func (m *Manager) releaseActiveLocked(key string, transfer *activeTransfer) {
	delete(m.active, key)
	if transfer.totalSize <= m.reservedBytes {
		m.reservedBytes -= transfer.totalSize
	} else {
		m.reservedBytes = 0
	}
}

func validTransferID(transferID []byte) bool {
	return len(transferID) > 0 && len(transferID) <= maxTransferIDBytes
}

func validDisplayName(name string) bool {
	if name == "" || len(name) > maxDisplayNameBytes || !utf8.ValidString(name) {
		return false
	}
	for _, r := range name {
		if unicode.Is(unicode.C, r) {
			return false
		}
	}
	return true
}

func equalHash(left, right []byte) bool {
	return len(left) == sha256.Size && len(right) == sha256.Size && subtle.ConstantTimeCompare(left, right) == 1
}

func cloneCompletedAttachment(completed CompletedAttachment) CompletedAttachment {
	completed.TransferID = append([]byte(nil), completed.TransferID...)
	completed.SHA256 = append([]byte(nil), completed.SHA256...)
	return completed
}

type utf8StreamValidator struct {
	invalid bool
	tail    []byte
}

func (v *utf8StreamValidator) Write(data []byte) {
	if v.invalid {
		return
	}
	combined := make([]byte, 0, len(v.tail)+len(data))
	combined = append(combined, v.tail...)
	combined = append(combined, data...)
	v.tail = v.tail[:0]

	for len(combined) > 0 {
		if combined[0] == 0 {
			v.invalid = true
			return
		}
		if !utf8.FullRune(combined) {
			v.tail = append(v.tail, combined...)
			return
		}
		runeValue, size := utf8.DecodeRune(combined)
		if runeValue == utf8.RuneError && size == 1 {
			v.invalid = true
			return
		}
		combined = combined[size:]
	}
}

func (v *utf8StreamValidator) Valid() bool {
	return !v.invalid && len(v.tail) == 0
}
