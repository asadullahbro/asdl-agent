package main

import (
	"log"
	"sync"
	"time"
)

// maxParallelJobs is the most jobs this agent runs at once, whatever the Hub hands it. The Hub sets the real
// limit per node (from its hardware, changeable on the node's page); this is only a ceiling.
const maxParallelJobs = 8

// jobSlots counts the jobs running on this agent. A restart (after an update) drains it first: no new jobs are
// claimed and the running ones finish, so none is cut off and loses its result.
type jobSlots struct {
	mu       sync.Mutex
	active   int
	draining bool
}

var slots = &jobSlots{}

// drainPoll is how often a drain checks the running jobs again.
var drainPoll = 5 * time.Second

// tryStart takes a slot for a job about to be claimed; false when the agent is full or draining. Each true must
// be followed by done.
func (s *jobSlots) tryStart() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.draining || s.active >= maxParallelJobs {
		return false
	}
	s.active++
	return true
}

func (s *jobSlots) done() {
	s.mu.Lock()
	s.active--
	s.mu.Unlock()
}

// drain stops new jobs from being claimed and waits until at most keep jobs are running (keep counts the caller
// when it is itself a job), or until limit has passed.
func (s *jobSlots) drain(keep int, limit time.Duration) {
	deadline := time.Now().Add(limit)
	s.mu.Lock()
	s.draining = true
	if n := s.active - keep; n > 0 {
		log.Printf("⏳ Waiting for %d running job(s) to finish before restarting", n)
	}
	for s.active > keep && time.Now().Before(deadline) {
		s.mu.Unlock()
		time.Sleep(drainPoll)
		s.mu.Lock()
	}
	s.mu.Unlock()
}

// resume lets jobs be claimed again after a drain that didn't end in a restart.
func (s *jobSlots) resume() {
	s.mu.Lock()
	s.draining = false
	s.mu.Unlock()
}
