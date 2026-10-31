package bridge

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/yaping-pro/agyapi/internal/config"
)

// CappedBuffer is a thread-safe io.Writer with an upper bound on memory capacity.
// Writes that exceed the cap are silently discarded at the tail to prevent memory exhaustion
// and pipe buffer deadlocks while avoiding ErrShortWrite errors.
type CappedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
	max int
}

// NewCappedBuffer creates a new CappedBuffer with the specified maximum byte size.
func NewCappedBuffer(max int) *CappedBuffer {
	return &CappedBuffer{max: max}
}

// Write appends data to the buffer up to its maximum capacity.
func (c *CappedBuffer) Write(p []byte) (n int, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.max <= 0 {
		return len(p), nil
	}

	if c.buf.Len() >= c.max {
		return len(p), nil
	}

	remaining := c.max - c.buf.Len()
	if len(p) > remaining {
		_, _ = c.buf.Write(p[:remaining])
		return len(p), nil
	}

	return c.buf.Write(p)
}

// String returns the contents of the buffer as a string.
func (c *CappedBuffer) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}

// Bytes returns a copy of the buffer's contents.
func (c *CappedBuffer) Bytes() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]byte, c.buf.Len())
	copy(out, c.buf.Bytes())
	return out
}

// Len returns the current number of bytes in the buffer.
func (c *CappedBuffer) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.Len()
}

// Cap returns the maximum capacity of the buffer.
func (c *CappedBuffer) Cap() int {
	return c.max
}

// Reset clears the buffer contents.
func (c *CappedBuffer) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.buf.Reset()
}
func cleanupConversation(convID string) {
	if convID == "" {
		return
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return
	}
	base := filepath.Join(home, ".gemini", "antigravity-cli")
	convBase := filepath.Join(base, "conversations", convID+".db")
	for _, suffix := range []string{"", "-shm", "-wal", ".bak", ".bak-shm", ".bak-wal"} {
		_ = os.Remove(convBase + suffix)
	}
	_ = os.RemoveAll(filepath.Join(base, "brain", convID))
	_ = os.Remove(filepath.Join(base, "annotations", convID+".pb"))
}

// RunAgyNonStream executes the agy CLI non-interactively in non-streaming mode.
// It writes prompt to stdin, drains stderr with a capped buffer, and parses JSON output.
func RunAgyNonStream(ctx context.Context, cfg *config.Config, prompt string, model string) (*AgyJSONResponse, error) {
	args := []string{"--output-format", "json", "--disable-slash-commands"}
	if model != "" {
		args = append(args, "--model", model)
	}

	cmd := exec.CommandContext(ctx, cfg.AgyPath, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}
	cmd.Cancel = func() error {
		if cmd.Process != nil && cmd.Process.Pid > 0 {
			pid := cmd.Process.Pid
			err := syscall.Kill(-pid, syscall.SIGTERM)
			time.AfterFunc(cmd.WaitDelay, func() {
				_ = syscall.Kill(-pid, syscall.SIGKILL)
			})
			return err
		}
		return nil
	}
	cmd.WaitDelay = 3 * time.Second

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to open stdin pipe: %w", err)
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to open stdout pipe: %w", err)
	}

	stderrBuf := NewCappedBuffer(128 * 1024)
	cmd.Stderr = stderrBuf

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start agy: %w", err)
	}

	go func() {
		defer stdinPipe.Close()
		_, _ = io.WriteString(stdinPipe, prompt)
	}()

	var stdoutBuf bytes.Buffer
	_, copyErr := io.Copy(&stdoutBuf, stdoutPipe)
	waitErr := cmd.Wait()

	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if waitErr != nil {
		return nil, fmt.Errorf("agy failed: %w, stderr: %s", waitErr, stderrBuf.String())
	}
	if copyErr != nil {
		return nil, fmt.Errorf("failed to read agy stdout: %w", copyErr)
	}

	var resp AgyJSONResponse
	if err := json.Unmarshal(stdoutBuf.Bytes(), &resp); err != nil {
		return nil, fmt.Errorf("failed to parse agy json output: %w (output: %s)", err, stdoutBuf.String())
	}
	if resp.Status != "" && resp.Status != "SUCCESS" {
		return nil, fmt.Errorf("agy returned error status %q: %s", resp.Status, resp.Response)
	}
	if resp.Usage.TotalTokens == 0 {
		resp.Usage.TotalTokens = resp.Usage.InputTokens + resp.Usage.OutputTokens
	}
	cleanupConversation(resp.ConversationID)

	return &resp, nil
}

