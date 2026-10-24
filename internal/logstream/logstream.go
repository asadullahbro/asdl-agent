// Package logstream sends a running job's output to the Hub as it is produced,
// so the dashboard can show it live. Streaming is best effort: if the Hub
// can't be reached, output is kept and retried, and the job's complete logs
// still go up when it finishes.
package logstream

import (
	"errors"
	"sync"
	"time"
)

// ErrEnded is what a Sender returns when the Hub says the job is no longer
// running (it ended, failed or was cancelled there): nobody is waiting for the
// output, and the agent should stop the job instead of running it on.
var ErrEnded = errors.New("the Hub says this job is no longer running")

const (
	flushEvery = 500 * time.Millisecond
	maxPending = 256 << 10 // unsent output kept while the Hub is unreachable
	maxChunk   = 64 << 10
)

// keepAliveEvery is how often a job with no output still asks the Hub, with an
// empty chunk, whether it is wanted. Without it a silent job (a follow with
// nothing to say, a hung pull) would never find out it was cancelled.
var keepAliveEvery = 15 * time.Second

// Sender delivers output that starts at byte `offset` of the job's output.
type Sender func(jobID string, offset int, chunk string) error

type Streamer struct {
	jobID   string
	send    Sender
	onEnded func()

	mu      sync.Mutex
	pending []byte
	offset  int // bytes of output already delivered
	closed  bool
	ended   bool

	wake chan struct{}
	done chan struct{}
}

// New starts streaming for a job. Call Close when the job's output ends.
// onEnded, if not nil, is called once when the Hub says the job is over.
func New(jobID string, send Sender, onEnded func()) *Streamer {
	s := &Streamer{jobID: jobID, send: send, onEnded: onEnded, wake: make(chan struct{}, 1), done: make(chan struct{})}
	go s.loop()
	return s
}

// Write queues output. It never blocks on the network and never fails, so it is
// safe to put behind an io.MultiWriter next to the real log buffer.
func (s *Streamer) Write(p []byte) (int, error) {
	s.mu.Lock()
	if !s.closed && !s.ended {
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
	k := time.NewTicker(keepAliveEvery)
	defer k.Stop()
	for {
		select {
		case <-t.C:
			s.flush()
		case <-k.C:
			s.keepAlive()
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
			if errors.Is(err, ErrEnded) {
				s.end()
			}
			return // otherwise try again on the next tick
		}

		// Delivered: drop it. (If the Hub restarted it only lost the live view;
		// the job's complete logs are sent when it finishes.)
		s.mu.Lock()
		s.pending = s.pending[n:]
		s.offset += n
		s.mu.Unlock()
	}
}

// keepAlive asks the Hub whether the job is still wanted when there is
// nothing to send.
func (s *Streamer) keepAlive() {
	s.mu.Lock()
	if s.ended || s.closed || len(s.pending) > 0 {
		s.mu.Unlock()
		return
	}
	offset := s.offset
	s.mu.Unlock()
	if err := s.send(s.jobID, offset, ""); errors.Is(err, ErrEnded) {
		s.end()
	}
}

// end records that the Hub no longer wants this job, and tells the owner once.
func (s *Streamer) end() {
	s.mu.Lock()
	if s.ended {
		s.mu.Unlock()
		return
	}
	s.ended = true
	s.pending = nil
	cb := s.onEnded
	s.mu.Unlock()
	if cb != nil {
		cb()
	}
}
