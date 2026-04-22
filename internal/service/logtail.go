package service

import (
	"bufio"
	"context"
	"io"
	"os"
	"strings"
	"time"
)

// tailLogFile polls a log file for new content and sends each line as a LogLine message.
// It detects file truncation (e.g. on service restart) and resets to the beginning.
func (m *Manager) tailLogFile(ctx context.Context, svcName, filePath string) {
	// Wait briefly for the file to be created
	var f *os.File
	for i := 0; i < 10; i++ {
		var err error
		f, err = os.Open(filePath)
		if err == nil {
			break
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(200 * time.Millisecond):
		}
	}
	if f == nil {
		return
	}
	defer f.Close()

	// Seek to end — we only want new output (bulk load is done separately)
	offset, _ := f.Seek(0, io.SeekEnd)

	reader := bufio.NewReader(f)
	var partial string

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Check for truncation
		info, err := f.Stat()
		if err != nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		if info.Size() < offset {
			// File was truncated — reset
			f.Seek(0, io.SeekStart)
			offset = 0
			reader.Reset(f)
			partial = ""
		}

		line, err := reader.ReadString('\n')
		if err != nil {
			// No new data — update offset and wait
			if line != "" {
				partial += line
			}
			offset, _ = f.Seek(0, io.SeekCurrent)
			time.Sleep(200 * time.Millisecond)
			continue
		}

		full := partial + line
		partial = ""
		full = strings.TrimRight(full, "\n\r")
		if full != "" {
			m.sendLog(svcName, full)
		}
		offset, _ = f.Seek(0, io.SeekCurrent)
	}
}

// bulkLoadLogFile reads the last maxLines lines from a log file directly into
// the service's RingBuffer, without sending messages. This avoids flooding the
// UI with historical lines on re-adoption.
func (m *Manager) bulkLoadLogFile(svc *Service, filePath string) {
	f, err := os.Open(filePath)
	if err != nil {
		return
	}
	defer f.Close()

	// Read up to last 1MB
	info, err := f.Stat()
	if err != nil {
		return
	}
	readFrom := info.Size() - 1024*1024
	if readFrom < 0 {
		readFrom = 0
	}
	f.Seek(readFrom, io.SeekStart)

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	const maxLines = 200
	lines := make([]string, 0, maxLines)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
		if len(lines) > maxLines {
			lines = lines[1:]
		}
	}

	for _, line := range lines {
		cleaned := sanitizeLog(line)
		if strings.TrimSpace(cleaned) != "" {
			svc.LogBuffer.Write(cleaned)
		}
	}
}
