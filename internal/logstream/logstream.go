// Package logstream sends a running job's output to the Hub as it is produced,
// so the dashboard can show it live. Streaming is best effort: if the Hub
// can't be reached, output is kept and retried, and the job's complete logs
// still go up when it finishes.
package logstream

import (
	"sync"
	"time"
)

const (
	flushEvery = 500 * time.Millisecond
	maxPending = 256 << 10 // unsent output kept while the Hub is unreachable
	maxChunk   = 64 << 10
)

// Sender delivers output that starts at byte `offset` of the job's output.
type Sender func(jobID string, offset int, chunk string) error

type Streamer struct {
	jobID string
	send  Sender

	mu      sync.Mutex
	pending []byte
	offset  int // bytes of output already delivered
	closed  bool

	wake chan struct{}
	done chan struct{}
}

// New starts streaming for a job. Call Close when the job's output ends.
func New(jobID string, send Sender) *Streamer {
	s := &Streamer{jobID: jobID, send: send, wake: make(chan struct{}, 1), done: make(chan struct{})}
	go s.loop()
	return s
}

// Write queues output. It never blocks on the network and never fails, so it is
// safe to put behind an io.MultiWriter next to the real log buffer.
func (s *Streamer) Write(p []byte) (int, error) {
	s.mu.Lock()
	if !s.closed {
		s.pending = append(s.pending, p...)
		if over := len(s.pending) - maxPending; over > 0 {
			// The Hub has been unreachable for a long time: drop the oldest
			s.pending = append([]byte(nil), s.pending[over:]...)
			s.offset += over
		}
		if len(s.pending) >= maxChunk {
			select {
			case s.wake <- struct{}{}:
			default:
			}
		}
	}
	s.mu.Unlock()
	return len(p), nil
}

// Close sends what is left and stops. Safe to call more than once.
func (s *Streamer) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.mu.Unlock()
	close(s.wake)
	<-s.done
}

func (s *Streamer) loop() {
	defer close(s.done)
	t := time.NewTicker(flushEvery)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			s.flush()
		case _, ok := <-s.wake:
			s.flush()
			if !ok {
				return
			}
		}
	}
}

func (s *Streamer) flush() {
	for {
		s.mu.Lock()
		if len(s.pending) == 0 {
			s.mu.Unlock()
			return
		}
		n := len(s.pending)
		if n > maxChunk {
			n = maxChunk
		}
		chunk := string(s.pending[:n])
		offset := s.offset
		s.mu.Unlock()

		if err := s.send(s.jobID, offset, chunk); err != nil {
			return // try again on the next tick
		}

		// Delivered: drop it. (If the Hub restarted it only lost the live view;
		// the job's complete logs are sent when it finishes.)
		s.mu.Lock()
		s.pending = s.pending[n:]
		s.offset += n
		s.mu.Unlock()
	}
}
