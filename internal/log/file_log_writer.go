package log

import (
	"os"
	"regexp"
	"sync"
)

var ansiRegExp = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

// LogWriter writes to os.Stderr and a file if provided
type LogWriter struct {
	File *os.File
	m    sync.Mutex
}

func (w *LogWriter) Write(b []byte) (int, error) {
	w.m.Lock()
	defer w.m.Unlock()

	i, err := os.Stderr.Write(b)
	if w.File == nil || err != nil {
		return i, err
	}

	if _, err := w.File.Write(ansiRegExp.ReplaceAll(b, nil)); err != nil {
		return i, err
	}

	return len(b), nil
}
