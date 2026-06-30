// Package natslisten is a test-only recorder that subscribes to the Tasmota
// command and state subjects and records every message for assertions
// (REQUIREMENTS §11.4, REQ-TEST-8..10). It listens on wildcard subjects so a
// test can observe all devices at once, is safe for concurrent publication, and
// provides a wait-for primitive so schedule/timezone tests are deterministic.
package natslisten

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
)

// Record is one observed message: the device it concerns, the decoded state,
// the subject, and the order in which it arrived.
type Record struct {
	Device  string
	State   string
	Subject string
	Seq     int
}

// Recorder captures Tasmota command (and optionally state) messages.
type Recorder struct {
	namespace string

	mu      sync.Mutex
	records []Record
	seq     int
	waiters []waiter

	subs []*nats.Subscription
}

type waiter struct {
	match func(Record) bool
	ch    chan Record
}

type payload struct {
	State string `json:"state"`
}

// NewRecorder subscribes to gorai.<namespace>.tasmota.*.command (REQ-TEST-9). It
// returns the recorder and a close function that unsubscribes.
func NewRecorder(nc *nats.Conn, namespace string) (*Recorder, func() error, error) {
	r := &Recorder{namespace: namespace}
	subject := fmt.Sprintf("gorai.%s.tasmota.*.command", namespace)
	sub, err := nc.Subscribe(subject, r.onMessage)
	if err != nil {
		return nil, nil, fmt.Errorf("subscribe %q: %w", subject, err)
	}
	r.subs = append(r.subs, sub)
	if err := nc.Flush(); err != nil {
		return nil, nil, err
	}
	closeFn := func() error {
		for _, s := range r.subs {
			if err := s.Unsubscribe(); err != nil {
				return err
			}
		}
		return nil
	}
	return r, closeFn, nil
}

// device extracts the device name from gorai.<ns>.tasmota.<device>.command.
func (r *Recorder) device(subject string) string {
	prefix := fmt.Sprintf("gorai.%s.tasmota.", r.namespace)
	rest := subject
	if len(subject) > len(prefix) && subject[:len(prefix)] == prefix {
		rest = subject[len(prefix):]
	}
	for i := 0; i < len(rest); i++ {
		if rest[i] == '.' {
			return rest[:i]
		}
	}
	return rest
}

func (r *Recorder) onMessage(m *nats.Msg) {
	var p payload
	_ = json.Unmarshal(m.Data, &p)

	r.mu.Lock()
	rec := Record{Device: r.device(m.Subject), State: p.State, Subject: m.Subject, Seq: r.seq}
	r.seq++
	r.records = append(r.records, rec)

	remaining := r.waiters[:0]
	for _, w := range r.waiters {
		if w.match(rec) {
			w.ch <- rec
		} else {
			remaining = append(remaining, w)
		}
	}
	r.waiters = remaining
	r.mu.Unlock()
}

// Records returns a copy of all observed records in arrival order.
func (r *Recorder) Records() []Record {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Record, len(r.records))
	copy(out, r.records)
	return out
}

// LastState returns the most recent state observed for a device, or "" if none.
func (r *Recorder) LastState(device string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := len(r.records) - 1; i >= 0; i-- {
		if r.records[i].Device == device {
			return r.records[i].State
		}
	}
	return ""
}

// Count returns how many commands were observed for a device.
func (r *Recorder) Count(device string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, rec := range r.records {
		if rec.Device == device {
			n++
		}
	}
	return n
}

// WaitFor blocks until a command for device with the given state is observed, or
// timeout elapses (REQ-TEST-10). It matches against already-recorded messages
// first, so a command that arrived before the call is not missed.
func (r *Recorder) WaitFor(device, state string, timeout time.Duration) (Record, bool) {
	match := func(rec Record) bool { return rec.Device == device && rec.State == state }

	r.mu.Lock()
	for _, rec := range r.records {
		if match(rec) {
			r.mu.Unlock()
			return rec, true
		}
	}
	ch := make(chan Record, 1)
	r.waiters = append(r.waiters, waiter{match: match, ch: ch})
	r.mu.Unlock()

	select {
	case rec := <-ch:
		return rec, true
	case <-time.After(timeout):
		return Record{}, false
	}
}
