package deviceidentity

import (
	"bytes"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	vibebridgev1 "github.com/zzemy/VibeBridge/gen/go/vibebridge/v1"
	"github.com/zzemy/VibeBridge/internal/securestore"
	"google.golang.org/protobuf/proto"
)

var testTime = time.Date(2026, 7, 15, 3, 4, 5, 600_000_000, time.UTC)

func TestLoadOrCreateKeepsStableProtectedIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "identity.json")
	first, err := LoadOrCreate(Options{Path: path, DisplayName: "Home PC", Platform: "windows", Now: func() time.Time { return testTime }})
	if err != nil {
		t.Fatalf("create identity: %v", err)
	}
	firstDescriptor, err := first.Descriptor()
	if err != nil {
		t.Fatalf("read first descriptor: %v", err)
	}
	if err := VerifySignedDescriptor(firstDescriptor); err != nil {
		t.Fatalf("verify first descriptor: %v", err)
	}
	if firstDescriptor.DeviceDescriptor.DisplayName != "Home PC" || firstDescriptor.DeviceDescriptor.Platform != "windows" {
		t.Fatalf("first descriptor metadata = %q/%q", firstDescriptor.DeviceDescriptor.DisplayName, firstDescriptor.DeviceDescriptor.Platform)
	}
	message := []byte("identity continuity")
	firstSignature := first.Sign(message)

	second, err := LoadOrCreate(Options{Path: path, DisplayName: "Ignored replacement", Platform: "ignored", Now: func() time.Time { return testTime.Add(time.Hour) }})
	if err != nil {
		t.Fatalf("reload identity: %v", err)
	}
	defer second.Close()
	secondDescriptor, err := second.Descriptor()
	if err != nil {
		t.Fatalf("read second descriptor: %v", err)
	}
	if !proto.Equal(firstDescriptor, secondDescriptor) {
		t.Fatalf("reloaded descriptor changed\nfirst: %v\nsecond: %v", firstDescriptor, secondDescriptor)
	}
	if !ed25519.Verify(secondDescriptor.DeviceDescriptor.SigningPublicKey, message, firstSignature) {
		t.Fatal("reloaded identity does not verify a signature from the original identity")
	}
	if runtime.GOOS == "windows" {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read protected identity: %v", err)
		}
		if bytes.Contains(raw, []byte("Home PC")) || bytes.Contains(raw, firstDescriptor.DeviceDescriptor.DeviceId) {
			t.Fatal("Windows identity store exposes protected identity material as plaintext")
		}
	}
	first.Close()
}

