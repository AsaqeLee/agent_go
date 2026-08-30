package mcp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

func writeMsg(w io.Writer, v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "Content-Length: %d\r\n\r\n%s", len(body), body)
	return err
}

func readMsg(r *bufio.Reader) ([]byte, error) {
	// Skip leading whitespace / newlines.
	for {
		b, err := r.ReadByte()
		if err != nil {
			return nil, err
		}
		if b == ' ' || b == '\r' || b == '\n' || b == '\t' {
			continue
		}
		if err := r.UnreadByte(); err != nil {
			return nil, err
		}
		break
	}

	peek, err := r.Peek(15)
	if err != nil && err != io.EOF && err != bufio.ErrBufferFull {
		return nil, err
	}
	if bytes.HasPrefix(bytes.ToLower(peek), []byte("content-length:")) {
		return readLSP(r)
	}
	line, err := r.ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return nil, err
	}
	return bytes.TrimSpace(line), nil
}

func readLSP(r *bufio.Reader) ([]byte, error) {
	var contentLen int
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "content-length:") {
			n, err := strconv.Atoi(strings.TrimSpace(line[len("Content-Length:"):]))
			if err != nil {
				n, err = strconv.Atoi(strings.TrimSpace(lower[len("content-length:"):]))
			}
			if err != nil {
				return nil, fmt.Errorf("mcp: content-length: %w", err)
			}
			contentLen = n
		}
	}
	if contentLen <= 0 {
		return nil, fmt.Errorf("mcp: missing content-length")
	}
	if contentLen > 16*1024*1024 {
		return nil, fmt.Errorf("mcp: message too large")
	}
	body := make([]byte, contentLen)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	return body, nil
}
