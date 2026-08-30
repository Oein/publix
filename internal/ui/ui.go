// Package ui renders publix's terminal output. Deploy tooling is read far
// more often than it is configured, so the output is treated as a product
// surface: quiet on success, specific on failure, and never a wall of noise.
package ui

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

// Logger writes progress for one operation.
type Logger struct {
	mu      sync.Mutex
	w       io.Writer
	color   bool
	verbose bool
	start   time.Time
	prefix  string
}

// New returns a Logger writing to w. Colour is enabled only for a terminal,
// so piped output and CI logs stay clean.
func New(w io.Writer, verbose bool) *Logger {
	return &Logger{w: w, color: isTerminal(w) && os.Getenv("NO_COLOR") == "", verbose: verbose, start: time.Now()}
}

func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	st, err := f.Stat()
	if err != nil {
		return false
	}
	return st.Mode()&os.ModeCharDevice != 0
}

// ANSI codes, blanked when colour is off.
func (l *Logger) c(code, s string) string {
	if !l.color {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

// Dim renders secondary text.
func (l *Logger) Dim(s string) string { return l.c("2", s) }

// Bold renders emphasised text.
func (l *Logger) Bold(s string) string { return l.c("1", s) }

// Green renders success text.
func (l *Logger) Green(s string) string { return l.c("32", s) }

// Red renders failure text.
func (l *Logger) Red(s string) string { return l.c("31", s) }

// Yellow renders warning text.
func (l *Logger) Yellow(s string) string { return l.c("33", s) }

// Cyan renders links and identifiers.
func (l *Logger) Cyan(s string) string { return l.c("36", s) }

func (l *Logger) write(s string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprint(l.w, l.prefix+s)
}

// Step announces a phase of the operation.
func (l *Logger) Step(format string, args ...any) {
	l.write(l.Cyan("→ ") + fmt.Sprintf(format, args...) + "\n")
}

// Done reports a completed phase with its elapsed time.
func (l *Logger) Done(format string, args ...any) {
	l.write(l.Green("✓ ") + fmt.Sprintf(format, args...) + "\n")
}

// Warn reports something the user should know but which is not fatal.
func (l *Logger) Warn(format string, args ...any) {
	l.write(l.Yellow("! ") + fmt.Sprintf(format, args...) + "\n")
}

// Fail reports a failure.
func (l *Logger) Fail(format string, args ...any) {
	l.write(l.Red("✗ ") + fmt.Sprintf(format, args...) + "\n")
}

// Info writes an unadorned line.
func (l *Logger) Info(format string, args ...any) {
	l.write(fmt.Sprintf(format, args...) + "\n")
}

// Detail writes secondary information, indented.
func (l *Logger) Detail(format string, args ...any) {
	l.write("  " + l.Dim(fmt.Sprintf(format, args...)) + "\n")
}

// Debug writes only when verbose output was requested.
func (l *Logger) Debug(format string, args ...any) {
	if l.verbose {
		l.write("  " + l.Dim(fmt.Sprintf(format, args...)) + "\n")
	}
}

// Elapsed reports how long the operation has been running.
func (l *Logger) Elapsed() time.Duration { return time.Since(l.start) }

// Indented returns a writer that prefixes each line, used for relaying
// build and container output without losing the surrounding structure.
// In non-verbose mode build chatter is dropped entirely.
func (l *Logger) Indented() io.Writer {
	if !l.verbose {
		return &tailWriter{l: l, keep: 20}
	}
	return &prefixWriter{l: l, prefix: "  "}
}

// Verbose reports whether full output was requested.
func (l *Logger) Verbose() bool { return l.verbose }

// prefixWriter indents every line written through it.
type prefixWriter struct {
	l      *Logger
	prefix string
	buf    strings.Builder
}

func (p *prefixWriter) Write(b []byte) (int, error) {
	p.buf.Write(b)
	s := p.buf.String()
	for {
		i := strings.IndexByte(s, '\n')
		if i < 0 {
			break
		}
		line := strings.TrimRight(s[:i], "\r")
		p.l.write(p.prefix + p.l.Dim(line) + "\n")
		s = s[i+1:]
	}
	p.buf.Reset()
	p.buf.WriteString(s)
	return len(b), nil
}

// tailWriter keeps only the last N lines in memory. On success they are
// discarded; on failure the caller prints them, which is exactly the
// information a user needs and nothing more.
type tailWriter struct {
	l     *Logger
	keep  int
	buf   strings.Builder
	lines []string
}

func (t *tailWriter) Write(b []byte) (int, error) {
	t.buf.Write(b)
	s := t.buf.String()
	for {
		i := strings.IndexByte(s, '\n')
		if i < 0 {
			break
		}
		line := strings.TrimRight(s[:i], "\r")
		if strings.TrimSpace(line) != "" {
			t.lines = append(t.lines, line)
			if len(t.lines) > t.keep {
				t.lines = t.lines[len(t.lines)-t.keep:]
			}
		}
		s = s[i+1:]
	}
	t.buf.Reset()
	t.buf.WriteString(s)
	return len(b), nil
}

// Tail returns the retained lines from an Indented writer, if it was
// buffering. Used to show build output only when the build failed.
func Tail(w io.Writer) []string {
	if t, ok := w.(*tailWriter); ok {
		return t.lines
	}
	return nil
}

// Table renders aligned columns, the format used by every `publix` listing.
func Table(w io.Writer, headers []string, rows [][]string) {
	if len(rows) == 0 && len(headers) == 0 {
		return
	}
	width := make([]int, len(headers))
	for i, h := range headers {
		width[i] = len([]rune(h))
	}
	for _, r := range rows {
		for i, cell := range r {
			if i < len(width) {
				if n := len([]rune(stripANSI(cell))); n > width[i] {
					width[i] = n
				}
			}
		}
	}
	var b strings.Builder
	for i, h := range headers {
		b.WriteString(pad(strings.ToUpper(h), width[i]))
		if i < len(headers)-1 {
			b.WriteString("   ")
		}
	}
	fmt.Fprintln(w, strings.TrimRight(b.String(), " "))
	for _, r := range rows {
		b.Reset()
		for i := range headers {
			cell := ""
			if i < len(r) {
				cell = r[i]
			}
			b.WriteString(padANSI(cell, width[i]))
			if i < len(headers)-1 {
				b.WriteString("   ")
			}
		}
		fmt.Fprintln(w, strings.TrimRight(b.String(), " "))
	}
}

func pad(s string, n int) string {
	if d := n - len([]rune(s)); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}

func padANSI(s string, n int) string {
	if d := n - len([]rune(stripANSI(s))); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}

// stripANSI removes escape sequences so coloured cells still align.
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == '\x1b' {
			j := i
			for j < len(s) && s[j] != 'm' {
				j++
			}
			i = j + 1
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// Age renders a timestamp the way a human reads it: relative and coarse.
func Age(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	default:
		return t.Format("2006-01-02")
	}
}

// Bytes renders a byte count in the largest unit that keeps it readable.
func Bytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit && exp < 3; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%cB", float64(n)/float64(div), "KMGT"[exp])
}
