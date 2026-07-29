package relay

import (
	"strings"
	"testing"
)

// fakeEpochSource is a tiny EpochSource implementation used to
// drive StoreAuthorizer without paying for the real
// *deviceidentity.Store construction cost (key generation,
// secure-storage backend, persisted state).
type fakeEpochSource struct {
	epoch uint64
}

func (f *fakeEpochSource) RevocationEpoch() uint64 { return f.epoch }

// The compile-time check that *deviceidentity.Store satisfies
// EpochSource lives in epoch.go; here we only need the fake.

func TestStoreAuthorizerRejectsNilSource(t *testing.T) {
	t.Parallel()
	authorizer := StoreAuthorizer(nil)
	// Even with a nil source the wrapper never panics; it
	// rejects every call so a misconfigured relay cannot
	// fall through to a no-op.
	if err := authorizer([]byte("device"), 1); err == nil {
		t.Fatalf("expected an error from nil source, got nil")
	}
}

func TestStoreAuthorizerAcceptsMatchingEpoch(t *testing.T) {
	t.Parallel()
	source := &fakeEpochSource{epoch: 5}
	authorizer := StoreAuthorizer(source)
	if err := authorizer([]byte("device"), 5); err != nil {
		t.Fatalf("expected accept, got %v", err)
	}
}

func TestStoreAuthorizerAcceptsHigherEpoch(t *testing.T) {
	t.Parallel()
	// A ticket stamped at a higher epoch than the live one is
	// a forward-dated ticket the issuer produced against a
	// store that has since rolled back. The source cannot
	// disprove a future epoch; the Authorizer passes and the
	// ticket's expiry / signature still backstop the trust.
	source := &fakeEpochSource{epoch: 5}
	authorizer := StoreAuthorizer(source)
	if err := authorizer([]byte("device"), 9); err != nil {
		t.Fatalf("expected accept, got %v", err)
	}
}

func TestStoreAuthorizerRejectsOlderEpoch(t *testing.T) {
	t.Parallel()
	source := &fakeEpochSource{epoch: 10}
	authorizer := StoreAuthorizer(source)
	err := authorizer([]byte("device"), 3)
	if err == nil {
		t.Fatalf("expected reject, got nil")
	}
	if !strings.Contains(err.Error(), "revoked") {
		t.Fatalf("expected error to mention revocation, got %v", err)
	}
}

func TestStoreAuthorizerRejectsZeroIssuerEpoch(t *testing.T) {
	t.Parallel()
	// A ticket without a stamped epoch is the legacy case; an
	// active gate must treat it as not authorized.
	source := &fakeEpochSource{epoch: 0}
	authorizer := StoreAuthorizer(source)
	err := authorizer([]byte("device"), 0)
	if err == nil {
		t.Fatalf("expected reject for zero issuer epoch, got nil")
	}
	if !strings.Contains(err.Error(), "no issuer epoch") {
		t.Fatalf("expected error to mention missing epoch, got %v", err)
	}
}

func TestStoreAuthorizerIncludesDeviceIDInError(t *testing.T) {
	t.Parallel()
	source := &fakeEpochSource{epoch: 10}
	authorizer := StoreAuthorizer(source)
	device := []byte{0x01, 0x02, 0x03, 0x04}
	err := authorizer(device, 1)
	if err == nil {
		t.Fatalf("expected reject, got nil")
	}
	if !strings.Contains(err.Error(), "01020304") {
		t.Fatalf("expected error to include hex device id, got %v", err)
	}
}
