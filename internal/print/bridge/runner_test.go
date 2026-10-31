package bridge_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yaping-pro/agyapi/internal/config"
	"github.com/yaping-pro/agyapi/internal/print/bridge"
)

func TestCappedBuffer_WriteAndTruncation(t *testing.T) {
	buf := bridge.NewCappedBuffer(10)

	n, err := buf.Write([]byte("hello "))
	if err != nil {
		t.Fatalf("unexpected write error: %v", err)
	}
	if n != 6 {
		t.Fatalf("expected n=6, got %d", n)
	}
	if buf.Len() != 6 {
		t.Fatalf("expected len=6, got %d", buf.Len())
	}

	n, err = buf.Write([]byte("world!"))
	if err != nil {
		t.Fatalf("unexpected write error: %v", err)
	}
	if n != 6 {
		t.Fatalf("expected n=6 to avoid ErrShortWrite, got %d", n)
	}
	if buf.Len() != 10 {
		t.Fatalf("expected len=10, got %d", buf.Len())
	}
	if buf.String() != "hello worl" {
		t.Fatalf("expected 'hello worl', got '%s'", buf.String())
	}

	// Further writes are discarded without error
	n, err = buf.Write([]byte("extra content"))
	if err != nil {
		t.Fatalf("unexpected write error on full buffer: %v", err)
	}
	if n != len("extra content") {
		t.Fatalf("expected n=%d, got %d", len("extra content"), n)
	}
	if buf.Len() != 10 {
		t.Fatalf("expected len=10 after discard, got %d", buf.Len())
	}
	if buf.String() != "hello worl" {
		t.Fatalf("expected 'hello worl', got '%s'", buf.String())
	}
}

func TestCappedBuffer_Concurrency(t *testing.T) {
	const maxCap = 1024
	buf := bridge.NewCappedBuffer(maxCap)

	var wg sync.WaitGroup
	const goroutines = 30
	const writesPerGoroutine = 20

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < writesPerGoroutine; j++ {
				_, _ = buf.Write([]byte(fmt.Sprintf("[g%d-w%d]", id, j)))
				_ = buf.String()
				_ = buf.Len()
				_ = buf.Bytes()
			}
		}(i)
	}

	wg.Wait()

	if buf.Len() > maxCap {
		t.Errorf("buffer length %d exceeds max capacity %d", buf.Len(), maxCap)
	}
}

func writeMockScript(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "mock_agy.sh")
	if err := os.WriteFile(scriptPath, []byte(content), 0755); err != nil {
		t.Fatalf("failed to write mock script: %v", err)
	}
	return scriptPath
}

func TestRunAgyNonStream_Success(t *testing.T) {
	script := `#!/bin/bash
# Verify arguments
if [ "$1" != "--output-format" ] || [ "$2" != "json" ] || [ "$3" != "--disable-slash-commands" ]; then
    echo "Invalid args: $@" >&2
    exit 1
fi
# Read stdin prompt
stdin_content=$(cat)
if [ -z "$stdin_content" ]; then
    echo "Empty stdin" >&2
    exit 2
fi

cat << 'EOF'
{
  "conversation_id": "conv-test-123",
  "status": "SUCCESS",
  "response": "Hello from mock non-stream!",
  "duration_seconds": 0.45,
  "usage": {
    "input_tokens": 42,
    "output_tokens": 18,
    "thinking_tokens": 0,
    "total_tokens": 60
  }
}
EOF
`
	scriptPath := writeMockScript(t, script)
	cfg := &config.Config{AgyPath: scriptPath}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := bridge.RunAgyNonStream(ctx, cfg, "Ping non-stream", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.ConversationID != "conv-test-123" {
		t.Errorf("expected conversation_id 'conv-test-123', got '%s'", resp.ConversationID)
	}
	if resp.Status != "SUCCESS" {
		t.Errorf("expected status 'SUCCESS', got '%s'", resp.Status)
	}
	if resp.Response != "Hello from mock non-stream!" {
		t.Errorf("unexpected response: %s", resp.Response)
	}
	if resp.Usage.InputTokens != 42 || resp.Usage.OutputTokens != 18 || resp.Usage.TotalTokens != 60 {
		t.Errorf("unexpected usage: %+v", resp.Usage)
	}
}

func TestRunAgyNonStream_WithModel(t *testing.T) {
	script := `#!/bin/bash
model_found=false
for arg in "$@"; do
    if [ "$arg" = "custom-model-x" ]; then
        model_found=true
    fi
done

if [ "$model_found" != "true" ]; then
    echo "Model argument missing" >&2
    exit 1
fi

cat << 'EOF'
{
  "conversation_id": "conv-model",
  "status": "SUCCESS",
  "response": "Model OK",
  "duration_seconds": 0.1,
  "usage": {
    "input_tokens": 10,
    "output_tokens": 5,
    "total_tokens": 15
  }
}
EOF
`
	scriptPath := writeMockScript(t, script)
	cfg := &config.Config{AgyPath: scriptPath}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := bridge.RunAgyNonStream(ctx, cfg, "Test with model", "custom-model-x")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Response != "Model OK" {
		t.Errorf("expected 'Model OK', got '%s'", resp.Response)
	}
}

func TestRunAgyNonStream_FailureReturnsStderr(t *testing.T) {
	script := `#!/bin/bash
echo "Fatal error: API quota exceeded" >&2
exit 1
`
	scriptPath := writeMockScript(t, script)
	cfg := &config.Config{AgyPath: scriptPath}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := bridge.RunAgyNonStream(ctx, cfg, "Will fail", "")
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "Fatal error: API quota exceeded") {
		t.Errorf("expected error to contain stderr, got: %v", err)
	}
}

