package order

import (
	"fmt"
	"testing"
)

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/accepted_batch.rs:83
//	test: test_empty_batch
func TestOrderAcceptedBatchEmpty(t *testing.T) {
	batch := NewOrderAcceptedBatch(nil)
	if !batch.IsEmpty() || batch.Len() != 0 {
		t.Fatalf("batch = %#v", batch)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/accepted_batch.rs:90
//	test: test_batch_with_events
func TestOrderAcceptedBatchWithEvents(t *testing.T) {
	batch := NewOrderAcceptedBatch([]OrderAcceptedEvent{{}, {}})
	if batch.IsEmpty() || batch.Len() != 2 {
		t.Fatalf("batch = %#v", batch)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/accepted_batch.rs:98
//	test: test_debug_display
func TestOrderAcceptedBatchDebugDisplay(t *testing.T) {
	batch := NewOrderAcceptedBatch([]OrderAcceptedEvent{{}})
	if fmt.Sprint(batch) != "OrderAcceptedBatch(len=1)" ||
		fmt.Sprintf("%#v", batch) != "OrderAcceptedBatch { len: 1 }" {
		t.Fatalf("display/debug = %q/%q", fmt.Sprint(batch), fmt.Sprintf("%#v", batch))
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/accepted_batch.rs:105
//	test: test_into_iter
func TestOrderAcceptedBatchIntoIter(t *testing.T) {
	batch := NewOrderAcceptedBatch([]OrderAcceptedEvent{{}, {}})
	count := 0
	for range batch.Events {
		count++
	}
	if count != 2 {
		t.Fatalf("iteration count = %d", count)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/canceled_batch.rs:83
//	test: test_empty_batch
func TestOrderCanceledBatchEmpty(t *testing.T) {
	batch := NewOrderCanceledBatch(nil)
	if !batch.IsEmpty() || batch.Len() != 0 {
		t.Fatalf("batch = %#v", batch)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/canceled_batch.rs:90
//	test: test_batch_with_events
func TestOrderCanceledBatchWithEvents(t *testing.T) {
	batch := NewOrderCanceledBatch([]OrderCanceledEvent{{}, {}})
	if batch.IsEmpty() || batch.Len() != 2 {
		t.Fatalf("batch = %#v", batch)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/canceled_batch.rs:98
//	test: test_debug_display
func TestOrderCanceledBatchDebugDisplay(t *testing.T) {
	batch := NewOrderCanceledBatch([]OrderCanceledEvent{{}})
	if fmt.Sprint(batch) != "OrderCanceledBatch(len=1)" ||
		fmt.Sprintf("%#v", batch) != "OrderCanceledBatch { len: 1 }" {
		t.Fatalf("display/debug = %q/%q", fmt.Sprint(batch), fmt.Sprintf("%#v", batch))
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/canceled_batch.rs:105
//	test: test_into_iter
func TestOrderCanceledBatchIntoIter(t *testing.T) {
	batch := NewOrderCanceledBatch([]OrderCanceledEvent{{}, {}})
	count := 0
	for range batch.Events {
		count++
	}
	if count != 2 {
		t.Fatalf("iteration count = %d", count)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/submitted_batch.rs:83
//	test: test_empty_batch
func TestOrderSubmittedBatchEmpty(t *testing.T) {
	batch := NewOrderSubmittedBatch(nil)
	if !batch.IsEmpty() || batch.Len() != 0 {
		t.Fatalf("batch = %#v", batch)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/submitted_batch.rs:90
//	test: test_batch_with_events
func TestOrderSubmittedBatchWithEvents(t *testing.T) {
	batch := NewOrderSubmittedBatch([]OrderSubmittedEvent{{}, {}})
	if batch.IsEmpty() || batch.Len() != 2 {
		t.Fatalf("batch = %#v", batch)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/submitted_batch.rs:98
//	test: test_debug_display
func TestOrderSubmittedBatchDebugDisplay(t *testing.T) {
	batch := NewOrderSubmittedBatch([]OrderSubmittedEvent{{}})
	if fmt.Sprint(batch) != "OrderSubmittedBatch(len=1)" ||
		fmt.Sprintf("%#v", batch) != "OrderSubmittedBatch { len: 1 }" {
		t.Fatalf("display/debug = %q/%q", fmt.Sprint(batch), fmt.Sprintf("%#v", batch))
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/submitted_batch.rs:105
//	test: test_into_iter
func TestOrderSubmittedBatchIntoIter(t *testing.T) {
	batch := NewOrderSubmittedBatch([]OrderSubmittedEvent{{}, {}})
	count := 0
	for range batch.Events {
		count++
	}
	if count != 2 {
		t.Fatalf("iteration count = %d", count)
	}
}
