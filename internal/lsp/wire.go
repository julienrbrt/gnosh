package lsp

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// The most a server's message may be: headers of up to 64 KiB and a body
// of up to 32 MiB, as Claude Code allows.
const (
	maxHeader = 64 << 10
	maxBody   = 32 << 20
)

// errNotProtocol is a server's output that is not a message: a server
// that logs to its standard output, say.
var errNotProtocol = errors.New("its output is not Language Server Protocol messages")

// readMessage reads one message: header lines, an empty line, and a body
// as long as Content-Length says. r must buffer at least maxHeader bytes.
func readMessage(r *bufio.Reader) ([]byte, error) {
	length, seen := -1, 0
	for {
		line, err := r.ReadSlice('\n')
		seen += len(line)
		switch {
		case errors.Is(err, bufio.ErrBufferFull) || seen > maxHeader:
			return nil, fmt.Errorf("%w: a header longer than %d bytes", errNotProtocol, maxHeader)
		case err == io.EOF && seen == len(line) && len(line) == 0 && length < 0:
			return nil, io.EOF
		case err != nil:
			return nil, err
		}
		text := strings.TrimRight(string(line), "\r\n")
		if text == "" {
			if length < 0 {
				return nil, fmt.Errorf("%w: a message with no Content-Length", errNotProtocol)
			}
			break
		}
		name, value, ok := strings.Cut(text, ":")
		if !ok {
			return nil, fmt.Errorf("%w: %q", errNotProtocol, clip(text, 80))
		}
		if strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			n, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil || n < 0 {
				return nil, fmt.Errorf("%w: Content-Length %q", errNotProtocol, clip(value, 40))
			}
			length = n
		}
	}
	if length > maxBody {
		return nil, fmt.Errorf("%w: a message of %d bytes, more than the %d taken", errNotProtocol, length, maxBody)
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	return body, nil
}

// frame is a message's body with its header in front.
func frame(body []byte) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "Content-Length: %d\r\n\r\n", len(body))
	b.Write(body)
	return b.Bytes()
}

// clip cuts s to at most n bytes, for a message.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
