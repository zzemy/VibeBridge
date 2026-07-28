package relay

import (
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"
)

// timestamppbNew wraps timestamppb.New so the relay package can keep a
// single import for timestamp helpers. The alias avoids name shadowing
// with the proto types and makes the call site read like a normal
// constructor.
func timestamppbNew(value time.Time) *timestamppb.Timestamp {
	return timestamppb.New(value)
}