func TestLoadOrCreateSerializesFirstCreationAcrossProcesses(t *testing.T) {
	const processCount = 6
	directory := t.TempDir()
	identityPath := filepath.Join(directory, "identity.json")
	barrierPath := filepath.Join(directory, "start")
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve test executable: %v", err)
	}

	type child struct {
		command *exec.Cmd
		result  string
		output  bytes.Buffer
	}
	children := make([]child, processCount)
	for index := range children {
		resultPath := filepath.Join(directory, fmt.Sprintf("result-%d", index))
		command := exec.Command(executable, "-test.run=^TestLoadOrCreateCrossProcessHelper$")
		command.Env = append(os.Environ(),
			"VIBEBRIDGE_IDENTITY_HELPER=1",
			"VIBEBRIDGE_IDENTITY_PATH="+identityPath,
			"VIBEBRIDGE_IDENTITY_BARRIER="+barrierPath,
			"VIBEBRIDGE_IDENTITY_RESULT="+resultPath,
		)
		children[index] = child{command: command, result: resultPath}
		command.Stdout = &children[index].output
		command.Stderr = &children[index].output
		if err := command.Start(); err != nil {
			t.Fatalf("start helper %d: %v", index, err)
		}
	}
	defer func() {
		for index := range children {
			if children[index].command.ProcessState == nil {
				_ = children[index].command.Process.Kill()
			}
		}
	}()

	deadline := time.Now().Add(15 * time.Second)
	for index := range children {
		readyPath := children[index].result + ".ready"
		for {
			if _, err := os.Stat(readyPath); err == nil {
				break
			} else if !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("check helper %d readiness: %v", index, err)
			}
			if time.Now().After(deadline) {
				t.Fatalf("helper %d did not become ready", index)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	if err := os.WriteFile(barrierPath, []byte("start"), 0o600); err != nil {
		t.Fatalf("release helpers: %v", err)
	}

	identities := make(map[string]struct{})
	for index := range children {
		if err := children[index].command.Wait(); err != nil {
			t.Fatalf("helper %d failed: %v\n%s", index, err, children[index].output.String())
		}
		encoded, err := os.ReadFile(children[index].result)
		if err != nil {
			t.Fatalf("read helper %d result: %v", index, err)
		}
		identities[string(encoded)] = struct{}{}
	}
	if len(identities) != 1 {
		t.Fatalf("concurrent first creation returned %d identities: %v", len(identities), identities)
	}
}

func TestLoadOrCreateCrossProcessHelper(t *testing.T) {
	if os.Getenv("VIBEBRIDGE_IDENTITY_HELPER") != "1" {
		return
	}
	resultPath := os.Getenv("VIBEBRIDGE_IDENTITY_RESULT")
	if err := os.WriteFile(resultPath+".ready", []byte("ready"), 0o600); err != nil {
		t.Fatalf("write helper readiness: %v", err)
	}
	barrierPath := os.Getenv("VIBEBRIDGE_IDENTITY_BARRIER")
	deadline := time.Now().Add(15 * time.Second)
	for {
		if _, err := os.Stat(barrierPath); err == nil {
			break
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("check helper barrier: %v", err)
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for helper barrier")
		}
		time.Sleep(10 * time.Millisecond)
	}
	store, err := LoadOrCreate(Options{Path: os.Getenv("VIBEBRIDGE_IDENTITY_PATH")})
	if err != nil {
		t.Fatalf("load or create identity in helper: %v", err)
	}
	defer store.Close()
	descriptor, err := store.Descriptor()
	if err != nil {
		t.Fatalf("read helper descriptor: %v", err)
	}
	if err := os.WriteFile(resultPath, []byte(hex.EncodeToString(descriptor.DeviceDescriptor.DeviceId)), 0o600); err != nil {
		t.Fatalf("write helper result: %v", err)
	}
}

func TestLoadOrCreateFailsClosedForCorruptOrInvalidExistingState(t *testing.T) {
	t.Run("corrupt envelope", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "identity.json")
		if err := os.WriteFile(path, []byte("corrupt"), 0o600); err != nil {
			t.Fatalf("write corrupt state: %v", err)
		}
		before, _ := os.ReadFile(path)
		if _, err := LoadOrCreate(Options{Path: path}); err == nil {
			t.Fatal("corrupt existing identity was silently replaced")
		}
		after, _ := os.ReadFile(path)
		if !bytes.Equal(after, before) {
			t.Fatal("corrupt existing identity was modified")
		}
	})

	t.Run("invalid protected state", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "identity.json")
		invalid, err := json.Marshal(persistedState{Version: 999})
		if err != nil {
			t.Fatalf("encode invalid state: %v", err)
		}
		if err := securestore.Write(path, secureStorePurpose, invalid); err != nil {
			t.Fatalf("protect invalid state: %v", err)
		}
		before, _ := os.ReadFile(path)
		if _, err := LoadOrCreate(Options{Path: path}); err == nil {
			t.Fatal("invalid protected identity was silently replaced")
		}
		after, _ := os.ReadFile(path)
		if !bytes.Equal(after, before) {
			t.Fatal("invalid protected identity was modified")
		}
	})

	t.Run("inconsistent signing private key", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "identity.json")
		store, err := LoadOrCreate(Options{Path: path})
		if err != nil {
			t.Fatalf("create valid identity: %v", err)
		}
		store.Close()
		encoded, err := securestore.Read(path, secureStorePurpose)
		if err != nil {
			t.Fatalf("read protected identity: %v", err)
		}
		var state persistedState
		if err := json.Unmarshal(encoded, &state); err != nil {
			t.Fatalf("decode protected identity: %v", err)
		}
		zeroBytes(encoded)
		state.SigningPrivateKey[ed25519.SeedSize] ^= 0xff
		encoded, err = json.Marshal(state)
		zeroState(&state)
		if err != nil {
			t.Fatalf("encode inconsistent identity: %v", err)
		}
		if err := securestore.Write(path, secureStorePurpose, encoded); err != nil {
			t.Fatalf("protect inconsistent identity: %v", err)
		}
		zeroBytes(encoded)
		if _, err := LoadOrCreate(Options{Path: path}); err == nil {
			t.Fatal("inconsistent signing private key was accepted")
		}
	})
}

func TestSignedDescriptorRejectsMutationAndInvalidBoundaries(t *testing.T) {
	store, err := LoadOrCreate(Options{Path: filepath.Join(t.TempDir(), "identity.json"), Now: func() time.Time { return testTime }})
	if err != nil {
		t.Fatalf("create identity: %v", err)
	}
	defer store.Close()
	signed, err := store.Descriptor()
	if err != nil {
		t.Fatalf("read descriptor: %v", err)
	}
	mutated := proto.Clone(signed).(*vibebridgev1.SignedDeviceDescriptor)
	mutated.DeviceDescriptor.DisplayName = "Attacker PC"
	if err := VerifySignedDescriptor(mutated); err == nil {
		t.Fatal("mutated signed descriptor verified")
	}
	invalid := proto.Clone(signed.DeviceDescriptor).(*vibebridgev1.DeviceDescriptor)
	invalid.DeviceId = invalid.DeviceId[:15]
	if err := ValidateDescriptor(invalid); err == nil {
		t.Fatal("short device ID accepted")
	}
	fingerprint, err := Fingerprint(signed)
	if err != nil || len(fingerprint) != 12 {
		t.Fatalf("fingerprint = %q/%v, want 12 hexadecimal characters", fingerprint, err)
	}
}

