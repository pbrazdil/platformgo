package testkit

import (
	"time"

	"github.com/upcomers-org/platformgo/internal/engine"
)

// ManualClock advances only when a test explicitly changes it.
type ManualClock struct {
	now engine.LogicalTime
}

// NewManualClock starts a clock at the supplied logical time.
func NewManualClock(start engine.LogicalTime) *ManualClock {
	return &ManualClock{now: start}
}

// Now returns the current explicit logical time.
func (clock *ManualClock) Now() engine.LogicalTime {
	return clock.now
}

// Set replaces the current logical time.
func (clock *ManualClock) Set(value engine.LogicalTime) {
	clock.now = value
}

// Advance moves logical time by an explicit duration.
func (clock *ManualClock) Advance(duration time.Duration) {
	clock.now += engine.LogicalTime(duration)
}
