// Package proxy contains backend-neutral remote attachment behavior.
package proxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/charmbracelet/x/term"
	"golang.org/x/sys/unix"
)

const (
	// HealthyAttachmentPeriod is the minimum attachment duration considered a
	// recovered connection before backoff is reset.
	HealthyAttachmentPeriod = time.Minute
	connectionExitCode      = 255
)

// Transport attaches to a remote container. An attachment returning an error
// is considered a dropped connection and is retried by RunReconnect.
type Transport interface {
	Attach(context.Context, string, string) error
}

// Clock is the sleep seam for reconnect backoff tests. Implementations may
// also provide Now() for deterministic healthy-attachment tests.
type Clock interface {
	Sleep(context.Context, time.Duration) error
}

type systemClock struct{}

func (systemClock) Sleep(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// RunReconnect attaches repeatedly after transport failures. A q read while
// waiting for the next attempt closes the proxy without touching the remote
// container.
func RunReconnect(ctx context.Context, transport Transport, clock Clock, input io.Reader, machine, container string, initial, maximum time.Duration) error {
	if transport == nil {
		return errors.New("proxy transport is required")
	}
	if clock == nil {
		clock = systemClock{}
	}
	if initial <= 0 {
		initial = time.Second
	}
	if maximum <= 0 {
		maximum = 30 * time.Second
	}
	if initial > maximum {
		initial = maximum
	}

	delay := initial
	for {
		started := now(clock)
		err := transport.Attach(ctx, machine, container)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !isConnectionError(err) {
			return err
		}
		if now(clock).Sub(started) >= HealthyAttachmentPeriod {
			// A connection that remained up for a meaningful period marks the
			// previous outage recovered. The next outage gets the initial delay.
			delay = initial
		}
		quit, sleepErr := waitRetry(ctx, clock, input, delay)
		if sleepErr != nil {
			return sleepErr
		}
		if quit {
			return nil
		}
		if delay < maximum {
			delay *= 2
			if delay > maximum || delay < 0 {
				delay = maximum
			}
		}
	}
}

func isConnectionError(err error) bool {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode() == connectionExitCode
	}
	return true
}

type clockNow interface {
	Now() time.Time
}

func now(clock Clock) time.Time {
	if clockWithNow, ok := clock.(clockNow); ok {
		return clockWithNow.Now()
	}
	return time.Now()
}

// waitRetry owns input for the complete retry wait. It keeps the terminal raw
// and keeps reading until q/Q, cancellation, or a read error, discarding every
// other byte so no key typed during the wait leaks to the next attachment.
func waitRetry(ctx context.Context, clock Clock, input io.Reader, delay time.Duration) (bool, error) {
	if input != nil {
		if key, available := availableKey(input); available {
			return key == 'q' || key == 'Q', nil
		}
	}

	sleepDone := make(chan error, 1)
	go func() { sleepDone <- clock.Sleep(ctx, delay) }()
	if input == nil {
		return false, <-sleepDone
	}

	reader, stop, done, restore, ok := startInput(input)
	if !ok {
		// A reader that cannot be canceled cannot safely run concurrently with
		// the next attachment. Buffered readers were handled above; for an
		// arbitrary blocking reader, honor the retry timer without starting a
		// stale read goroutine.
		return false, <-sleepDone
	}
	fmt.Fprintf(os.Stderr, "reconnecting in %s — press q to close\n", delay)
	keyRead := make(chan error, 1)
	go func() {
		defer close(done)
		var one [1]byte
		for {
			count, err := reader.Read(one[:])
			if err != nil {
				keyRead <- err
				return
			}
			if count == 0 {
				continue
			}
			if one[0] == 'q' || one[0] == 'Q' {
				keyRead <- nil
				return
			}
		}
	}()

	cleanup := func() {
		stop()
		<-done
		if closer, ok := reader.(io.Closer); ok {
			_ = closer.Close()
		}
		restore()
	}
	select {
	case <-ctx.Done():
		cleanup()
		return false, ctx.Err()
	case err := <-sleepDone:
		cleanup()
		return false, err
	case resultErr := <-keyRead:
		cleanup()
		if resultErr != nil {
			// EOF or unavailable input should not prevent a retry, but the
			// configured delay still applies.
			return false, <-sleepDone
		}
		return true, nil
	}
}

