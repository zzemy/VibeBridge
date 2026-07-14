package main

import (
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"net/url"
	"strings"

	vibebridgev1 "github.com/zzemy/VibeBridge/gen/go/vibebridge/v1"
	"github.com/zzemy/VibeBridge/internal/deviceidentity"
	"github.com/zzemy/VibeBridge/internal/pairing"
	"rsc.io/qr"
)

const (
	localPairingPath = "/agent/pair"
	localRevokePath  = "/agent/devices/revoke"
)

var pairingPageTemplate = template.Must(template.New("pairing").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="referrer" content="no-referrer">
<title>Pair a phone · VibeBridge</title>
<style>
:root { color-scheme: dark; font-family: ui-sans-serif, system-ui, sans-serif; background: #09090b; color: #fafafa; }
body { min-height: 100vh; margin: 0; }
main { width: min(92vw, 38rem); margin: 0 auto; padding: 2rem 0 4rem; }
.hero { text-align: center; }
img { width: min(72vw, 20rem); height: auto; padding: 1rem; border-radius: 1rem; background: white; }
p { color: #a1a1aa; line-height: 1.5; }
code { overflow-wrap: anywhere; color: #d8b4fe; }
.verification { font: 700 1.6rem ui-monospace, monospace; letter-spacing: .12em; color: #fafafa; }
section { margin-top: 2rem; border-top: 1px solid #27272a; padding-top: 1.5rem; }
.device { display: flex; align-items: center; justify-content: space-between; gap: 1rem; padding: .9rem 0; border-bottom: 1px solid #27272a; }
.device p { margin: .2rem 0; }
button { border: 1px solid #7f1d1d; border-radius: .5rem; color: #fecaca; background: #450a0a; padding: .55rem .8rem; cursor: pointer; }
.badge { color: #86efac; }
.badge.revoked { color: #a1a1aa; }
details { margin-top: 1rem; }
</style>
</head>
<body><main>
<div class="hero">
<h1>Pair a phone</h1>
{{if .QRCode}}<img src="data:image/png;base64,{{.QRCode}}" alt="VibeBridge phone pairing QR code">
<p>Connect the phone to the same trusted network, then scan this single-use code before <strong>{{.ExpiresAt}}</strong>.</p>
<p>Agent fingerprint <code>{{.AgentFingerprint}}</code></p>
<p>Verification code</p><div class="verification">{{.VerificationCode}}</div>
<details><summary>Copy pairing link</summary><code>{{.Target}}</code></details>
{{else}}<p>No private-network address is available. Connect this computer to your trusted network and try again.</p>{{end}}
<p>The QR secret is valid for five minutes and is never a permanent credential. Final trust is stored only after the encrypted pairing handshake and explicit approval on this computer.</p>
</div>
<section><h2>Paired devices</h2>
{{if .Devices}}
{{range .Devices}}<div class="device"><div><strong>{{.Name}}</strong><p>{{.Platform}} · <code>{{.Fingerprint}}</code> · <span class="badge {{if .Revoked}}revoked{{end}}">{{.State}}</span></p></div>
{{if not .Revoked}}<form method="post" action="/agent/devices/revoke?token={{$.Token}}"><input type="hidden" name="device_id" value="{{.DeviceID}}"><button type="submit">Revoke</button></form>{{end}}</div>{{end}}
{{else}}<p>No phones have been authorized yet.</p>{{end}}
</section>
</main></body></html>`))

type pairingPageData struct {
	QRCode           string
	Target           string
	ExpiresAt        string
	VerificationCode string
	AgentFingerprint string
	Token            string
	Devices          []pairingDeviceRow
}

type pairingDeviceRow struct {
	DeviceID    string
	Name        string
	Platform    string
	Fingerprint string
	State       string
	Revoked     bool
}

type agentManagement struct {
	token       string
	pairingBase string
	pairing     *pairing.Manager
	identity    *deviceidentity.Store
}

func newAgentHTTPHandler(application http.Handler, listenAddress string, token string, pairingManager *pairing.Manager, identity *deviceidentity.Store) (http.Handler, error) {
	if application == nil {
		return nil, errors.New("application handler must not be nil")
	}
	if token == "" {
		return nil, errors.New("Agent management token must not be empty")
	}
	if pairingManager == nil || identity == nil {
		return nil, errors.New("Agent pairing dependencies must not be nil")
	}
	pairingBase, err := phonePairingBaseURL(listenAddress)
	if err != nil {
		return nil, err
	}
	management := &agentManagement{token: token, pairingBase: pairingBase, pairing: pairingManager, identity: identity}
	mux := http.NewServeMux()
	mux.Handle(localPairingPath, management.pairingPageHandler())
	mux.Handle(localRevokePath, management.revokeDeviceHandler())
	mux.Handle("/", application)
	return mux, nil
}

func (management *agentManagement) pairingPageHandler() http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		setManagementHeaders(writer, true)
		if request.Method != http.MethodGet {
			writer.Header().Set("Allow", http.MethodGet)
			http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !authorizeLocalManagement(writer, request, management.token) {
			return
		}

		data := pairingPageData{Token: url.QueryEscape(management.token)}
		descriptor, err := management.identity.Descriptor()
		if err != nil {
			http.Error(writer, "could not read Agent identity", http.StatusInternalServerError)
			return
		}
		data.AgentFingerprint, err = deviceidentity.Fingerprint(descriptor)
		if err != nil {
			http.Error(writer, "could not verify Agent identity", http.StatusInternalServerError)
			return
		}
		data.Devices, err = management.deviceRows()
		if err != nil {
			http.Error(writer, "could not read paired devices", http.StatusInternalServerError)
			return
		}
		if management.pairingBase != "" {
			connectionHint := strings.TrimSuffix(management.pairingBase, "/") + "/pairing/v1"
			invitation, createErr := management.pairing.Create([]string{connectionHint})
			if createErr != nil {
				http.Error(writer, "could not create pairing invitation", http.StatusInternalServerError)
				return
			}
			data.Target, err = pairing.FragmentURL(management.pairingBase, invitation)
			if err != nil {
				http.Error(writer, "could not encode pairing invitation", http.StatusInternalServerError)
				return
			}
			code, encodeErr := qr.Encode(data.Target, qr.M)
			if encodeErr != nil {
				http.Error(writer, "could not create pairing code", http.StatusInternalServerError)
				return
			}
			data.QRCode = base64.StdEncoding.EncodeToString(code.PNG())
			data.ExpiresAt = invitation.ExpiresAt.AsTime().Local().Format("15:04:05")
			data.VerificationCode = invitation.VerificationCode
		}
		writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = pairingPageTemplate.Execute(writer, data)
	})
}

func (management *agentManagement) revokeDeviceHandler() http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		setManagementHeaders(writer, false)
		if request.Method != http.MethodPost {
			writer.Header().Set("Allow", http.MethodPost)
			http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !authorizeLocalManagement(writer, request, management.token) {
			return
		}
		request.Body = http.MaxBytesReader(writer, request.Body, 4096)
		if err := request.ParseForm(); err != nil {
			http.Error(writer, "invalid revoke request", http.StatusBadRequest)
			return
		}
		deviceID, err := base64.RawURLEncoding.DecodeString(request.PostForm.Get("device_id"))
		if err != nil || len(deviceID) != deviceidentity.DeviceIDBytes {
			http.Error(writer, "invalid device ID", http.StatusBadRequest)
			return
		}
		if _, err := management.identity.Revoke(deviceID); err != nil {
			if errors.Is(err, deviceidentity.ErrDeviceNotFound) {
				http.Error(writer, "device not found", http.StatusNotFound)
				return
			}
			http.Error(writer, "could not revoke device", http.StatusInternalServerError)
			return
		}
		http.Redirect(writer, request, localPairingPath+"?token="+url.QueryEscape(management.token), http.StatusSeeOther)
	})
}

func (management *agentManagement) deviceRows() ([]pairingDeviceRow, error) {
	devices, err := management.identity.AuthorizedDevices(true)
	if err != nil {
		return nil, err
	}
	rows := make([]pairingDeviceRow, 0, len(devices))
	for _, device := range devices {
		fingerprint, err := deviceidentity.Fingerprint(device.Device)
		if err != nil {
			return nil, err
		}
		revoked := device.State == vibebridgev1.DeviceAuthorizationState_DEVICE_AUTHORIZATION_STATE_REVOKED
		state := "Authorized"
		if revoked {
			state = "Revoked"
		}
		rows = append(rows, pairingDeviceRow{
			DeviceID:    base64.RawURLEncoding.EncodeToString(device.Device.DeviceDescriptor.DeviceId),
			Name:        device.Device.DeviceDescriptor.DisplayName,
			Platform:    device.Device.DeviceDescriptor.Platform,
			Fingerprint: fingerprint,
			State:       state,
			Revoked:     revoked,
		})
	}
	return rows, nil
}

func setManagementHeaders(writer http.ResponseWriter, allowForms bool) {
	formAction := "'none'"
	if allowForms {
		formAction = "'self'"
	}
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Content-Security-Policy", fmt.Sprintf("default-src 'none'; img-src data:; style-src 'unsafe-inline'; base-uri 'none'; form-action %s; frame-ancestors 'none'", formAction))
	writer.Header().Set("Referrer-Policy", "no-referrer")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
}

func authorizeLocalManagement(writer http.ResponseWriter, request *http.Request, token string) bool {
	if !isLocalMachineRemote(request.RemoteAddr) {
		http.Error(writer, "local access only", http.StatusForbidden)
		return false
	}
	provided := request.URL.Query().Get("token")
	if subtle.ConstantTimeCompare([]byte(provided), []byte(token)) != 1 {
		http.Error(writer, "unauthorized", http.StatusUnauthorized)
		return false
	}
	return true
}

func phonePairingBaseURL(listenAddress string) (string, error) {
	urls, err := accessURLs(listenAddress, "pairing-placeholder")
	if err != nil {
		return "", err
	}
	for _, candidate := range urls {
		parsed, err := url.Parse(candidate)
		if err != nil {
			continue
		}
		ip := net.ParseIP(parsed.Hostname())
		if ip != nil && ip.IsLoopback() {
			continue
		}
		parsed.RawQuery = ""
		parsed.Fragment = ""
		parsed.Path = "/"
		return parsed.String(), nil
	}
	return "", nil
}

func isLocalMachineRemote(remoteAddress string) bool {
	host, _, err := net.SplitHostPort(remoteAddress)
	if err != nil {
		host = strings.Trim(remoteAddress, "[]")
	}
	host, _, _ = strings.Cut(host, "%")
	return isLocalMachineIP(net.ParseIP(host))
}

func isLocalMachineIP(candidate net.IP) bool {
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		return candidate != nil && candidate.IsLoopback()
	}
	return matchesLocalMachineIP(candidate, addresses)
}

func matchesLocalMachineIP(candidate net.IP, addresses []net.Addr) bool {
	if candidate == nil {
		return false
	}
	if candidate.IsLoopback() {
		return true
	}
	for _, address := range addresses {
		localIP := addressIP(address)
		if localIP != nil && localIP.Equal(candidate) {
			return true
		}
	}
	return false
}
