package protocol

import (
	"testing"

	vibebridgev1 "github.com/zzemy/VibeBridge/gen/go/vibebridge/v1"
	"google.golang.org/protobuf/encoding/protowire"
)

func TestHasUnknownFields(t *testing.T) {
	known := &vibebridgev1.PairingInvitation{
		Agent: &vibebridgev1.SignedDeviceDescriptor{
			DeviceDescriptor: &vibebridgev1.DeviceDescriptor{},
		},
	}
	if HasUnknownFields(nil) {
		t.Fatal("nil message must not contain unknown fields")
	}
	if HasUnknownFields(known) {
		t.Fatal("known message unexpectedly contains unknown fields")
	}

	topLevel := protowire.AppendTag(nil, 99, protowire.VarintType)
	topLevel = protowire.AppendVarint(topLevel, 1)
	known.ProtoReflect().SetUnknown(topLevel)
	if !HasUnknownFields(known) {
		t.Fatal("top-level unknown field was not detected")
	}
	known.ProtoReflect().SetUnknown(nil)

	nested := protowire.AppendTag(nil, 100, protowire.BytesType)
	nested = protowire.AppendBytes(nested, []byte("future"))
	known.Agent.DeviceDescriptor.ProtoReflect().SetUnknown(nested)
	if !HasUnknownFields(known) {
		t.Fatal("nested unknown field was not detected")
	}
}
