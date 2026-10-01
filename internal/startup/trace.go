// Package startup provides opt-in timing markers for startup measurements.
// It is silent unless NEXO_STARTUP_TRACE is set, keeping normal invocations
// free of diagnostics while leaving repeatable measurements available.
package startup

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

// Trace records elapsed markers for one invocation. Markers are written as
// tab-separated lines so shell tools can consume them without parsing prose.
type Trace struct {
	start time.Time
	name  string
	out   io.Writer
	mu    sync.Mutex
	once  map[string]bool
}

// New returns an enabled trace when NEXO_STARTUP_TRACE is non-empty. Output is
// sent to out, or stderr when out is nil. The trace is nil when disabled.
func New(name string, out io.Writer) *Trace {
	if strings.TrimSpace(os.Getenv("NEXO_STARTUP_TRACE")) == "" {
		return nil
	}
	if out == nil {
		out = os.Stderr
	}
	return &Trace{start: time.Now(), name: name, out: out, once: make(map[string]bool)}
}

// Mark writes one elapsed marker. Nil traces make instrumentation cheap at
// call sites and are intentionally safe to call unconditionally.
func (t *Trace) Mark(event string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	_, _ = fmt.Fprintf(t.out, "nexo-startup\t%s\t%s\t%dµs\n", t.name, event, time.Since(t.start).Microseconds())
}

// MarkOnce writes an elapsed marker only the first time event is observed.
func (t *Trace) MarkOnce(event string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	if t.once[event] {
		t.mu.Unlock()
		return
	}
	t.once[event] = true
	t.mu.Unlock()
	t.Mark(event)
}

// Since returns elapsed time from the trace origin, primarily for tests.
func (t *Trace) Since() time.Duration {
	if t == nil {
		return 0
	}
	return time.Since(t.start)
}
