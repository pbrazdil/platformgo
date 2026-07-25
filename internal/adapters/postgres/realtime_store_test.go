package postgres

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/upcomers-org/platformgo/internal/engine"
)

func TestRealtimeUserChannelUsesFinalCanonicalSubjectSegment(t *testing.T) {
	for userID, want := range map[string]string{
		"urn:xb:user:user-7":                      "user:user-7",
		"urn:xb:user:" + strings.Repeat("a", 250): "user:" + strings.Repeat("a", 250),
	} {
		got, err := realtimeUserChannel(userID)
		if err != nil {
			t.Fatalf("%s: %v", userID, err)
		}
		if got != want {
			t.Fatalf("%s channel = %q, want %q", userID, got, want)
		}
	}
	for _, userID := range []string{
		"user-7",
		"urn:xb:user:",
		"urn:xb:user:tenant:user-8",
		"urn:xb:user:někdo",
		"urn:xb:user:user/8",
		"urn:xb:user:" + strings.Repeat("a", 251),
	} {
		if _, err := realtimeUserChannel(userID); err == nil {
			t.Fatalf("invalid user ID %q accepted", userID)
		}
	}
}

func TestRealtimeFailureDetailIsPostgresSafeAndByteBounded(t *testing.T) {
	raw := "\xff\x00" + strings.Repeat("€", 1_365) + "tail"
	detail := realtimeFailureDetail(errors.New(raw))
	if len(detail) > 4096 || !strings.Contains(detail, "\uFFFD") ||
		strings.ContainsRune(detail, '\x00') {
		t.Fatalf("unsafe failure detail length=%d value=%q", len(detail), detail)
	}
}

func TestRealtimeOrderEventTypeDoesNotInferTriggerFromLatchedState(t *testing.T) {
	order := engine.OrderSnapshot{Triggered: true, Version: 2}
	for _, kind := range []string{"order.working", "order.rejected"} {
		got, ok := realtimeOrderEventType(kind, order)
		if !ok || got != "order.updated" {
			t.Fatalf("%s mapped to %q, accepted=%t", kind, got, ok)
		}
	}
	if got, ok := realtimeOrderEventType("order.triggered", order); !ok ||
		got != "order.triggered" {
		t.Fatalf("explicit trigger mapped to %q, accepted=%t", got, ok)
	}
}

