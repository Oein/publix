// Package buildlog captures a deployment's output so it can be watched live
// in the dashboard and read back afterwards.
//
// A build log is append-only and has two readers with different needs: a
// browser that connects mid-build and wants everything so far plus whatever
// comes next, and someone opening a finished deployment days later. Both are
// served from the same file, with live writes fanned out to subscribers.
package buildlog

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Stream names the origin of a line, so the dashboard can style them.
type Stream string

// Log line streams.
const (
	StreamStdout Stream = "stdout"
	StreamStderr Stream = "stderr"
	StreamSystem Stream = "system" // publix's own progress messages
)

// Line is one entry in a build log.
type Line struct {
	Seq    int       `json:"seq"`
	At     time.Time `json:"at"`
	Stream Stream    `json:"stream"`
	Text   string    `json:"text"`
}

// Log is one deployment's output.
type Log struct {
	mu       sync.Mutex
	path     string
	file     *os.File
	buf      *bufio.Writer
	lines    []Line
	seq      int
	closed   bool
	subs     map[int]chan Line
	nextSub  int
	maxLines int
}

// Store owns the log files for every deployment.
type Store struct {
	mu   sync.Mutex
	dir  string
	open map[string]*Log
}

// NewStore creates a log store rooted at dir.
func NewStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Store{dir: dir, open: map[string]*Log{}}, nil
}

func (s *Store) path(deploymentID string) string {
	return filepath.Join(s.dir, deploymentID+".ndjson")
}

// Create starts a new log for a deployment, replacing any previous one.
func (s *Store) Create(deploymentID string) (*Log, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if l, ok := s.open[deploymentID]; ok {
		return l, nil
	}
	f, err := os.OpenFile(s.path(deploymentID), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	l := &Log{
		path:     s.path(deploymentID),
		file:     f,
		buf:      bufio.NewWriter(f),
		subs:     map[int]chan Line{},
		maxLines: 5000,
	}
	s.open[deploymentID] = l
	return l, nil
}

// Get returns the live log for a deployment if one is still open.
func (s *Store) Get(deploymentID string) (*Log, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, ok := s.open[deploymentID]
	return l, ok
}

// Close finishes a deployment's log and releases its file handle.
func (s *Store) Close(deploymentID string) {
	s.mu.Lock()
	l, ok := s.open[deploymentID]
	delete(s.open, deploymentID)
	s.mu.Unlock()
	if ok {
		l.Close()
	}
}

// Read returns a deployment's log from disk. It works for finished
// deployments as well as running ones.
func (s *Store) Read(deploymentID string) ([]Line, error) {
	if l, ok := s.Get(deploymentID); ok {
		l.Flush()
	}
	f, err := os.Open(s.path(deploymentID))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var out []Line
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for sc.Scan() {
		var ln Line
		if json.Unmarshal(sc.Bytes(), &ln) == nil {
			out = append(out, ln)
		}
	}
	return out, sc.Err()
}

// Remove deletes a deployment's log file, called when its record is pruned.
func (s *Store) Remove(deploymentID string) error {
	s.Close(deploymentID)
	err := os.Remove(s.path(deploymentID))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// Write appends one line to the log and fans it out to live subscribers.
func (l *Log) Write(stream Stream, text string) {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return
	}
	l.seq++
	ln := Line{Seq: l.seq, At: time.Now().UTC(), Stream: stream, Text: text}

	if raw, err := json.Marshal(ln); err == nil {
		l.buf.Write(raw)
		l.buf.WriteByte('\n')
	}
	l.lines = append(l.lines, ln)
	if len(l.lines) > l.maxLines {
		l.lines = l.lines[len(l.lines)-l.maxLines:]
	}

	subs := make([]chan Line, 0, len(l.subs))
	for _, ch := range l.subs {
		subs = append(subs, ch)
	}
	l.mu.Unlock()

	for _, ch := range subs {
		select {
		case ch <- ln:
		default:
			// A subscriber too slow to keep up is dropped rather than
			// allowed to stall the build. It can reload for the full log.
		}
	}
}

// Printf appends a formatted system line.
func (l *Log) Printf(format string, args ...any) {
	l.Write(StreamSystem, strings.TrimRight(fmt.Sprintf(format, args...), "\n"))
}

// Writer returns an io.Writer that splits its input into log lines. Use it
// to relay the output of a build or a subprocess.
func (l *Log) Writer(stream Stream) *Writer { return &Writer{log: l, stream: stream} }

// Snapshot returns the lines retained in memory.
func (l *Log) Snapshot() []Line {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Line, len(l.lines))
	copy(out, l.lines)
	return out
}

// Tail returns the last n lines, which is what an error message shows.
func (l *Log) Tail(n int) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	start := len(l.lines) - n
	if start < 0 {
		start = 0
	}
	out := make([]string, 0, len(l.lines)-start)
	for _, ln := range l.lines[start:] {
		if strings.TrimSpace(ln.Text) != "" {
			out = append(out, ln.Text)
		}
	}
	return out
}

// Subscribe returns a channel of lines written from now on, plus a function
// to unsubscribe. Callers normally send Snapshot first, then stream.
func (l *Log) Subscribe() (<-chan Line, func()) {
	l.mu.Lock()
	defer l.mu.Unlock()
	id := l.nextSub
	l.nextSub++
	ch := make(chan Line, 256)
	l.subs[id] = ch
	return ch, func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		if c, ok := l.subs[id]; ok {
			delete(l.subs, id)
			close(c)
		}
	}
}

// Flush pushes buffered lines to disk so a reader sees them.
func (l *Log) Flush() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.closed {
		l.buf.Flush()
	}
}

// Close finishes the log and disconnects every subscriber.
func (l *Log) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return
	}
	l.closed = true
	l.buf.Flush()
	l.file.Close()
	for id, ch := range l.subs {
		delete(l.subs, id)
		close(ch)
	}
}

// Closed reports whether the log has finished.
func (l *Log) Closed() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.closed
}

// Writer adapts a Log to io.Writer, emitting one entry per line.
type Writer struct {
	log    *Log
	stream Stream
	buf    strings.Builder
	mu     sync.Mutex
}

// Write splits b into lines and appends each to the log.
func (w *Writer) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf.Write(b)
	s := w.buf.String()
	for {
		i := strings.IndexByte(s, '\n')
		if i < 0 {
			break
		}
		w.log.Write(w.stream, strings.TrimRight(s[:i], "\r"))
		s = s[i+1:]
	}
	w.buf.Reset()
	// A very long partial line (a progress bar with no newline) would grow
	// without bound; flush it rather than buffer forever.
	if len(s) > 8192 {
		w.log.Write(w.stream, s)
		s = ""
	}
	w.buf.WriteString(s)
	return len(b), nil
}

// Flush emits any trailing partial line.
func (w *Writer) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if s := strings.TrimRight(w.buf.String(), "\r\n"); s != "" {
		w.log.Write(w.stream, s)
	}
	w.buf.Reset()
}
