package runtime

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
)

func TestReplayCleanupOwnsBatchesCancellationAndFailure(t *testing.T) {
	t.Run("both purges run sequentially on every tick and cancellation stops the owner", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		ticks := make(chan time.Time)
		type cleanupCall struct {
			kind     string
			sequence int64
		}
		calls := make(chan cleanupCall, 6)
		var apiKeyPurges atomic.Int64
		var brokerEchoPurges atomic.Int64
		var coverageChecks atomic.Int64
		result := make(chan error, 1)
		go func() {
			result <- runReplayCleanup(
				ctx,
				ticks,
				func(context.Context) (int64, error) {
					call := apiKeyPurges.Add(1)
					calls <- cleanupCall{kind: "API-key", sequence: call}
					return call, nil
				},
				func(context.Context) (int64, error) {
					call := brokerEchoPurges.Add(1)
					calls <- cleanupCall{
						kind:     "broker-echo",
						sequence: call,
					}
					return call, nil
				},
				func(context.Context) error {
					call := coverageChecks.Add(1)
					calls <- cleanupCall{kind: "coverage", sequence: call}
					return nil
				},
			)
		}()
		ticks <- time.Time{}
		if call := <-calls; call != (cleanupCall{kind: "API-key", sequence: 1}) {
			t.Fatalf("first cleanup call = %#v", call)
		}
		if call := <-calls; call != (cleanupCall{kind: "broker-echo", sequence: 1}) {
			t.Fatalf("second cleanup call = %#v", call)
		}
		if call := <-calls; call != (cleanupCall{kind: "coverage", sequence: 1}) {
			t.Fatalf("third cleanup call = %#v", call)
		}
		ticks <- time.Time{}
		if call := <-calls; call != (cleanupCall{kind: "API-key", sequence: 2}) {
			t.Fatalf("fourth cleanup call = %#v", call)
		}
		if call := <-calls; call != (cleanupCall{kind: "broker-echo", sequence: 2}) {
			t.Fatalf("fifth cleanup call = %#v", call)
		}
		if call := <-calls; call != (cleanupCall{kind: "coverage", sequence: 2}) {
			t.Fatalf("sixth cleanup call = %#v", call)
		}
		cancel()
		if err := <-result; err != nil {
			t.Fatalf("canceled cleanup = %v", err)
		}
		if got := coverageChecks.Load(); got != 2 {
			t.Fatalf("coverage checks = %d", got)
		}
	})

	t.Run("API-key purge failure", func(t *testing.T) {
		failure := errors.New("API-key purge failed")
		ticks := make(chan time.Time, 1)
		ticks <- time.Time{}
		var brokerEchoCalls atomic.Int64
		var coverageCalls atomic.Int64
		err := runReplayCleanup(
			context.Background(),
			ticks,
			func(context.Context) (int64, error) { return 0, failure },
			func(context.Context) (int64, error) {
				brokerEchoCalls.Add(1)
				return 0, nil
			},
			func(context.Context) error {
				coverageCalls.Add(1)
				return nil
			},
		)
		if !errors.Is(err, failure) ||
			!strings.Contains(err.Error(), "API-key replay cleanup") {
			t.Fatalf("API-key purge failure = %v", err)
		}
		if got := brokerEchoCalls.Load(); got != 0 {
			t.Fatalf("broker-echo purge calls = %d", got)
		}
		if got := coverageCalls.Load(); got != 0 {
			t.Fatalf("coverage calls = %d", got)
		}
	})

	t.Run("broker-echo purge failure", func(t *testing.T) {
		failure := errors.New("broker-echo purge failed")
		ticks := make(chan time.Time, 1)
		ticks <- time.Time{}
		var apiKeyCalls atomic.Int64
		var coverageCalls atomic.Int64
		err := runReplayCleanup(
			context.Background(),
			ticks,
			func(context.Context) (int64, error) {
				apiKeyCalls.Add(1)
				return 0, nil
			},
			func(context.Context) (int64, error) { return 0, failure },
			func(context.Context) error {
				coverageCalls.Add(1)
				return nil
			},
		)
		if !errors.Is(err, failure) ||
			!strings.Contains(err.Error(), "broker-echo replay cleanup") {
			t.Fatalf("broker-echo purge failure = %v", err)
		}
		if got := apiKeyCalls.Load(); got != 1 {
			t.Fatalf("API-key purge calls = %d", got)
		}
		if got := coverageCalls.Load(); got != 0 {
			t.Fatalf("coverage calls = %d", got)
		}
	})

	t.Run("coverage failure", func(t *testing.T) {
		failure := errors.New("coverage failed")
		ticks := make(chan time.Time, 1)
		ticks <- time.Time{}
		var apiKeyCalls atomic.Int64
		var brokerEchoCalls atomic.Int64
		err := runReplayCleanup(
			context.Background(),
			ticks,
			func(context.Context) (int64, error) {
				apiKeyCalls.Add(1)
				return 0, nil
			},
			func(context.Context) (int64, error) {
				brokerEchoCalls.Add(1)
				return 0, nil
			},
			func(context.Context) error { return failure },
		)
		if !errors.Is(err, failure) ||
			!strings.Contains(err.Error(), "replay coverage") {
			t.Fatalf("coverage failure = %v", err)
		}
		if got := apiKeyCalls.Load(); got != 1 {
			t.Fatalf("API-key purge calls = %d", got)
		}
		if got := brokerEchoCalls.Load(); got != 1 {
			t.Fatalf("broker-echo purge calls = %d", got)
		}
	})
}

