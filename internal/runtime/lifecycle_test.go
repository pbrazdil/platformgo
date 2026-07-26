package runtime

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestAPIKeyReplayCleanupOwnsBatchesCancellationAndFailure(t *testing.T) {
	t.Run("multiple batches and cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		ticks := make(chan time.Time)
		calls := make(chan int64, 2)
		var purges atomic.Int64
		result := make(chan error, 1)
		go func() {
			result <- runAPIKeyReplayCleanup(
				ctx,
				ticks,
				func(context.Context) (int64, error) {
					call := purges.Add(1)
					calls <- call
					return call, nil
				},
				func(context.Context) error { return nil },
			)
		}()
		ticks <- time.Time{}
		if call := <-calls; call != 1 {
			t.Fatalf("first cleanup call = %d", call)
		}
		ticks <- time.Time{}
		if call := <-calls; call != 2 {
			t.Fatalf("second cleanup call = %d", call)
		}
		cancel()
		if err := <-result; err != nil {
			t.Fatalf("canceled cleanup = %v", err)
		}
	})

	t.Run("purge failure", func(t *testing.T) {
		failure := errors.New("purge failed")
		ticks := make(chan time.Time, 1)
		ticks <- time.Time{}
		err := runAPIKeyReplayCleanup(
			context.Background(),
			ticks,
			func(context.Context) (int64, error) { return 0, failure },
			func(context.Context) error { return nil },
		)
		if !errors.Is(err, failure) ||
			!strings.Contains(err.Error(), "API-key replay cleanup") {
			t.Fatalf("purge failure = %v", err)
		}
	})

	t.Run("coverage failure", func(t *testing.T) {
		failure := errors.New("coverage failed")
		ticks := make(chan time.Time, 1)
		ticks <- time.Time{}
		err := runAPIKeyReplayCleanup(
			context.Background(),
			ticks,
			func(context.Context) (int64, error) { return 0, nil },
			func(context.Context) error { return failure },
		)
		if !errors.Is(err, failure) ||
			!strings.Contains(err.Error(), "API-key replay coverage") {
			t.Fatalf("coverage failure = %v", err)
		}
	})
}
