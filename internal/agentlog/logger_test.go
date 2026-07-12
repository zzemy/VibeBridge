package agentlog

import (
	"bytes"
	"encoding/json"
	"reflect"
	"sort"
	"testing"
)

func TestJSONLoggerUsesPrivacyAllowlistSchema(t *testing.T) {
	var output bytes.Buffer
	logger := NewJSON(&output)
	logger.Log(Event{
		Name:      EventSessionEnded,
		SessionID: "opaque-session-id",
		State:     StateEnded,
		Reason:    ReasonExplicitEnd,
		Outcome:   OutcomeSuccess,
	})

	var record map[string]any
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatalf("decode JSON log: %v", err)
	}

	keys := make([]string, 0, len(record))
	for key := range record {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	wantKeys := []string{"event", "level", "msg", "outcome", "reason", "session_id", "state", "time"}
	if !reflect.DeepEqual(keys, wantKeys) {
		t.Fatalf("log keys = %q, want fixed allowlist %q", keys, wantKeys)
	}
	if record["event"] != string(EventSessionEnded) || record["session_id"] != "opaque-session-id" {
		t.Fatalf("unexpected structured event: %#v", record)
	}
}

func TestJSONLoggerOmitsEmptyOptionalFields(t *testing.T) {
	var output bytes.Buffer
	NewJSON(&output).Log(Event{Name: EventAgentStarting})

	var record map[string]any
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatalf("decode JSON log: %v", err)
	}
	for _, field := range []string{"session_id", "state", "reason", "outcome"} {
		if _, exists := record[field]; exists {
			t.Fatalf("empty optional field %q was emitted", field)
		}
	}
}
