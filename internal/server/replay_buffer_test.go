package server

import (
	"bytes"
	"testing"
	"time"
)

func TestReplayBufferBoundsOutputByBytes(t *testing.T) {
	now := time.Date(2026, time.July, 12, 12, 0, 0, 0, time.UTC)
	buffer := newReplayBuffer(5, time.Minute, func() time.Time { return now })
	buffer.append([]byte("abc"))
	buffer.append([]byte("defg"))
	if got := bytes.Join(buffer.drain(), nil); !bytes.Equal(got, []byte("cdefg")) {
		t.Fatalf("drained output = %q, want %q", got, "cdefg")
	}
}

func TestReplayBufferKeepsTailOfOversizedChunk(t *testing.T) {
	buffer := newReplayBuffer(4, time.Minute, func() time.Time { return time.Time{} })
	buffer.append([]byte("oversized"))
	if got := bytes.Join(buffer.drain(), nil); !bytes.Equal(got, []byte("ized")) {
		t.Fatalf("drained output = %q, want %q", got, "ized")
	}
}

func TestReplayBufferExpiresOldOutput(t *testing.T) {
	now := time.Date(2026, time.July, 12, 12, 0, 0, 0, time.UTC)
	buffer := newReplayBuffer(1024, time.Minute, func() time.Time { return now })
	buffer.append([]byte("expired"))
	now = now.Add(time.Minute + time.Nanosecond)
	buffer.append([]byte("current"))
	if got := bytes.Join(buffer.drain(), nil); !bytes.Equal(got, []byte("current")) {
		t.Fatalf("drained output = %q, want %q", got, "current")
	}
}

func TestReplayBufferDrainClearsBufferedOutput(t *testing.T) {
	buffer := newReplayBuffer(1024, time.Minute, func() time.Time { return time.Time{} })
	buffer.append([]byte("once"))
	_ = buffer.drain()
	if got := buffer.drain(); len(got) != 0 {
		t.Fatalf("second drain returned %d chunks, want 0", len(got))
	}
}
