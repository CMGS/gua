package mcpserver

import (
	"bufio"
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func TestReadFrameLineDelimited(t *testing.T) {
	t.Parallel()

	payload, mode, err := readFrame(bufio.NewReader(strings.NewReader("{\"jsonrpc\":\"2.0\",\"method\":\"ping\"}\n")))
	if err != nil {
		t.Fatalf("readFrame returned error: %v", err)
	}
	if mode != frameModeLine {
		t.Fatalf("readFrame mode = %v, want %v", mode, frameModeLine)
	}
	if got := string(payload); got != "{\"jsonrpc\":\"2.0\",\"method\":\"ping\"}" {
		t.Fatalf("readFrame payload = %q", got)
	}
}

func TestReadFrameContentLength(t *testing.T) {
	t.Parallel()

	msg := "{\"jsonrpc\":\"2.0\",\"method\":\"ping\"}"
	input := fmt.Sprintf("Content-Length: %d\r\nContent-Type: application/json\r\n\r\n%s", len(msg), msg)

	payload, mode, err := readFrame(bufio.NewReader(strings.NewReader(input)))
	if err != nil {
		t.Fatalf("readFrame returned error: %v", err)
	}
	if mode != frameModeContentLength {
		t.Fatalf("readFrame mode = %v, want %v", mode, frameModeContentLength)
	}
	if got := string(payload); got != msg {
		t.Fatalf("readFrame payload = %q", got)
	}
}

func TestWriteMessageLineDelimited(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	s := &Server{
		writer:    bufio.NewWriter(&buf),
		frameMode: frameModeLine,
	}

	if err := s.writeMessage(map[string]any{"jsonrpc": "2.0", "method": "ping"}); err != nil {
		t.Fatalf("writeMessage returned error: %v", err)
	}
	if !strings.HasSuffix(buf.String(), "\n") {
		t.Fatalf("writeMessage output = %q, want trailing newline", buf.String())
	}
	if strings.Contains(buf.String(), headerPrefix) {
		t.Fatalf("writeMessage output unexpectedly used content-length framing: %q", buf.String())
	}
}
