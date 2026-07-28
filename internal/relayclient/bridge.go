package relayclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	vibebridgev1 "github.com/zzemy/VibeBridge/gen/go/vibebridge/v1"
)

// Bridge pumps bytes in both directions between local and remote
// until either side reaches EOF or errors. The returned error is the
// first non-benign error from either side, or nil if both sides
// closed cleanly.
//
// Bridge takes ownership of both connections: when one io.Copy
// returns, the corresponding destination is closed so the other
// io.Copy can unblock and return. The caller must not close local
// or remote while Bridge is running.
//
// Bridge ignores ctx today; it is accepted for symmetry with future
// versions that may want a hard deadline independent of the
// underlying connection lifetime. Callers should rely on context
// cancellation on the WebSocket dial instead of passing a ctx here.
func Bridge(ctx context.Context, local io.ReadWriteCloser, remote io.ReadWriteCloser) error {
	_ = ctx
	if local == nil {
		return errors.New("relay bridge: local must not be nil")
	}
	if remote == nil {
		return errors.New("relay bridge: remote must not be nil")
	}
	// Guarded closer per side so a fast peer that has already
	// returned does not race the slow side closing the same
	// connection.
	var (
		localGuard  onceClose
		remoteGuard onceClose
		wg          sync.WaitGroup
		errCh       = make(chan error, 2)
	)
	localGuard.c = local
	remoteGuard.c = remote

	wg.Add(2)
	go func() {
		defer wg.Done()
		_, err := io.Copy(remote, local)
		// Unblock the other side if it is still reading from
		// remote by closing remote.
		_ = remoteGuard.close()
		errCh <- err
	}()
	go func() {
		defer wg.Done()
		_, err := io.Copy(local, remote)
		_ = localGuard.close()
		errCh <- err
	}()
	wg.Wait()
	close(errCh)

	var first error
	for err := range errCh {
		if isBenignClose(err) {
			continue
		}
		if first == nil {
			first = err
		}
	}
	if first != nil {
		return fmt.Errorf("relay bridge: %w", first)
	}
	return nil
}

// ConnectAndBridge dials the relay with ticket and bridges the
// resulting Stream to local. It is the one-call helper the Agent
// uses to attach a local transport to a relay route: the caller
// supplies the Agent ticket minted via Pair and the local
// ReadWriteCloser that should be wired to the Agent's transport
// (typically the local pairing transport).
//
// The returned error is whatever Dial or Bridge returns. The Stream
// is always closed before ConnectAndBridge returns.
func ConnectAndBridge(ctx context.Context, dialer Dialer, target string, ticket *vibebridgev1.RelayTicket, local io.ReadWriteCloser) error {
	stream, err := dialer.Dial(ctx, target, ticket)
	if err != nil {
		return err
	}
	defer stream.Close()
	return Bridge(ctx, local, stream)
}

func isBenignClose(err error) bool {
	return err == nil || err == io.EOF || err == io.ErrClosedPipe
}

type onceClose struct {
	c    io.Closer
	once sync.Once
	err  error
}

func (o *onceClose) close() error {
	o.once.Do(func() {
		if o.c != nil {
			o.err = o.c.Close()
		}
	})
	return o.err
}
