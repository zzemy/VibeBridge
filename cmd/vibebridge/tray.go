package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/zzemy/VibeBridge/internal/pairingflow"
	"github.com/zzemy/VibeBridge/internal/server"
)

const trayStatusTimeout = time.Second

type agentTrayOptions struct {
	AppURL            string
	PairingURL        string
	StatusURL         string
	PairingStatusURL  string
	PairingApproveURL string
	PairingRejectURL  string
	RequestStop       func()
	StatusPeriod      time.Duration
}

func (options agentTrayOptions) validate() error {
	for name, value := range map[string]string{
		"app URL":             options.AppURL,
		"pairing URL":         options.PairingURL,
		"status URL":          options.StatusURL,
		"pairing status URL":  options.PairingStatusURL,
		"pairing approve URL": options.PairingApproveURL,
		"pairing reject URL":  options.PairingRejectURL,
	} {
		parsed, err := url.ParseRequestURI(value)
		if err != nil || parsed.Scheme != "http" || parsed.Host == "" {
			return fmt.Errorf("tray %s is invalid", name)
		}
		host, _, _ := strings.Cut(parsed.Hostname(), "%")
		if !isLocalMachineIP(net.ParseIP(host)) {
			return fmt.Errorf("tray %s must target this computer", name)
		}
	}
	if options.RequestStop == nil {
		return errors.New("tray stop callback must not be nil")
	}
	if options.StatusPeriod <= 0 {
		return errors.New("tray status period must be positive")
	}
	return nil
}

func trayURLs(listenAddress string, token string) (appURL string, pairingURL string, statusURL string, err error) {
	host, port, err := net.SplitHostPort(listenAddress)
	if err != nil {
		return "", "", "", err
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	base := url.URL{Scheme: "http", Host: net.JoinHostPort(host, port)}
	query := url.Values{"token": []string{token}}

	base.Path = "/"
	base.RawQuery = query.Encode()
	appURL = base.String()
	base.Path = "/agent/pair"
	pairingURL = base.String()
	base.Path = "/status"
	statusURL = base.String()
	return appURL, pairingURL, statusURL, nil
}

func trayPairingURLs(listenAddress string, token string) (statusURL string, approveURL string, rejectURL string, err error) {
	host, port, err := net.SplitHostPort(listenAddress)
	if err != nil {
		return "", "", "", err
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	base := url.URL{Scheme: "http", Host: net.JoinHostPort(host, port), RawQuery: url.Values{"token": []string{token}}.Encode()}
	base.Path = localPairingStatusPath
	statusURL = base.String()
	base.Path = localPairingApprovePath
	approveURL = base.String()
	base.Path = localPairingRejectPath
	rejectURL = base.String()
	return statusURL, approveURL, rejectURL, nil
}

func queryTrayStatus(ctx context.Context, endpoint string) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return "", errors.New("Agent is unreachable")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Agent returned HTTP %d", response.StatusCode)
	}
	var status server.SessionStatus
	decoder := json.NewDecoder(response.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&status); err != nil {
		return "", errors.New("Agent returned an invalid status")
	}
	switch status.State {
	case "idle":
		return "Agent online · no active session", nil
	case "connected":
		return "Agent online · session connected", nil
	case "detached":
		return "Agent online · session waiting", nil
	default:
		return "Agent online", nil
	}
}

func queryTrayPairingStatus(ctx context.Context, endpoint string) (localPairingStatus, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return localPairingStatus{}, err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return localPairingStatus{}, errors.New("pairing status is unreachable")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return localPairingStatus{}, fmt.Errorf("pairing status returned HTTP %d", response.StatusCode)
	}
	var status localPairingStatus
	decoder := json.NewDecoder(io.LimitReader(response.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&status); err != nil {
		return localPairingStatus{}, errors.New("Agent returned an invalid pairing status")
	}
	switch status.State {
	case "idle":
		if status.FlowID != "" || status.SAS != "" {
			return localPairingStatus{}, errors.New("Agent returned an invalid idle pairing status")
		}
	case string(pairingflow.StateHandshaking):
		if status.FlowID == "" || status.DisplayName == "" || status.SAS != "" {
			return localPairingStatus{}, errors.New("Agent returned an invalid handshaking status")
		}
	case string(pairingflow.StatePending):
		if status.FlowID == "" || status.DisplayName == "" || len(status.SAS) != 7 {
			return localPairingStatus{}, errors.New("Agent returned an invalid pending status")
		}
	default:
		return localPairingStatus{}, errors.New("Agent returned an unknown pairing status")
	}
	return status, nil
}

func postTrayPairingDecision(ctx context.Context, endpoint, flowID string) error {
	form := url.Values{"flow_id": []string{flowID}}.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return errors.New("pairing decision is unreachable")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		return fmt.Errorf("pairing decision returned HTTP %d", response.StatusCode)
	}
	return nil
}
