package protocol

import (
	"errors"
	"fmt"
	"strings"
	"time"

	vibebridgev1 "github.com/zzemy/VibeBridge/gen/go/vibebridge/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	WebSocketSubprotocol                  = "vibebridge.v1"
	CurrentMajor                   uint32 = 1
	CurrentMinor                   uint32 = 0
	MaxEnvelopeBytes               uint32 = 64 * 1024
	CapabilityTerminalBinaryOutput        = "terminal.binary_output"

	connectionIDBytes = 16
	maxCapabilities   = 64
	maxCapabilityLen  = 128
)

type NegotiatedHello struct {
	Major                uint32
	Minor                uint32
	PeerMaxEnvelopeBytes uint32
	ConnectionID         []byte
	capabilities         map[string]struct{}
}

func (n NegotiatedHello) HasCapability(capability string) bool {
	_, ok := n.capabilities[capability]
	return ok
}

func AcceptClientHello(encoded []byte) (NegotiatedHello, error) {
	if len(encoded) == 0 || len(encoded) > int(MaxEnvelopeBytes) {
		return NegotiatedHello{}, fmt.Errorf("Hello envelope size must be between 1 and %d bytes", MaxEnvelopeBytes)
	}

	envelope := new(vibebridgev1.Envelope)
	if err := proto.Unmarshal(encoded, envelope); err != nil {
		return NegotiatedHello{}, fmt.Errorf("decode client Hello: %w", err)
	}
	if envelope.ProtocolMajor != CurrentMajor || envelope.ProtocolMinor != CurrentMinor {
		return NegotiatedHello{}, errors.New("client selected an unsupported protocol version")
	}
	if len(envelope.ConnectionId) != connectionIDBytes {
		return NegotiatedHello{}, fmt.Errorf("connection ID must be %d bytes", connectionIDBytes)
	}
	if len(envelope.SessionId) != 0 || envelope.SessionGeneration != 0 || envelope.Sequence != 1 || envelope.Acknowledge != 0 {
		return NegotiatedHello{}, errors.New("client Hello has invalid session or sequence metadata")
	}

	hello := envelope.GetHello()
	if hello == nil {
		return NegotiatedHello{}, errors.New("first Protocol V1 envelope must contain Hello")
	}
	if hello.PeerRole != vibebridgev1.PeerRole_PEER_ROLE_CLIENT {
		return NegotiatedHello{}, errors.New("Hello peer role must be client")
	}
	if hello.MaxEnvelopeBytes == 0 {
		return NegotiatedHello{}, errors.New("Hello max envelope bytes must be positive")
	}

	rangeValue := hello.SupportedVersions
	if rangeValue == nil || rangeValue.Minimum == nil || rangeValue.Maximum == nil {
		return NegotiatedHello{}, errors.New("Hello supported version range is required")
	}
	minimum, maximum := rangeValue.Minimum, rangeValue.Maximum
	if minimum.Major != maximum.Major || minimum.Major != CurrentMajor || minimum.Minor > maximum.Minor {
		return NegotiatedHello{}, errors.New("Hello supported version range is invalid or incompatible")
	}
	if CurrentMinor < minimum.Minor || CurrentMinor > maximum.Minor {
		return NegotiatedHello{}, errors.New("Hello supported version range does not include the current version")
	}

	capabilities := make(map[string]struct{}, len(hello.Capabilities))
	if len(hello.Capabilities) > maxCapabilities {
		return NegotiatedHello{}, fmt.Errorf("Hello has more than %d capabilities", maxCapabilities)
	}
	for _, capability := range hello.Capabilities {
		if capability == "" || len(capability) > maxCapabilityLen || strings.TrimSpace(capability) != capability {
			return NegotiatedHello{}, errors.New("Hello contains an invalid capability name")
		}
		if _, exists := capabilities[capability]; exists {
			return NegotiatedHello{}, errors.New("Hello contains a duplicate capability")
		}
		capabilities[capability] = struct{}{}
	}

	return NegotiatedHello{
		Major:                CurrentMajor,
		Minor:                CurrentMinor,
		PeerMaxEnvelopeBytes: hello.MaxEnvelopeBytes,
		ConnectionID:         append([]byte(nil), envelope.ConnectionId...),
		capabilities:         capabilities,
	}, nil
}

func NewAgentHello(connectionID []byte, major, minor uint32, sentAt time.Time) (*vibebridgev1.Envelope, error) {
	if len(connectionID) != connectionIDBytes {
		return nil, fmt.Errorf("connection ID must be %d bytes", connectionIDBytes)
	}
	if major != CurrentMajor || minor != CurrentMinor {
		return nil, errors.New("cannot advertise an unsupported negotiated version")
	}

	return &vibebridgev1.Envelope{
		ProtocolMajor: major,
		ProtocolMinor: minor,
		ConnectionId:  append([]byte(nil), connectionID...),
		Sequence:      1,
		SentAt:        timestamppb.New(sentAt.UTC()),
		Payload: &vibebridgev1.Envelope_Hello{Hello: &vibebridgev1.Hello{
			PeerRole: vibebridgev1.PeerRole_PEER_ROLE_AGENT,
			SupportedVersions: &vibebridgev1.ProtocolVersionRange{
				Minimum: &vibebridgev1.ProtocolVersion{Major: CurrentMajor, Minor: CurrentMinor},
				Maximum: &vibebridgev1.ProtocolVersion{Major: CurrentMajor, Minor: CurrentMinor},
			},
			Capabilities:     []string{CapabilityTerminalBinaryOutput, CapabilityTerminalSequencedIO, CapabilitySessionResume},
			MaxEnvelopeBytes: MaxEnvelopeBytes,
		}},
	}, nil
}
