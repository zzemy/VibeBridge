package server

import (
	"crypto/rand"
	"encoding/hex"

	"github.com/zzemy/VibeBridge/internal/agentlog"
)

type sessionTelemetry struct {
	correlationID string
	logger        agentlog.Logger
}

func newSessionCorrelationID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

func (s *ptySession) logEvent(name agentlog.Name, state agentlog.State, reason agentlog.Reason, outcome agentlog.Outcome) {
	if s.telemetry.logger == nil {
		return
	}
	s.telemetry.logger.Log(agentlog.Event{
		Name:      name,
		SessionID: s.telemetry.correlationID,
		State:     state,
		Reason:    reason,
		Outcome:   outcome,
	})
}
