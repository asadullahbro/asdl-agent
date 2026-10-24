package logstream

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

type sink struct {
	mu     sync.Mutex
	got    strings.Builder
	offs   []int
	failed int
}

func (k *sink) send(_ string, offset int, chunk string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.failed > 0 {
		k.failed--
		return errors.New("hub down")
	}
	k.offs = append(k.offs, offset)
	k.got.WriteString(chunk)
	return nil
}

func TestCloseSendsEverythingInOrder(t *testing.T) {
	k := &sink{}
	s := New("j", k.send, nil)
	s.Write([]byte("one "))
	s.Write([]byte("two"))
	s.Close()
	s.Close() // twice is fine
	if k.got.String() != "one two" {
		t.Fatalf("got %q", k.got.String())
	}
	if k.offs[0] != 0 {
		t.Fatalf("offsets %v", k.offs)
	}
}

func TestOutputIsKeptAndResentWhenTheHubIsDown(t *testing.T) {
	k := &sink{failed: 2}
	s := New("j", k.send, nil)
	s.Write([]byte("abc"))
	s.flush() // fails
	s.flush() // fails
	s.Close() // succeeds
	if k.got.String() != "abc" {
		t.Fatalf("got %q", k.got.String())
	}
}

func TestWriteAfterCloseIsHarmless(t *testing.T) {
	s := New("j", (&sink{}).send, nil)
	s.Close()
	if n, err := s.Write([]byte("x")); n != 1 || err != nil {
		t.Fatal(n, err)
	}
}

// The Hub saying the job is over (409) must reach the owner of the job, once,
// and later output is dropped.
func TestHubEndingTheJobIsReported(t *testing.T) {
	calls := 0
	s := New("j", func(string, int, string) error { return ErrEnded }, func() { calls++ })
	s.Write([]byte("abc"))
	s.flush()
	s.flush()
	if calls != 1 {
		t.Fatalf("onEnded called %d times, want 1", calls)
	}
	s.Write([]byte("more"))
	if len(s.pending) != 0 {
		t.Fatalf("output kept after the job ended: %q", s.pending)
	}
	s.Close()
}

// A job with no output at all still finds out it was ended.
func TestSilentJobFindsOutItWasEnded(t *testing.T) {
	old := keepAliveEvery
	keepAliveEvery = 20 * time.Millisecond
	defer func() { keepAliveEvery = old }()

	ended := make(chan struct{})
	s := New("j", func(_ string, _ int, chunk string) error {
		if chunk != "" {
			t.Errorf("a keep-alive must be empty, got %q", chunk)
		}
		return ErrEnded
	}, func() { close(ended) })
	defer s.Close()
	select {
	case <-ended:
	case <-time.After(3 * time.Second):
		t.Fatal("a silent job never learned the Hub ended it")
	}
}
