package main

import (
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"html/template"
	"net"
	"net/http"
	"net/url"
	"strings"

	"rsc.io/qr"
)

const localPairingPath = "/agent/pair"

var pairingPageTemplate = template.Must(template.New("pairing").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="referrer" content="no-referrer">
<title>Pair a phone · VibeBridge</title>
<style>
:root { color-scheme: dark; font-family: ui-sans-serif, system-ui, sans-serif; background: #09090b; color: #fafafa; }
body { min-height: 100vh; margin: 0; display: grid; place-items: center; }
main { width: min(92vw, 30rem); text-align: center; padding: 2rem; }
img { width: min(72vw, 20rem); height: auto; padding: 1rem; border-radius: 1rem; background: white; }
p { color: #a1a1aa; line-height: 1.5; }
code { overflow-wrap: anywhere; color: #d8b4fe; }
</style>
</head>
<body><main>
<h1>Pair a phone</h1>
{{if .QRCode}}<img src="data:image/png;base64,{{.QRCode}}" alt="VibeBridge phone pairing QR code">
<p>Connect the phone to the same trusted network, then scan this code.</p>
<code>{{.Target}}</code>
{{else}}<p>No private-network address is available. Connect this computer to your trusted network and try again.</p>{{end}}
<p>This local access code expires when the Agent restarts. Persistent device pairing will replace it before remote access is enabled.</p>
</main></body></html>`))

type pairingPageData struct {
	QRCode string
	Target string
}

func newAgentHTTPHandler(application http.Handler, listenAddress string, token string) (http.Handler, error) {
	if application == nil {
		return nil, errors.New("application handler must not be nil")
	}
	if token == "" {
		return nil, errors.New("Agent management token must not be empty")
	}
	target, err := phoneAccessURL(listenAddress, token)
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.Handle(localPairingPath, localPairingHandler(token, target))
	mux.Handle("/", application)
	return mux, nil
}

func localPairingHandler(token string, target string) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Cache-Control", "no-store")
		writer.Header().Set("Content-Security-Policy", "default-src 'none'; img-src data:; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
		writer.Header().Set("Referrer-Policy", "no-referrer")
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		if request.Method != http.MethodGet {
			writer.Header().Set("Allow", http.MethodGet)
			http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !isLocalMachineRemote(request.RemoteAddr) {
			http.Error(writer, "local access only", http.StatusForbidden)
			return
		}
		provided := request.URL.Query().Get("token")
		if subtle.ConstantTimeCompare([]byte(provided), []byte(token)) != 1 {
			http.Error(writer, "unauthorized", http.StatusUnauthorized)
			return
		}

		data := pairingPageData{Target: target}
		if target != "" {
			code, err := qr.Encode(target, qr.M)
			if err != nil {
				http.Error(writer, "could not create pairing code", http.StatusInternalServerError)
				return
			}
			data.QRCode = base64.StdEncoding.EncodeToString(code.PNG())
		}
		writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := pairingPageTemplate.Execute(writer, data); err != nil {
			return
		}
	})
}

func phoneAccessURL(listenAddress string, token string) (string, error) {
	urls, err := accessURLs(listenAddress, token)
	if err != nil {
		return "", err
	}
	for _, candidate := range urls {
		parsed, err := url.Parse(candidate)
		if err != nil {
			continue
		}
		host := parsed.Hostname()
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return candidate, nil
		}
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
