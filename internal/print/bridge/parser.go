package bridge

import (
	"bytes"
	"encoding/json"
)

// ParseStreamLine parses a single NDJSON line into a StreamEvent struct.
// Returns nil, nil if the line is empty or whitespace-only.
func ParseStreamLine(line []byte) (*StreamEvent, error) {
	trimmed := bytes.TrimSpace(line)
	if len(trimmed) == 0 {
		return nil, nil
	}

	var ev StreamEvent
	if err := json.Unmarshal(trimmed, &ev); err != nil {
		return nil, err
	}
	return &ev, nil
}

// ParseStreamEvent parses a single line of NDJSON from agy streaming output.
// It extracts text deltas for step_update events, signals completion for result events,
// and gracefully ignores lifecycle events (e.g. init) and empty lines.
func ParseStreamEvent(line []byte) (delta string, done bool, err error) {
	ev, err := ParseStreamLine(line)
	if err != nil {
		return "", false, err
	}
	if ev == nil {
		return "", false, nil
	}

	if ev.Event == "step_update" && ev.StepUpdate != nil {
		return ev.StepUpdate.TextDelta, false, nil
	}

	if ev.Event == "result" {
		return "", true, nil
	}

	return "", false, nil
}