func TestRunAgyNonStream_ContextCancellation(t *testing.T) {
	script := `#!/bin/bash
trap "exit 143" TERM
sleep 10
`
	scriptPath := writeMockScript(t, script)
	cfg := &config.Config{AgyPath: scriptPath}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := bridge.RunAgyNonStream(ctx, cfg, "Will cancel", "")
	duration := time.Since(start)

	if err == nil {
		t.Fatalf("expected context error, got nil")
	}
	if duration > 2*time.Second {
		t.Errorf("expected process to be terminated quickly on cancellation, took: %v", duration)
	}
}

func TestRunAgyStream_Success(t *testing.T) {
	script := `#!/bin/bash
# Verify arguments
if [ "$1" != "--output-format" ] || [ "$2" != "stream-json" ] || [ "$3" != "--disable-slash-commands" ]; then
    echo "Invalid args: $@" >&2
    exit 1
fi
stdin_content=$(cat)
if [ -z "$stdin_content" ]; then
    echo "Empty stdin" >&2
    exit 2
fi

echo '{"event":"init"}'
echo '{"event":"step_update","step_update":{"state":"ACTIVE","text_delta":"Hello "}}'
echo '{"event":"step_update","step_update":{"state":"ACTIVE","text_delta":"streaming "}}'
echo '{"event":"step_update","step_update":{"state":"ACTIVE","text_delta":"world!"}}'
echo '{"event":"result","result":{"status":"SUCCESS","response":"Hello streaming world!","usage":{"input_tokens":30,"output_tokens":12,"total_tokens":42}}}'
`
	scriptPath := writeMockScript(t, script)
	cfg := &config.Config{AgyPath: scriptPath}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var deltas []string
	var doneResult *bridge.ResultInfo

	onDelta := func(delta string) error {
		deltas = append(deltas, delta)
		return nil
	}
	onDone := func(res *bridge.ResultInfo) error {
		doneResult = res
		return nil
	}

	err := bridge.RunAgyStream(ctx, cfg, "Ping stream", "", onDelta, onDone)
	if err != nil {
		t.Fatalf("unexpected stream error: %v", err)
	}

	combined := strings.Join(deltas, "")
	if combined != "Hello streaming world!" {
		t.Errorf("expected 'Hello streaming world!', got '%s'", combined)
	}
	if len(deltas) != 3 {
		t.Errorf("expected 3 deltas, got %d", len(deltas))
	}
	if doneResult == nil {
		t.Fatalf("expected doneResult to be non-nil")
	}
	if doneResult.Status != "SUCCESS" {
		t.Errorf("expected status 'SUCCESS', got '%s'", doneResult.Status)
	}
	if doneResult.Usage.TotalTokens != 42 {
		t.Errorf("expected total tokens 42, got %d", doneResult.Usage.TotalTokens)
	}
}

func TestRunAgyStream_FailureReturnsStderr(t *testing.T) {
	script := `#!/bin/bash
echo "Stream init failed: unauthorized" >&2
exit 1
`
	scriptPath := writeMockScript(t, script)
	cfg := &config.Config{AgyPath: scriptPath}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := bridge.RunAgyStream(ctx, cfg, "Will fail", "", func(string) error { return nil }, nil)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "Stream init failed: unauthorized") {
		t.Errorf("expected error to contain stderr, got: %v", err)
	}
}