// startInput returns a cancelable one-byte reader and a cleanup function for
// terminal state. os.File inputs use unix.Poll with a cancellation pipe so
// stopping a retry wakes a genuinely blocked read. os.Stdin is duplicated
// before reading so canceling a retry never closes process standard input.
func startInput(input io.Reader) (io.Reader, func(), chan struct{}, func(), bool) {
	restore := func() {}

	if file, isFile := input.(*os.File); isFile {
		var state *term.State
		if term.IsTerminal(file.Fd()) {
			var err error
			state, err = term.MakeRaw(file.Fd())
			if err != nil {
				return nil, nil, nil, nil, false
			}
			restore = func() { _ = term.Restore(file.Fd(), state) }
		}
		fd, err := unix.Dup(int(file.Fd()))
		if err != nil {
			restore()
			return nil, nil, nil, nil, false
		}
		cancel := make([]int, 2)
		if err := unix.Pipe(cancel); err != nil {
			_ = unix.Close(fd)
			restore()
			return nil, nil, nil, nil, false
		}
		reader := &pollReader{fd: fd, cancelFD: cancel[0], cancelWriteFD: cancel[1]}
		finished := make(chan struct{})
		return reader, reader.cancel, finished, restore, true
	}

	closer, canClose := input.(io.Closer)
	if !canClose {
		return nil, nil, nil, nil, false
	}
	finished := make(chan struct{})
	return input, func() { _ = closer.Close() }, finished, restore, true
}

type pollReader struct {
	fd            int
	cancelFD      int
	cancelWriteFD int
	cancelOnce    sync.Once
	closeOnce     sync.Once
}

func (r *pollReader) Read(buffer []byte) (int, error) {
	if len(buffer) == 0 {
		return 0, nil
	}
	pollfds := []unix.PollFd{
		{Fd: int32(r.fd), Events: unix.POLLIN},
		{Fd: int32(r.cancelFD), Events: unix.POLLIN},
	}
	for {
		_, err := unix.Poll(pollfds, -1)
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			return 0, err
		}
		if pollfds[1].Revents != 0 {
			return 0, errInputCanceled
		}
		if pollfds[0].Revents == 0 {
			continue
		}
		n, err := unix.Read(r.fd, buffer)
		if err == unix.EINTR {
			continue
		}
		return n, err
	}
}

func (r *pollReader) cancel() {
	r.cancelOnce.Do(func() {
		if r.cancelWriteFD < 0 {
			return
		}
		_, _ = unix.Write(r.cancelWriteFD, []byte{1})
		_ = unix.Close(r.cancelWriteFD)
		r.cancelWriteFD = -1
	})
}

func (r *pollReader) Close() error {
	r.closeOnce.Do(func() {
		_ = unix.Close(r.fd)
		_ = unix.Close(r.cancelFD)
		if r.cancelWriteFD >= 0 {
			_ = unix.Close(r.cancelWriteFD)
			r.cancelWriteFD = -1
		}
	})
	return nil
}

var errInputCanceled = errors.New("proxy input canceled")

func availableKey(input io.Reader) (byte, bool) {
	for inputBuffered(input) {
		var one [1]byte
		count, err := input.Read(one[:])
		if count != 1 || err != nil {
			return 0, false
		}
		if one[0] == 'q' || one[0] == 'Q' {
			return one[0], true
		}
	}
	return 0, false
}

func inputBuffered(input io.Reader) bool {
	if source, ok := input.(interface{ Len() int }); ok && source.Len() > 0 {
		return true
	}
	if source, ok := input.(interface{ Buffered() int }); ok && source.Buffered() > 0 {
		return true
	}
	return false
}
