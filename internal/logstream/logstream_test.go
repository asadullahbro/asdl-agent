package logstream

import (
	"errors"
	"strings"
	"sync"
	"testing"
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
	s := New("j", k.send)
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
	s := New("j", k.send)
	s.Write([]byte("abc"))
	s.flush() // fails
	s.flush() // fails
	s.Close() // succeeds
	if k.got.String() != "abc" {
		t.Fatalf("got %q", k.got.String())
	}
}

func TestWriteAfterCloseIsHarmless(t *testing.T) {
	s := New("j", (&sink{}).send)
	s.Close()
	if n, err := s.Write([]byte("x")); n != 1 || err != nil {
		t.Fatal(n, err)
	}
}