func TestRunAgyStream_CallbackErrorStopsProcess(t *testing.T) {
	script := `#!/bin/bash
echo '{"event":"step_update","step_update":{"state":"ACTIVE","text_delta":"chunk1"}}'
# Sleep so that we verify client callback error interrupts process
sleep 5
echo '{"event":"step_update","step_update":{"state":"ACTIVE","text_delta":"chunk2"}}'
`
	scriptPath := writeMockScript(t, script)
	cfg := &config.Config{AgyPath: scriptPath}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	start := time.Now()
	err := bridge.RunAgyStream(ctx, cfg, "Test cb error", "", func(delta string) error {
		return fmt.Errorf("client connection dropped")
	}, nil)
	duration := time.Since(start)

	if err == nil {
		t.Fatalf("expected callback error, got nil")
	}
	if !strings.Contains(err.Error(), "client connection dropped") {
		t.Errorf("expected callback error message, got: %v", err)
	}
	if duration > 2*time.Second {
		t.Errorf("process was not terminated promptly on callback error, took: %v", duration)
	}
}

func TestRunAgyStream_ContextCancellation(t *testing.T) {
	script := `#!/bin/bash
trap "exit 143" TERM
echo '{"event":"step_update","step_update":{"state":"ACTIVE","text_delta":"first"}}'
sleep 10
`
	scriptPath := writeMockScript(t, script)
	cfg := &config.Config{AgyPath: scriptPath}

	ctx, cancel := context.WithCancel(context.Background())

	received := make(chan struct{})
	errChan := make(chan error, 1)

	go func() {
		err := bridge.RunAgyStream(ctx, cfg, "Test cancel", "", func(delta string) error {
			close(received)
			return nil
		}, nil)
		errChan <- err
	}()

	select {
	case <-received:
		cancel()
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for first delta")
	}

	select {
	case err := <-errChan:
		if err == nil {
			t.Fatalf("expected error on cancelled context, got nil")
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for RunAgyStream to return after cancellation")
	}
}

func TestRunAgyNonStream_ErrorStatus(t *testing.T) {
	script := `#!/bin/bash
cat << 'EOF'
{
  "conversation_id": "conv-err",
  "status": "ERROR",
  "response": "Rate limit exceeded or backend error",
  "duration_seconds": 0.2,
  "usage": {
    "input_tokens": 10,
    "output_tokens": 0,
    "total_tokens": 10
  }
}
EOF
`
	scriptPath := writeMockScript(t, script)
	cfg := &config.Config{AgyPath: scriptPath}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := bridge.RunAgyNonStream(ctx, cfg, "Ping error", "")
	if err == nil {
		t.Fatalf("expected error for non-SUCCESS status, got nil")
	}
	if !strings.Contains(err.Error(), `agy returned error status "ERROR"`) {
		t.Errorf("expected error to mention error status, got: %v", err)
	}
	if !strings.Contains(err.Error(), "Rate limit exceeded or backend error") {
		t.Errorf("expected error to include response text, got: %v", err)
	}
}

func TestRunAgyStream_ErrorStatus(t *testing.T) {
	script := `#!/bin/bash
cat << 'EOF'
{"event":"init"}
{"event":"step_update","step_update":{"state":"ACTIVE","text_delta":"Something wrong..."}}
{"event":"result","result":{"status":"ERROR","response":"Model service crashed","usage":{"input_tokens":5,"output_tokens":2,"total_tokens":7}}}
EOF
`
	scriptPath := writeMockScript(t, script)
	cfg := &config.Config{AgyPath: scriptPath}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var calledDone bool
	err := bridge.RunAgyStream(ctx, cfg, "Ping stream error", "", func(string) error { return nil }, func(res *bridge.ResultInfo) error {
		calledDone = true
		return nil
	})
	if err == nil {
		t.Fatalf("expected error for stream non-SUCCESS status, got nil")
	}
	if !strings.Contains(err.Error(), `agy returned error status "ERROR"`) {
		t.Errorf("expected error to mention status, got: %v", err)
	}
	if !strings.Contains(err.Error(), "Model service crashed") {
		t.Errorf("expected error to include response text, got: %v", err)
	}
	if calledDone {
		t.Errorf("expected onDone NOT to be called when status is ERROR")
	}
}
