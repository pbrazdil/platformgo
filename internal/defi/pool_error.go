package defi

import (
	"fmt"

	"github.com/upcomers-org/platformgo/internal/ids"
)

type PoolEventKind uint8

const (
	PoolEventInitialize PoolEventKind = iota + 1
	PoolEventSwap
	PoolEventMint
	PoolEventBurn
	PoolEventCollect
	PoolEventFlash
)

func (k PoolEventKind) String() string {
	switch k {
	case PoolEventInitialize:
		return "Initialize"
	case PoolEventSwap:
		return "Swap"
	case PoolEventMint:
		return "Mint"
	case PoolEventBurn:
		return "Burn"
	case PoolEventCollect:
		return "Collect"
	case PoolEventFlash:
		return "Flash"
	default:
		return fmt.Sprintf("PoolEventKind(%d)", k)
	}
}

type PoolEventLocation struct {
	InstrumentID     ids.InstrumentID
	PoolIdentifier   PoolIdentifier
	Block            uint64
	TransactionIndex uint32
	LogIndex         uint32
	EventKind        PoolEventKind
}

func (l PoolEventLocation) String() string {
	return fmt.Sprintf(
		"pool=%s (%s) block=%d tx_index=%d log_index=%d event=%s",
		l.InstrumentID,
		l.PoolIdentifier,
		l.Block,
		l.TransactionIndex,
		l.LogIndex,
		l.EventKind,
	)
}

type LiquidityFailureKind uint8

const (
	LiquidityOverflow LiquidityFailureKind = iota + 1
	LiquidityUnderflow
)

type LiquidityMathFailure struct {
	Kind    LiquidityFailureKind
	Current uint64
	Delta   uint64
}

type PoolProfilerErrorKind uint8

const (
	ProfilerAlreadyInitialized PoolProfilerErrorKind = iota + 1
	ProfilerNotInitialized
	ProfilerInitialTickMismatch
	ProfilerLiquidityOverflow
	ProfilerLiquidityUnderflow
	ProfilerNoEventsProcessed
)

type PoolProfilerError struct {
	Kind           PoolProfilerErrorKind
	Location       *PoolEventLocation
	InstrumentID   ids.InstrumentID
	PoolIdentifier PoolIdentifier
	EventKind      PoolEventKind
	Current        uint64
	Delta          uint64
	InitialTick    int32
	CalculatedTick int32
}

func (e PoolProfilerError) EventLocation() *PoolEventLocation {
	if e.Kind == ProfilerLiquidityOverflow || e.Kind == ProfilerLiquidityUnderflow {
		return e.Location
	}
	return nil
}

func (e PoolProfilerError) Error() string {
	switch e.Kind {
	case ProfilerAlreadyInitialized:
		return fmt.Sprintf("Pool %s (%s) already initialized", e.InstrumentID, e.PoolIdentifier)
	case ProfilerNotInitialized:
		return fmt.Sprintf(
			"Pool %s (%s) is not initialized while processing %s",
			e.InstrumentID,
			e.PoolIdentifier,
			e.EventKind,
		)
	case ProfilerInitialTickMismatch:
		return fmt.Sprintf(
			"Initial tick mismatch for pool %s (%s): pool.initial_tick=%d, computed_from_sqrt_price=%d",
			e.InstrumentID,
			e.PoolIdentifier,
			e.InitialTick,
			e.CalculatedTick,
		)
	case ProfilerLiquidityOverflow:
		return fmt.Sprintf("Liquidity overflow at %s: current=%d, delta=%d", e.Location, e.Current, e.Delta)
	case ProfilerLiquidityUnderflow:
		return fmt.Sprintf("Liquidity underflow at %s: current=%d, delta=%d", e.Location, e.Current, e.Delta)
	case ProfilerNoEventsProcessed:
		return fmt.Sprintf(
			"No events processed yet for pool %s (%s); cannot extract snapshot",
			e.InstrumentID,
			e.PoolIdentifier,
		)
	default:
		return fmt.Sprintf("PoolProfilerError(%d)", e.Kind)
	}
}

func LiquidityErrorWithLocation(failure LiquidityMathFailure, location PoolEventLocation) PoolProfilerError {
	kind := ProfilerLiquidityOverflow
	if failure.Kind == LiquidityUnderflow {
		kind = ProfilerLiquidityUnderflow
	}
	return PoolProfilerError{
		Kind:     kind,
		Location: &location,
		Current:  failure.Current,
		Delta:    failure.Delta,
	}
}
