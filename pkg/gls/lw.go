package gls

import (
	"bufio"
	"bytes"
	"sync"
)

// LineWriter is an io.Writer that executes a function when a full line is written.
type LineWriter struct {
	mu       sync.Mutex
	buf      bytes.Buffer
	callback func(string)
}

func NewLineWriter(callback func(string)) *LineWriter {
	return &LineWriter{callback: callback}
}

// Write implements the io.Writer interface.
func (lw *LineWriter) Write(p []byte) (n int, err error) {
	lw.mu.Lock()
	defer lw.mu.Unlock()

	n, err = lw.buf.Write(p)
	if err != nil {
		return n, err
	}

	scanner := bufio.NewScanner(&lw.buf)
	for scanner.Scan() {
		line := scanner.Text()
		lw.callback(line)
	}

	lw.buf.Reset()
	remaining := scanner.Bytes()
	if len(remaining) > 0 {
		lw.buf.Write(remaining)
	}

	return n, scanner.Err()
}
