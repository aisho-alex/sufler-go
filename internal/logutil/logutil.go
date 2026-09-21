package logutil

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type FileLog struct {
	mu   sync.Mutex
	f    *os.File
	echo bool
}

func NewFileLog(path string, echo bool) (*FileLog, error) {
	if d := filepath.Dir(path); d != "" && d != "." {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	return &FileLog{f: f, echo: echo}, nil
}

func (l *FileLog) Log(msg string)             { l.write(msg, true, l.echo) }
func (l *FileLog) LogSilent(msg string)       { l.write(msg, true, false) }
func (l *FileLog) Logf(f string, a ...any)    { l.Log(fmt.Sprintf(f, a...)) }
func (l *FileLog) LogSilentf(f string, a ...any) {
	l.LogSilent(fmt.Sprintf(f, a...))
}

func (l *FileLog) write(msg string, ts, echo bool) {
	line := msg
	if ts {
		line = time.Now().Format("2006-01-02 15:04:05") + " " + msg
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if echo {
		fmt.Println(line)
	}
	fmt.Fprintln(l.f, line)
}

func (l *FileLog) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	_ = l.f.Close()
}
