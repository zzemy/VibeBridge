package main

import (
	"strings"
	"testing"

	"github.com/zzemy/VibeBridge/internal/deviceidentity"
)

func TestRecoverListMissingStore(t *testing.T) {
	err := recoverList(t.TempDir() + "/nonexistent.json")
	if err == nil {
		t.Fatal("expected error for missing store")
	}
}

func TestRecoverRevokeAllMissingStore(t *testing.T) {
	err := recoverRevokeAll(t.TempDir()+"/nonexistent.json", true)
	if err == nil {
		t.Fatal("expected error for missing store")
	}
}

func TestRecoverRevokeAllRequiresConfirmation(t *testing.T) {
	path := t.TempDir() + "/identity.json"
	_, err := deviceidentity.LoadOrCreate(deviceidentity.Options{Path: path, DisplayName: "T", Platform: "t"})
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	err = recoverRevokeAll(path, false)
	if err == nil {
		t.Fatal("expected error without --yes")
	}
	if !strings.Contains(err.Error(), "confirmation required") {
		t.Fatalf("expected confirmation error, got: %v", err)
	}
}

func TestRecoverRevokeAllNoDevices(t *testing.T) {
	path := t.TempDir() + "/identity.json"
	store, err := deviceidentity.LoadOrCreate(deviceidentity.Options{Path: path, DisplayName: "T", Platform: "t"})
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	store.Close()
	err = recoverRevokeAll(path, true)
	if err != nil {
		t.Fatalf("recoverRevokeAll: %v", err)
	}
}

func TestRecoverListEmptyStore(t *testing.T) {
	path := t.TempDir() + "/identity.json"
	store, err := deviceidentity.LoadOrCreate(deviceidentity.Options{Path: path, DisplayName: "T", Platform: "t"})
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	store.Close()
	err = recoverList(path)
	if err != nil {
		t.Fatalf("recoverList: %v", err)
	}
}

func TestRecoverCommandMutuallyExclusive(t *testing.T) {
	err := runRecoverCommand([]string{"--list", "--revoke-all"})
	if err == nil {
		t.Fatal("expected error for mutually exclusive flags")
	}
}

func TestRecoverCommandNoAction(t *testing.T) {
	err := runRecoverCommand([]string{})
	if err == nil {
		t.Fatal("expected error when no action specified")
	}
}
