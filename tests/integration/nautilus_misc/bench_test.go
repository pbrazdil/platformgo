package nautilusmisc

import "testing"

// Ported from:
// platform: 50141367492be46ebf5623f6191a14b94af2f2bd
// source: apps/nautilus/tests/live/bench/e2e_saga_throughput.rs:198
// test: bench_single_account_contention
func TestBenchSingleAccountContention(t *testing.T) {
	const operations = 100
	bench := newSagaBench()
	bench.addAccount("bench_single")
	for index := 0; index < operations; index++ {
		bench.deposit("bench_single", 1)
	}
	account := bench.accounts["bench_single"]
	if bench.completed != operations || account.Operations != operations || account.Balance != operations {
		t.Fatalf("completed=%d account=%#v, want %d deposits", bench.completed, account, operations)
	}
	if bench.logicalMS > 120_000 {
		t.Fatalf("logical completion = %dms, exceeds 120s", bench.logicalMS)
	}
	assertDeterministicThroughput(t, bench)
}

// Ported from:
// platform: 50141367492be46ebf5623f6191a14b94af2f2bd
// source: apps/nautilus/tests/live/bench/e2e_saga_throughput.rs:256
// test: bench_multi_account_parallel
func TestBenchMultiAccountParallel(t *testing.T) {
	const (
		accountCount = 20
		operations   = 100
	)
	bench := newSagaBench()
	for index := 0; index < accountCount; index++ {
		bench.addAccount(benchLogin(index))
	}
	for index := 0; index < operations; index++ {
		bench.deposit(benchLogin(index%accountCount), 1)
	}
	if bench.completed != operations || len(bench.accounts) != accountCount {
		t.Fatalf("completed=%d accounts=%d", bench.completed, len(bench.accounts))
	}
	for login, account := range bench.accounts {
		if account.Operations != 5 || account.Balance != 5 {
			t.Errorf("%s = %#v, want five deposits", login, account)
		}
	}
	if bench.logicalMS > 120_000 {
		t.Fatalf("logical completion = %dms, exceeds 120s", bench.logicalMS)
	}
	assertDeterministicThroughput(t, bench)
}

func benchLogin(index int) string {
	const digits = "0123456789"
	if index < 10 {
		return "bench_multi_" + string(digits[index])
	}
	return "bench_multi_1" + string(digits[index-10])
}

func assertDeterministicThroughput(t *testing.T, bench *sagaBench) {
	t.Helper()
	if got := bench.throughput(); got != 1000 {
		t.Fatalf("throughput = %v ops/s, want 1000", got)
	}
	latencies := bench.sortedLatencies()
	if p50 := percentile(latencies, 0.50); p50 != 5 {
		t.Errorf("p50 = %vms, want 5ms", p50)
	}
	if p95 := percentile(latencies, 0.95); p95 != 10 {
		t.Errorf("p95 = %vms, want 10ms", p95)
	}
	if p99 := percentile(latencies, 0.99); p99 != 10 {
		t.Errorf("p99 = %vms, want 10ms", p99)
	}
}
