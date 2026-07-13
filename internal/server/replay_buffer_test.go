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

func TestReplayBufferReportsTruncatedHistory(t *testing.T) {
	now := time.Date(2026, time.July, 13, 12, 0, 0, 0, time.UTC)
	buffer := newReplayBuffer(5, time.Minute, func() time.Time { return now })
	buffer.append([]byte("abc"))
	buffer.append([]byte("defg"))
	output, complete := buffer.drainWithStatus()
	if complete {
		t.Fatal("byte-truncated replay was reported complete")
	}
	if got := bytes.Join(output, nil); !bytes.Equal(got, []byte("cdefg")) {
		t.Fatalf("drained output = %q, want cdefg", got)
	}

	buffer.append([]byte("old"))
	now = now.Add(time.Minute + time.Nanosecond)
	output, complete = buffer.drainWithStatus()
	if complete || len(output) != 0 {
		t.Fatalf("expired replay complete/output = %t/%q, want false/empty", complete, bytes.Join(output, nil))
	}
}

func TestReplayBufferResetsCompletenessAfterDrain(t *testing.T) {
	buffer := newReplayBuffer(3, time.Minute, func() time.Time { return time.Time{} })
	buffer.append([]byte("overflow"))
	_, complete := buffer.drainWithStatus()
	if complete {
		t.Fatal("truncated replay was reported complete")
	}
	buffer.append([]byte("new"))
	output, complete := buffer.drainWithStatus()
	if !complete || !bytes.Equal(bytes.Join(output, nil), []byte("new")) {
		t.Fatalf("next replay complete/output = %t/%q, want true/new", complete, bytes.Join(output, nil))
	}
}
