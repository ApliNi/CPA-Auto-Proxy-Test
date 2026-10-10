// Events is the plugin's in-memory diagnostic event ring buffer. It records
// lifecycle events such as proxy switches, cleanup activity,
// and usage-record anomalies so production behavior can be diagnosed through
// the /data and /export endpoints without host log access.
package plugin

import (
	"sync"
	"time"
)

// Event is one diagnostic entry in the ring buffer.
type Event struct {
	// TS is the event timestamp (unix seconds).
	TS int64 `json:"ts"`
	// Level is one of "info", "warn", or "error".
	Level string `json:"level"`
	// Kind groups events by origin: "switch", "cleanup", "usage".
	Kind string `json:"kind"`
	// AuthID carries the credential identifier when known.
	AuthID string `json:"auth_id,omitempty"`
	// Message is the human-readable diagnostic text.
	Message string `json:"message"`
}

// eventBufferCapacity bounds the ring buffer. The oldest entries are dropped
// once the capacity is reached.
const eventBufferCapacity = 200

// eventBuffer stores the most recent events in insertion order.
type eventBuffer struct {
	mu     sync.Mutex
	events []Event
}

func newEventBuffer() *eventBuffer {
	return &eventBuffer{events: make([]Event, 0, eventBufferCapacity)}
}

// Add appends one event, dropping the oldest entry when the ring buffer is
// full. Safe for concurrent use.
func (b *eventBuffer) Add(ev Event) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.events) >= eventBufferCapacity {
		// Drop from the front; the copy keeps the backing array bounded.
		copy(b.events, b.events[1:])
		b.events[len(b.events)-1] = ev
		return
	}
	b.events = append(b.events, ev)
}

// Snapshot returns a copy of the buffered events, newest first, so exports
// surface the most recent diagnostics at the top.
func (b *eventBuffer) Snapshot() []Event {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]Event, len(b.events))
	for i, ev := range b.events {
		out[len(b.events)-1-i] = ev
	}
	return out
}

// AddEvent appends a diagnostic event to the ring buffer. It is exported so
// main.go can record usage-record decode errors that happen before the
// engine's HandleUsage runs.
func (e *Engine) AddEvent(level string, kind string, authID string, message string) {
	e.events.Add(Event{
		TS:      time.Now().Unix(),
		Level:   level,
		Kind:    kind,
		AuthID:  authID,
		Message: message,
	})
}
