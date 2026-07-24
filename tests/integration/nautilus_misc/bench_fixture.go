package nautilusmisc

import "sort"

type sagaAccount struct {
	Balance    int
	Operations int
}

type sagaBench struct {
	accounts  map[string]*sagaAccount
	latencies []float64
	completed int
	logicalMS int
}

func newSagaBench() *sagaBench {
	return &sagaBench{accounts: make(map[string]*sagaAccount)}
}

func (bench *sagaBench) addAccount(login string) {
	bench.accounts[login] = &sagaAccount{}
}

func (bench *sagaBench) deposit(login string, amount int) {
	account := bench.accounts[login]
	account.Balance += amount
	account.Operations++
	bench.completed++
	bench.logicalMS++
	bench.latencies = append(bench.latencies, float64((bench.completed-1)%10+1))
}

func (bench *sagaBench) throughput() float64 {
	if bench.logicalMS == 0 {
		return 0
	}
	return float64(bench.completed) / (float64(bench.logicalMS) / 1000)
}

func (bench *sagaBench) sortedLatencies() []float64 {
	latencies := append([]float64(nil), bench.latencies...)
	sort.Float64s(latencies)
	return latencies
}

func percentile(sorted []float64, proportion float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	rank := intCeiling(proportion * float64(len(sorted)))
	if rank < 1 {
		rank = 1
	}
	if rank > len(sorted) {
		rank = len(sorted)
	}
	return sorted[rank-1]
}

func intCeiling(value float64) int {
	integer := int(value)
	if float64(integer) == value {
		return integer
	}
	return integer + 1
}
