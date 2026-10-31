package bridge_test

import (
	"testing"

	"github.com/yaping-pro/agyapi/internal/print/bridge"
)

func TestParseStreamEvent_TextDelta(t *testing.T) {
	line := `{"event":"step_update","step_update":{"conversation_id":"123","step_index":3,"state":"ACTIVE","text_delta":"Hello world"}}`
	delta, done, err := bridge.ParseStreamEvent([]byte(line))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if done {
		t.Errorf("expected done=false, got true")
	}
	if delta != "Hello world" {
		t.Errorf("expected delta 'Hello world', got '%s'", delta)
	}
}

func TestParseStreamEvent_Result(t *testing.T) {
	line := `{"event":"result","result":{"conversation_id":"123","status":"SUCCESS","response":"Hello world\n","usage":{"input_tokens":100,"output_tokens":20,"thinking_tokens":0,"total_tokens":120}}}`
	delta, done, err := bridge.ParseStreamEvent([]byte(line))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !done {
		t.Errorf("expected done=true, got false")
	}
	if delta != "" {
		t.Errorf("expected empty delta for result event, got '%s'", delta)
	}
}

func TestParseStreamEvent_InitEvent(t *testing.T) {
	line := `{"event":"init","init":{"model":"gemini-3.8-flash-high"}}`
	delta, done, err := bridge.ParseStreamEvent([]byte(line))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if done {
		t.Errorf("expected done=false for init event, got true")
	}
	if delta != "" {
		t.Errorf("expected empty delta for init event, got '%s'", delta)
	}
}

func TestParseStreamEvent_OtherEvent(t *testing.T) {
	line := `{"event":"step_start"}`
	delta, done, err := bridge.ParseStreamEvent([]byte(line))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if done {
		t.Errorf("expected done=false, got true")
	}
	if delta != "" {
		t.Errorf("expected empty delta, got '%s'", delta)
	}
}

func TestParseStreamEvent_EmptyLine(t *testing.T) {
	testCases := []string{
		"",
		"   ",
		"\n",
		"\r\n",
	}
	for _, tc := range testCases {
		delta, done, err := bridge.ParseStreamEvent([]byte(tc))
		if err != nil {
			t.Fatalf("unexpected error on empty line: %v", err)
		}
		if done {
			t.Errorf("expected done=false for empty line, got true")
		}
		if delta != "" {
			t.Errorf("expected empty delta for empty line, got '%s'", delta)
		}
	}
}

func TestParseStreamEvent_InvalidJSON(t *testing.T) {
	line := `{"event": "broken json`
	_, _, err := bridge.ParseStreamEvent([]byte(line))
	if err == nil {
		t.Fatalf("expected error for invalid json, got nil")
	}
}

func TestParseStreamLine_Details(t *testing.T) {
	line := `{"event":"result","result":{"conversation_id":"conv-999","status":"SUCCESS","response":"Done!","usage":{"input_tokens":50,"output_tokens":10}}}`
	ev, err := bridge.ParseStreamLine([]byte(line))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ev == nil {
		t.Fatalf("expected non-nil StreamEvent")
	}
	if ev.Event != "result" {
		t.Errorf("expected event 'result', got '%s'", ev.Event)
	}
	if ev.Result == nil {
		t.Fatalf("expected non-nil Result")
	}
	if ev.Result.Status != "SUCCESS" {
		t.Errorf("expected status 'SUCCESS', got '%s'", ev.Result.Status)
	}
	if ev.Result.Response != "Done!" {
		t.Errorf("expected response 'Done!', got '%s'", ev.Result.Response)
	}
	if ev.Result.Usage.InputTokens != 50 || ev.Result.Usage.OutputTokens != 10 {
		t.Errorf("unexpected usage: %+v", ev.Result.Usage)
	}
}