func TestBrokerEchoCleanupDrainsOneBoundedCycle(t *testing.T) {
	t.Run("partial batch stops one wakeup after complete catch-up", func(t *testing.T) {
		deletions := []int64{100, 100, 5}
		var calls atomic.Int64
		deleted, err := drainExpiredBrokerEchoReplays(
			context.Background(),
			func(context.Context, int) (int64, error) {
				call := int(calls.Add(1)) - 1
				if call >= len(deletions) {
					t.Fatalf("unexpected purge call %d", call+1)
				}
				return deletions[call], nil
			},
			100,
			10,
		)
		if err != nil {
			t.Fatal(err)
		}
		if deleted != 205 || calls.Load() != 3 {
			t.Fatalf("deleted=%d calls=%d, want 205/3", deleted, calls.Load())
		}
	})

	t.Run("full batches stop at the policy work bound", func(t *testing.T) {
		var calls atomic.Int64
		deleted, err := drainExpiredBrokerEchoReplays(
			context.Background(),
			func(context.Context, int) (int64, error) {
				calls.Add(1)
				return 100, nil
			},
			100,
			10,
		)
		if err != nil {
			t.Fatal(err)
		}
		if deleted != 1000 || calls.Load() != 10 {
			t.Fatalf("deleted=%d calls=%d, want 1000/10", deleted, calls.Load())
		}
	})

	t.Run("cancellation stops between batches", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		var calls atomic.Int64
		_, err := drainExpiredBrokerEchoReplays(
			ctx,
			func(context.Context, int) (int64, error) {
				calls.Add(1)
				cancel()
				return 100, nil
			},
			100,
			10,
		)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled drain error = %v", err)
		}
		if calls.Load() != 1 {
			t.Fatalf("canceled drain calls = %d", calls.Load())
		}
	})
}

func TestBrokerEchoReplayCoverageFailsClosed(t *testing.T) {
	valid := platformpostgres.BrokerEchoReplayCoverage{
		MaxTotalRows:               1000,
		MaxRowsPerPrincipal:        100,
		PurgeBatchSize:             100,
		MaxBatchesPerCycle:         10,
		CleanupIntervalSeconds:     60,
		CleanupCycleTimeoutSeconds: 10,
		ExpiredReadinessSLOSeconds: 120,
		MaxRetryAfterSeconds:       86460,
	}
	if err := validateBrokerEchoReplayCoverage(valid, true, false); err != nil {
		t.Fatalf("valid empty coverage: %v", err)
	}

	live := valid
	live.TotalRows = 1
	live.LiveRows = 1
	live.MaximumPrincipalRows = 1
	live.OldestLiveExpiresAt = "2026-07-28 07:00:00+00"
	if err := validateBrokerEchoReplayCoverage(live, true, false); err != nil {
		t.Fatalf("valid live coverage: %v", err)
	}

	for _, test := range []struct {
		name           string
		coverage       platformpostgres.BrokerEchoReplayCoverage
		requireDrained bool
	}{
		{
			name: "aggregate mismatch",
			coverage: func() platformpostgres.BrokerEchoReplayCoverage {
				candidate := live
				candidate.TotalRows = 2
				return candidate
			}(),
		},
		{
			name: "global cap violation",
			coverage: func() platformpostgres.BrokerEchoReplayCoverage {
				candidate := live
				candidate.TotalRows = 1001
				candidate.LiveRows = 1001
				return candidate
			}(),
		},
		{
			name: "principal cap violation",
			coverage: func() platformpostgres.BrokerEchoReplayCoverage {
				candidate := live
				candidate.MaximumPrincipalRows = 101
				return candidate
			}(),
		},
		{
			name: "invalid live exact response",
			coverage: func() platformpostgres.BrokerEchoReplayCoverage {
				candidate := live
				candidate.InvalidLiveRows = 1
				return candidate
			}(),
		},
		{
			name: "live response exceeds maximum remaining lifetime",
			coverage: func() platformpostgres.BrokerEchoReplayCoverage {
				candidate := live
				candidate.OverlongLiveRows = 1
				return candidate
			}(),
		},
		{
			name: "expired backlog exceeds readiness SLO",
			coverage: func() platformpostgres.BrokerEchoReplayCoverage {
				candidate := valid
				candidate.TotalRows = 1
				candidate.ExpiredRows = 1
				candidate.MaximumPrincipalRows = 1
				candidate.OldestExpiredAt = "2026-07-27 06:57:59+00"
				candidate.OldestExpiredAgeSeconds = 121
				return candidate
			}(),
		},
		{
			name: "startup requires complete drain",
			coverage: func() platformpostgres.BrokerEchoReplayCoverage {
				candidate := valid
				candidate.TotalRows = 1
				candidate.ExpiredRows = 1
				candidate.MaximumPrincipalRows = 1
				candidate.OldestExpiredAt = "2026-07-27 06:59:59+00"
				candidate.OldestExpiredAgeSeconds = 1
				return candidate
			}(),
			requireDrained: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := validateBrokerEchoReplayCoverage(
				test.coverage,
				test.requireDrained,
				false,
			); err == nil {
				t.Fatal("invalid broker-echo replay coverage was accepted")
			}
		})
	}
}