func TestRealtimeProjectionsPreserveClientTransitionSequence(t *testing.T) {
	orderID := engine.IDFromSequence(engine.ID{}, 71)
	eventID := engine.IDFromSequence(engine.ID{}, 72)
	logicalTime := engine.NewLogicalTime(time.Unix(0, 77))
	base := engine.OrderSnapshot{
		OrderID: orderID, Status: engine.OrderStatusFilled,
		FilledQuantity: "1", Version: 2,
	}
	event := engine.DomainEvent{
		EventID: eventID, Kind: "order.filled", AggregateID: orderID,
		AggregateVersion: 2, LogicalTime: logicalTime,
	}
	projections, err := realtimeProjections(
		engine.TradingAction{
			Kind:        engine.TradingActionSubmitOrder,
			SubmitOrder: &engine.SubmitOrder{OrderID: orderID},
		},
		engine.Decision{OrderChanges: []engine.OrderSnapshot{base}},
		event,
		false,
		"0",
		engine.OrderStatusWorking,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(projections) != 2 ||
		projections[0].event.Kind != "order.created" ||
		projections[1].event.Kind != "order.filled" ||
		projections[0].event.EventID == eventID ||
		projections[1].event.EventID != eventID {
		t.Fatalf("immediate-fill projections = %+v", projections)
	}
	rejected := base
	rejected.Status = engine.OrderStatusRejected
	rejected.FilledQuantity = "0"
	rejectedEvent := event
	rejectedEvent.Kind = "order.rejected"
	rejectedProjections, err := realtimeProjections(
		engine.TradingAction{
			Kind:        engine.TradingActionSubmitOrder,
			SubmitOrder: &engine.SubmitOrder{OrderID: orderID},
		},
		engine.Decision{OrderChanges: []engine.OrderSnapshot{rejected}},
		rejectedEvent,
		false,
		"0",
		engine.OrderStatusWorking,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(rejectedProjections) != 1 ||
		rejectedProjections[0].event.Kind != "order.rejected" {
		t.Fatalf("rejected submit projections = %+v", rejectedProjections)
	}
	bracketProjections, err := realtimeProjections(
		engine.TradingAction{
			Kind: engine.TradingActionPlaceBracket,
			PlaceBracket: &engine.PlaceBracket{
				EntryOrderID: orderID,
			},
		},
		engine.Decision{OrderChanges: []engine.OrderSnapshot{base}},
		event,
		false,
		"0",
		engine.OrderStatusWorking,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(bracketProjections) != 2 ||
		bracketProjections[0].event.Kind != "order.created" ||
		bracketProjections[1].event.Kind != "order.filled" {
		t.Fatalf("immediate bracket projections = %+v", bracketProjections)
	}

	triggered := base
	triggered.Status = engine.OrderStatusWorking
	triggered.FilledQuantity = "0"
	triggered.Triggered = true
	triggered.TriggeredAt = logicalTime
	triggerEvent := event
	triggerEvent.Kind = "order.working"
	triggerOnly, err := realtimeProjections(
		engine.TradingAction{Kind: engine.TradingActionUpdateBook},
		engine.Decision{OrderChanges: []engine.OrderSnapshot{triggered}},
		triggerEvent,
		false,
		"0",
		engine.OrderStatusWorking,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(triggerOnly) != 1 ||
		triggerOnly[0].event.Kind != "order.triggered" {
		t.Fatalf("resting trigger projections = %+v", triggerOnly)
	}

	amended := triggered
	amended.TriggeredAt = logicalTime - 1
	amendOnly, err := realtimeProjections(
		engine.TradingAction{Kind: engine.TradingActionAmendOrder},
		engine.Decision{OrderChanges: []engine.OrderSnapshot{amended}},
		triggerEvent,
		true,
		"0",
		engine.OrderStatusWorking,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(amendOnly) != 1 ||
		amendOnly[0].event.Kind != "order.working" {
		t.Fatalf("post-trigger amend projections = %+v", amendOnly)
	}
	immediateAmend, err := realtimeProjections(
		engine.TradingAction{
			Kind: engine.TradingActionAmendOrder,
			AmendOrder: &engine.AmendOrder{
				OrderID: orderID, Quantity: "1", Price: "100",
			},
		},
		engine.Decision{OrderChanges: []engine.OrderSnapshot{base}},
		event,
		true,
		"0.25",
		engine.OrderStatusPartiallyFilled,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(immediateAmend) != 2 ||
		immediateAmend[0].event.Kind != "order.updated" ||
		immediateAmend[0].decision.OrderChanges[0].FilledQuantity != "0.25" ||
		immediateAmend[0].decision.OrderChanges[0].Status !=
			engine.OrderStatusPartiallyFilled ||
		immediateAmend[1].event.Kind != "order.filled" {
		t.Fatalf("immediate amend-fill projections = %+v", immediateAmend)
	}
	amendedTriggerFill := base
	amendedTriggerFill.Triggered = true
	amendedTriggerFill.TriggeredAt = logicalTime
	amendTriggerProjections, err := realtimeProjections(
		engine.TradingAction{
			Kind: engine.TradingActionAmendOrder,
			AmendOrder: &engine.AmendOrder{
				OrderID: orderID, Quantity: "1", Price: "100",
			},
		},
		engine.Decision{OrderChanges: []engine.OrderSnapshot{amendedTriggerFill}},
		event,
		false,
		"0",
		engine.OrderStatusWorking,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(amendTriggerProjections) != 3 ||
		amendTriggerProjections[0].event.Kind != "order.updated" ||
		amendTriggerProjections[1].event.Kind != "order.triggered" ||
		amendTriggerProjections[2].event.Kind != "order.filled" {
		t.Fatalf(
			"amend-trigger-fill projections = %+v",
			amendTriggerProjections,
		)
	}
}
