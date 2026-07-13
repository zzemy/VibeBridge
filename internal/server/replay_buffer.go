package server

import "time"

type bufferedOutput struct {
	data       []byte
	receivedAt time.Time
}

type replayBuffer struct {
	maxBytes int
	maxAge   time.Duration
	now      func() time.Time
	entries  []bufferedOutput
	bytes    int
}

func newReplayBuffer(maxBytes int, maxAge time.Duration, now func() time.Time) replayBuffer {
	if now == nil {
		now = time.Now
	}
	return replayBuffer{maxBytes: maxBytes, maxAge: maxAge, now: now}
}

func (b *replayBuffer) append(data []byte) {
	if len(data) == 0 || b.maxBytes <= 0 || b.maxAge <= 0 {
		return
	}

	now := b.now()
	b.pruneExpired(now)
	if len(data) > b.maxBytes {
		data = data[len(data)-b.maxBytes:]
	}
	copied := append([]byte(nil), data...)
	b.entries = append(b.entries, bufferedOutput{data: copied, receivedAt: now})
	b.bytes += len(copied)

	for b.bytes > b.maxBytes && len(b.entries) > 0 {
		overflow := b.bytes - b.maxBytes
		oldest := &b.entries[0]
		if overflow < len(oldest.data) {
			oldest.data = oldest.data[overflow:]
			b.bytes -= overflow
			break
		}
		b.bytes -= len(oldest.data)
		b.entries = b.entries[1:]
	}
}

func (b *replayBuffer) snapshot() [][]byte {
	output := make([][]byte, len(b.entries))
	for index, entry := range b.entries {
		output[index] = entry.data
	}
	return output
}

func (b *replayBuffer) drain() [][]byte {
	b.pruneExpired(b.now())
	output := b.snapshot()
	b.entries = nil
	b.bytes = 0
	return output
}

func (b *replayBuffer) pruneExpired(now time.Time) {
	cutoff := now.Add(-b.maxAge)
	firstCurrent := 0
	for firstCurrent < len(b.entries) && b.entries[firstCurrent].receivedAt.Before(cutoff) {
		b.bytes -= len(b.entries[firstCurrent].data)
		firstCurrent++
	}
	b.entries = b.entries[firstCurrent:]
}
