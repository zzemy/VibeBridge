package relay

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	vibebridgev1 "github.com/zzemy/VibeBridge/gen/go/vibebridge/v1"
	"github.com/zzemy/VibeBridge/internal/deviceidentity"
	"google.golang.org/protobuf/proto"
)

// Admin issues signed RelayTickets over a small HTTP control plane. The
// endpoint is intended for use by the Agent or a control plane on the
// same host; production deployments should bind it to localhost and
// (recommended) supply an admin token. The HTTP server is intentionally
// kept separate from the WebSocket listener so the two surfaces do not
// share a port.
type Admin struct {
	issuer *Issuer
	clock  func() time.Time
}

// NewAdmin returns an Admin backed by the supplied Issuer. The Issuer's
// private key is used to sign every issued ticket; the Admin never
// exposes the private key to callers.
func NewAdmin(issuer *Issuer) (*Admin, error) {
	if issuer == nil {
		return nil, errors.New("relay admin requires an Issuer")
	}
	return &Admin{issuer: issuer, clock: time.Now}, nil
}

// issueRequest is the JSON body of POST /v1/tickets. Every field is
// required; an absent or zero value is rejected so the caller can
// never mint a malformed ticket by accident.
type issueRequest struct {
	RouteID         string `json:"route_id"`
	DeviceID        string `json:"device_id"`
	Endpoint        string `json:"endpoint"`
	MaxConnections  uint32 `json:"max_connections"`
	LifetimeSeconds int    `json:"lifetime_seconds"`
}

// issueResponse is the JSON body of a successful issue. The ticket is
// returned as a hex-encoded protobuf so the caller can paste it into a
// header, embed it in a QR code, or hand it to a client without further
// encoding.
type issueResponse struct {
	Ticket    string    `json:"ticket"`
	ExpiresAt time.Time `json:"expires_at"`
}

// errorResponse is the body of every 4xx / 5xx response. Reason is a
// short, machine-friendly code; Message is a human-friendly hint.
type errorResponse struct {
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

// Handler returns the http.Handler that serves the admin surface. The
// returned handler has no state of its own; safe to share across
// multiple http.Server instances. The supplied token is the bearer
// token required to authorize every request. An empty token disables
// bearer checks; callers are then expected to rely on the listener's
// bind address as the trust boundary.
func (admin *Admin) Handler(token string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/tickets", admin.handleIssue(token))
	return mux
}

func (admin *Admin) handleIssue(token string) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "only POST is accepted")
			return
		}
		if !authorizeBearer(request, token) {
			writeError(writer, http.StatusUnauthorized, "unauthorized", "missing or invalid bearer token")
			return
		}
		defer request.Body.Close()
		decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 4096))
		decoder.DisallowUnknownFields()
		var body issueRequest
		if err := decoder.Decode(&body); err != nil {
			writeError(writer, http.StatusBadRequest, "invalid_body", fmt.Sprintf("decode request body: %v", err))
			return
		}
		endpoint, err := parseEndpoint(body.Endpoint)
		if err != nil {
			writeError(writer, http.StatusBadRequest, "invalid_endpoint", err.Error())
			return
		}
		routeID, err := parseHexBytes("route_id", body.RouteID, routeIDBytes)
		if err != nil {
			writeError(writer, http.StatusBadRequest, "invalid_route_id", err.Error())
			return
		}
		deviceID, err := parseDeviceID(body.DeviceID)
		if err != nil {
			writeError(writer, http.StatusBadRequest, "invalid_device_id", err.Error())
			return
		}
		if body.MaxConnections == 0 {
			writeError(writer, http.StatusBadRequest, "invalid_max_connections", "max_connections must be greater than zero")
			return
		}
		if body.LifetimeSeconds <= 0 {
			writeError(writer, http.StatusBadRequest, "invalid_lifetime", "lifetime_seconds must be greater than zero")
			return
		}
		lifetime := time.Duration(body.LifetimeSeconds) * time.Second
		ticket, err := admin.issuer.Issue(IssueInput{
			RouteID:        routeID,
			DeviceID:       deviceID,
			Endpoint:       endpoint,
			MaxConnections: body.MaxConnections,
			Lifetime:       lifetime,
		})
		if err != nil {
			writeError(writer, http.StatusBadRequest, "issue_failed", err.Error())
			return
		}
		wire, err := proto.Marshal(ticket)
		if err != nil {
			writeError(writer, http.StatusInternalServerError, "encode_failed", err.Error())
			return
		}
		expires := admin.clock().Add(lifetime).UTC()
		if ticket.ExpiresAt != nil {
			expires = ticket.ExpiresAt.AsTime().UTC()
		}
		writeJSON(writer, http.StatusOK, issueResponse{
			Ticket:    hex.EncodeToString(wire),
			ExpiresAt: expires,
		})
	}
}

// parseEndpoint maps the wire string to the protobuf enum. Anything
// outside the two known values is rejected so a typo can never silently
// mint a RELAY_ENDPOINT_UNSPECIFIED ticket.
func parseEndpoint(value string) (vibebridgev1.RelayEndpoint, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "agent":
		return vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_AGENT, nil
	case "client":
		return vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_CLIENT, nil
	case "":
		return 0, errors.New("endpoint is required")
	default:
		return 0, fmt.Errorf("endpoint must be one of agent|client, got %q", value)
	}
}

// parseHexBytes decodes a hex string and validates it has the expected
// byte length. field is included in the error so the caller can surface
// it without further decoration.
func parseHexBytes(field string, value string, expected int) ([]byte, error) {
	if value == "" {
		return nil, fmt.Errorf("%s is required", field)
	}
	raw, err := hex.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("%s is not valid hex: %w", field, err)
	}
	if len(raw) != expected {
		return nil, fmt.Errorf("%s must be %d bytes, got %d", field, expected, len(raw))
	}
	return raw, nil
}

// parseDeviceID routes device id parsing through the shared size
// constant the Verifier already enforces. Keeping a single source of
// truth means a future change to the device id size only has to land
// in one place.
func parseDeviceID(value string) ([]byte, error) {
	return parseHexBytes("device_id", value, deviceidentity.DeviceIDBytes)
}

// authorizeBearer checks the Authorization header against the
// configured token. An empty token means the admin surface is open;
// callers should bind it to a trusted interface in that case.
func authorizeBearer(request *http.Request, token string) bool {
	if token == "" {
		return true
	}
	header := request.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return false
	}
	supplied := strings.TrimSpace(header[len(prefix):])
	if supplied == "" {
		return false
	}
	// Constant-time compare so a timing oracle cannot leak the token
	// byte by byte.
	return subtleEqual(supplied, token)
}

func subtleEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := 0; i < len(a); i++ {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}

func writeJSON(writer http.ResponseWriter, status int, body interface{}) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(body)
}

func writeError(writer http.ResponseWriter, status int, reason, message string) {
	writeJSON(writer, status, errorResponse{Reason: reason, Message: message})
}