// RunAgyStream executes the agy CLI in streaming mode (--output-format stream-json).
// It streams incremental text deltas to onDelta and reports final results to onDone.
func RunAgyStream(ctx context.Context, cfg *config.Config, prompt string, model string, onDelta func(string) error, onDone func(*ResultInfo) error) error {
	streamCtx, streamCancel := context.WithCancel(ctx)
	defer streamCancel()

	args := []string{"--output-format", "stream-json", "--disable-slash-commands"}
	if model != "" {
		args = append(args, "--model", model)
	}

	cmd := exec.CommandContext(streamCtx, cfg.AgyPath, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}
	cmd.Cancel = func() error {
		if cmd.Process != nil && cmd.Process.Pid > 0 {
			pid := cmd.Process.Pid
			err := syscall.Kill(-pid, syscall.SIGTERM)
			time.AfterFunc(cmd.WaitDelay, func() {
				_ = syscall.Kill(-pid, syscall.SIGKILL)
			})
			return err
		}
		return nil
	}
	cmd.WaitDelay = 3 * time.Second

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to open stdin pipe: %w", err)
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to open stdout pipe: %w", err)
	}

	stderrBuf := NewCappedBuffer(128 * 1024)
	cmd.Stderr = stderrBuf

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start agy: %w", err)
	}

	go func() {
		defer stdinPipe.Close()
		_, _ = io.WriteString(stdinPipe, prompt)
	}()

	var (
		cbErr      error
		readErr    error
		lastConvID string
	)
	defer func() {
		if lastConvID != "" {
			cleanupConversation(lastConvID)
		}
	}()
	reader := bufio.NewReader(stdoutPipe)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			ev, parseErr := ParseStreamLine(line)
			if parseErr == nil && ev != nil {
				if ev.Event == "step_update" && ev.StepUpdate != nil && ev.StepUpdate.TextDelta != "" {
					if onDelta != nil {
						if err := onDelta(ev.StepUpdate.TextDelta); err != nil {
							cbErr = err
							streamCancel()
							break
						}
					}
				} else if ev.Event == "result" {
					if ev.Result != nil {
						if ev.Result.ConversationID != "" {
							lastConvID = ev.Result.ConversationID
						}
						if ev.Result.Status != "" && ev.Result.Status != "SUCCESS" {
							cbErr = fmt.Errorf("agy returned error status %q: %s", ev.Result.Status, ev.Result.Response)
							streamCancel()
							break
						}
						if ev.Result.Usage.TotalTokens == 0 {
							ev.Result.Usage.TotalTokens = ev.Result.Usage.InputTokens + ev.Result.Usage.OutputTokens
						}
						if onDone != nil {
							if err := onDone(ev.Result); err != nil {
								cbErr = err
								streamCancel()
								break
							}
						}
					}
				}
			}
		}
		if err != nil {
			if err != io.EOF && ctx.Err() == nil && cbErr == nil {
				readErr = fmt.Errorf("error reading agy stream: %w", err)
				streamCancel()
			}
			break
		}
	}

	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if cbErr != nil {
		return cbErr
	}
	if readErr != nil {
		return readErr
	}
	if waitErr != nil {
		return fmt.Errorf("agy process ended with error: %w, stderr: %s", waitErr, stderrBuf.String())
	}
	return nil
}
