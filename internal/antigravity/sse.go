package antigravity

import (
	"bufio"
	"bytes"
	"io"
)

// SSE is a minimal Server-Sent Events reader. It tolerates comment lines,
// multi-line data fields, CRLF endings, and a stream that ends without a
// trailing blank line.
type SSE struct {
	sc *bufio.Scanner
}

// NewSSE wraps r. The scanner buffer is large because a single upstream chunk
// can carry a long signature or tool argument blob.
func NewSSE(r io.Reader) *SSE {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 8*1024*1024)
	return &SSE{sc: sc}
}

// Next returns the payload of the next event, or io.EOF when the stream ends.
func (s *SSE) Next() ([]byte, error) {
	var data bytes.Buffer
	for {
		if !s.sc.Scan() {
			if err := s.sc.Err(); err != nil {
				return nil, err
			}
			if data.Len() > 0 {
				return copyBytes(data.Bytes()), nil
			}
			return nil, io.EOF
		}

		line := bytes.TrimSuffix(s.sc.Bytes(), []byte("\r"))
		if len(line) == 0 {
			if data.Len() > 0 {
				return copyBytes(data.Bytes()), nil
			}
			continue
		}
		if line[0] == ':' {
			continue // keep-alive comment
		}

		field, value := splitField(line)
		if field != "data" {
			continue
		}
		if data.Len() > 0 {
			data.WriteByte('\n')
		}
		data.Write(value)
	}
}

func splitField(line []byte) (field string, value []byte) {
	i := bytes.IndexByte(line, ':')
	if i < 0 {
		return string(line), nil
	}
	field = string(line[:i])
	value = bytes.TrimPrefix(line[i+1:], []byte(" "))
	return field, value
}

func copyBytes(b []byte) []byte {
	out := make([]byte, len(b))
	copy(out, b)
	return out
}
