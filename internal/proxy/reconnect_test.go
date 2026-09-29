package proxy

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

type fakeTransport struct {
	failures int
	calls    int
}

func (t *fakeTransport) Attach(context.Context, string, string) error {
	t.calls++
	if t.calls <= t.failures {
		return errors.New("connection dropped")
	}
	return nil
}

type fakeClock struct {
	delays []time.Duration
}

func (c *fakeClock) Sleep(_ context.Context, delay time.Duration) error {
	c.delays = append(c.delays, delay)
	return nil
}

func TestRunReconnectUsesFakeTransportAndBackoffClock(t *testing.T) {
	transport := &fakeTransport{failures: 3}
	clock := &fakeClock{}
	if err := RunReconnect(context.Background(), transport, clock, nil, "machine", "container", time.Second, 3*time.Second); err != nil {
		t.Fatalf("RunReconnect() error = %v", err)
	}
	if transport.calls != 4 {
		t.Fatalf("transport calls = %d, want 4", transport.calls)
	}
	if want := []time.Duration{time.Second, 2 * time.Second, 3 * time.Second}; !reflect.DeepEqual(clock.delays, want) {
		t.Fatalf("delays = %v, want %v", clock.delays, want)
	}
}

func TestRunReconnectQStopsBeforeRetry(t *testing.T) {
	transport := &fakeTransport{failures: 1}
	clock := &fakeClock{}
	if err := RunReconnect(context.Background(), transport, clock, bytes.NewBufferString("q"), "machine", "container", time.Second, time.Second); err != nil {
		t.Fatalf("RunReconnect() error = %v", err)
	}
	if transport.calls != 1 {
		t.Fatalf("transport calls = %d, want one", transport.calls)
	}
}
