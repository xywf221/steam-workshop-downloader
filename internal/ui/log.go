// Package ui provides human-facing stderr logging and progress.
package ui

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"sync"
)

// ANSI sequences. Empty when colour is disabled.
type colors struct {
	Reset, Dim, Bold, Red, Green, Yellow, Cyan, Grey string
}

var (
	ansiOn = colors{
		Reset: "\033[0m", Dim: "\033[2m", Bold: "\033[1m",
		Red: "\033[31m", Green: "\033[32m", Yellow: "\033[33m",
		Cyan: "\033[36m", Grey: "\033[90m",
	}
	ansiOff = colors{}
	ansiRE  = regexp.MustCompile(`\x1b\[[0-9;]*m`)
)

// StripANSI removes all ANSI escape codes from s.
func StripANSI(s string) string {
	return ansiRE.ReplaceAllString(s, "")
}

// Log is the single sink for human output (stderr + optional tee file).
type Log struct {
	c   colors
	out io.Writer
	fp  io.WriteCloser
	mu  sync.Mutex
}

// Options for NewLog.
type Options struct {
	// Color forces colour when true; when nil, auto = stderr is a TTY.
	// When false pointer, colour off.
	Color   *bool
	LogFile string // optional path; ANSI stripped when teeing
	Out     io.Writer
}

// New constructs a Log. Prefer New with Options for tests.
func New(opts Options) (*Log, error) {
	out := opts.Out
	if out == nil {
		out = os.Stderr
	}
	useColor := false
	if opts.Color != nil {
		useColor = *opts.Color
	} else if f, ok := out.(*os.File); ok {
		useColor = isTerminal(f)
	}
	l := &Log{out: out}
	if useColor {
		l.c = ansiOn
	} else {
		l.c = ansiOff
	}
	if opts.LogFile != "" {
		f, err := os.OpenFile(opts.LogFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return nil, err
		}
		l.fp = f
	}
	return l, nil
}

func (l *Log) emit(line string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintln(l.out, line)
	if l.fp != nil {
		fmt.Fprintln(l.fp, StripANSI(line))
	}
}

// Stage prints [STAGE] msg in cyan.
func (l *Log) Stage(stage, msg string) {
	l.emit(fmt.Sprintf("%s[%s]%s %s", l.c.Cyan, stage, l.c.Reset, msg))
}

// Info prints a plain line.
func (l *Log) Info(msg string) { l.emit(msg) }

// OK prints a green OK line.
func (l *Log) OK(msg string) {
	l.emit(fmt.Sprintf("%sOK%s  %s", l.c.Green, l.c.Reset, msg))
}

// Warn prints a yellow !! line.
func (l *Log) Warn(msg string) {
	l.emit(fmt.Sprintf("%s!!%s  %s", l.c.Yellow, l.c.Reset, msg))
}

// Err prints a red FAIL line.
func (l *Log) Err(msg string) {
	l.emit(fmt.Sprintf("%sFAIL%s %s", l.c.Red, l.c.Reset, msg))
}

// Dim prints a dimmed line.
func (l *Log) Dim(msg string) {
	l.emit(fmt.Sprintf("%s%s%s", l.c.Dim, msg, l.c.Reset))
}

// Retry prints a yellow retry line.
func (l *Log) Retry(msg string) {
	l.emit(fmt.Sprintf("%s-> retry:%s %s", l.c.Yellow, l.c.Reset, msg))
}

// Blank prints an empty line.
func (l *Log) Blank() { l.emit("") }

// Close closes the optional log file.
func (l *Log) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.fp != nil {
		err := l.fp.Close()
		l.fp = nil
		return err
	}
	return nil
}
