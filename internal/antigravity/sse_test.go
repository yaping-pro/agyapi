package antigravity

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func collect(t *testing.T, body string) []string {
	t.Helper()
	s := NewSSE(strings.NewReader(body))
	var out []string
	for {
		payload, err := s.Next()
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		out = append(out, string(payload))
	}
}

func TestSSESkipsKeepAliveComments(t *testing.T) {
	got := collect(t, ": keep-alive\n\ndata: {\"a\":1}\n\n: keep-alive\n\n")
	if len(got) != 1 || got[0] != `{"a":1}` {
		t.Fatalf("events = %q", got)
	}
}

func TestSSEMultipleEvents(t *testing.T) {
	got := collect(t, "data: one\n\ndata: two\n\n")
	if len(got) != 2 || got[0] != "one" || got[1] != "two" {
		t.Fatalf("events = %q", got)
	}
}

func TestSSEMultiLineDataIsJoined(t *testing.T) {
	got := collect(t, "data: line1\ndata: line2\n\n")
	if len(got) != 1 || got[0] != "line1\nline2" {
		t.Fatalf("events = %q", got)
	}
}

func TestSSEHandlesCRLF(t *testing.T) {
	got := collect(t, "data: a\r\n\r\ndata: b\r\n\r\n")
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("events = %q", got)
	}
}

func TestSSEFlushesTrailingEventWithoutBlankLine(t *testing.T) {
	got := collect(t, "data: tail\n")
	if len(got) != 1 || got[0] != "tail" {
		t.Fatalf("events = %q", got)
	}
}

func TestSSEFieldWithoutDataIsIgnored(t *testing.T) {
	got := collect(t, "event: ping\nid: 3\n\ndata: real\n\n")
	if len(got) != 1 || got[0] != "real" {
		t.Fatalf("events = %q", got)
	}
}

func TestSSEEmptyStreamYieldsNothing(t *testing.T) {
	if got := collect(t, ""); len(got) != 0 {
		t.Fatalf("events = %q", got)
	}
}