func TestAuthorizeRevokeAndReloadDeviceGraph(t *testing.T) {
	path := filepath.Join(t.TempDir(), "identity.json")
	store, err := LoadOrCreate(Options{Path: path, Now: func() time.Time { return testTime }})
	if err != nil {
		t.Fatalf("create identity: %v", err)
	}
	client := newTestClientDescriptor(t, "My Phone", 0x20)
	authorized, err := store.Authorize(client)
	if err != nil {
		t.Fatalf("authorize client: %v", err)
	}
	if authorized.State != vibebridgev1.DeviceAuthorizationState_DEVICE_AUTHORIZATION_STATE_AUTHORIZED || authorized.AuthorizationVersion != 1 {
		t.Fatalf("authorization = %v, want active version 1", authorized)
	}
	idempotent, err := store.Authorize(client)
	if err != nil || idempotent.AuthorizationVersion != authorized.AuthorizationVersion {
		t.Fatalf("idempotent authorization = %v/%v", idempotent, err)
	}
	store.Close()

	reloaded, err := LoadOrCreate(Options{Path: path, Now: func() time.Time { return testTime.Add(time.Minute) }})
	if err != nil {
		t.Fatalf("reload identity graph: %v", err)
	}
	defer reloaded.Close()
	active, err := reloaded.AuthorizedDevices(false)
	if err != nil || len(active) != 1 || !proto.Equal(active[0].Device, client) {
		t.Fatalf("active devices = %v/%v, want client", active, err)
	}
	revoked, err := reloaded.Revoke(client.DeviceDescriptor.DeviceId)
	if err != nil {
		t.Fatalf("revoke client: %v", err)
	}
	if revoked.State != vibebridgev1.DeviceAuthorizationState_DEVICE_AUTHORIZATION_STATE_REVOKED || revoked.AuthorizationVersion != 2 || revoked.RevocationEpoch != 1 {
		t.Fatalf("revocation = %v, want revoked version 2 epoch 1", revoked)
	}
	again, err := reloaded.Revoke(client.DeviceDescriptor.DeviceId)
	if err != nil || again.AuthorizationVersion != revoked.AuthorizationVersion || reloaded.RevocationEpoch() != 1 {
		t.Fatalf("idempotent revocation = %v/%v epoch %d", again, err, reloaded.RevocationEpoch())
	}
	active, _ = reloaded.AuthorizedDevices(false)
	all, _ := reloaded.AuthorizedDevices(true)
	if len(active) != 0 || len(all) != 1 {
		t.Fatalf("device views active/all = %d/%d, want 0/1", len(active), len(all))
	}
	reauthorized, err := reloaded.Authorize(client)
	if err != nil {
		t.Fatalf("reauthorize paired client: %v", err)
	}
	if reauthorized.AuthorizationVersion != 3 || reauthorized.State != vibebridgev1.DeviceAuthorizationState_DEVICE_AUTHORIZATION_STATE_AUTHORIZED || reloaded.RevocationEpoch() != 1 {
		t.Fatalf("reauthorization = %v epoch %d, want active version 3 epoch 1", reauthorized, reloaded.RevocationEpoch())
	}
}

func TestConcurrentDuplicateAuthorizationIsIdempotent(t *testing.T) {
	store, err := LoadOrCreate(Options{Path: filepath.Join(t.TempDir(), "identity.json"), Now: func() time.Time { return testTime }})
	if err != nil {
		t.Fatalf("create identity: %v", err)
	}
	defer store.Close()
	client := newTestClientDescriptor(t, "Phone", 0x40)
	const workers = 16
	versions := make(chan uint64, workers)
	errorsFound := make(chan error, workers)
	var wait sync.WaitGroup
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			result, err := store.Authorize(client)
			if err != nil {
				errorsFound <- err
				return
			}
			versions <- result.AuthorizationVersion
		}()
	}
	wait.Wait()
	close(versions)
	close(errorsFound)
	for err := range errorsFound {
		t.Fatalf("concurrent authorization: %v", err)
	}
	for version := range versions {
		if version != 1 {
			t.Fatalf("concurrent authorization version = %d, want 1", version)
		}
	}
	devices, err := store.AuthorizedDevices(false)
	if err != nil || len(devices) != 1 {
		t.Fatalf("authorized devices = %d/%v, want one", len(devices), err)
	}
}

func newTestClientDescriptor(t *testing.T, name string, idByte byte) *vibebridgev1.SignedDeviceDescriptor {
	t.Helper()
	_, signingKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate test signing key: %v", err)
	}
	agreementKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate test agreement key: %v", err)
	}
	signed, err := newSignedDescriptor(DescriptorOptions{
		DeviceID:              bytes.Repeat([]byte{idByte}, DeviceIDBytes),
		DisplayName:           name,
		Platform:              "web",
		DeviceClass:           vibebridgev1.DeviceClass_DEVICE_CLASS_CLIENT,
		SigningPublicKey:      signingKey.Public().(ed25519.PublicKey),
		KeyAgreementPublicKey: agreementKey.PublicKey().Bytes(),
		CreatedAt:             testTime,
		KeyVersion:            1,
		ProtocolMajor:         1,
		ProtocolMinor:         0,
	}, signingKey)
	if err != nil {
		t.Fatalf("sign test client descriptor: %v", err)
	}
	return signed
}
