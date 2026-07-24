package order

import "fmt"

type OrderAcceptedBatch struct{ Events []OrderAcceptedEvent }

func NewOrderAcceptedBatch(events []OrderAcceptedEvent) OrderAcceptedBatch {
	return OrderAcceptedBatch{Events: append([]OrderAcceptedEvent(nil), events...)}
}
func (batch OrderAcceptedBatch) Len() int      { return len(batch.Events) }
func (batch OrderAcceptedBatch) IsEmpty() bool { return len(batch.Events) == 0 }
func (batch OrderAcceptedBatch) String() string {
	return fmt.Sprintf("OrderAcceptedBatch(len=%d)", len(batch.Events))
}
func (batch OrderAcceptedBatch) GoString() string {
	return fmt.Sprintf("OrderAcceptedBatch { len: %d }", len(batch.Events))
}

type OrderCanceledBatch struct{ Events []OrderCanceledEvent }

func NewOrderCanceledBatch(events []OrderCanceledEvent) OrderCanceledBatch {
	return OrderCanceledBatch{Events: append([]OrderCanceledEvent(nil), events...)}
}
func (batch OrderCanceledBatch) Len() int      { return len(batch.Events) }
func (batch OrderCanceledBatch) IsEmpty() bool { return len(batch.Events) == 0 }
func (batch OrderCanceledBatch) String() string {
	return fmt.Sprintf("OrderCanceledBatch(len=%d)", len(batch.Events))
}
func (batch OrderCanceledBatch) GoString() string {
	return fmt.Sprintf("OrderCanceledBatch { len: %d }", len(batch.Events))
}

type OrderSubmittedBatch struct{ Events []OrderSubmittedEvent }

func NewOrderSubmittedBatch(events []OrderSubmittedEvent) OrderSubmittedBatch {
	return OrderSubmittedBatch{Events: append([]OrderSubmittedEvent(nil), events...)}
}
func (batch OrderSubmittedBatch) Len() int      { return len(batch.Events) }
func (batch OrderSubmittedBatch) IsEmpty() bool { return len(batch.Events) == 0 }
func (batch OrderSubmittedBatch) String() string {
	return fmt.Sprintf("OrderSubmittedBatch(len=%d)", len(batch.Events))
}
func (batch OrderSubmittedBatch) GoString() string {
	return fmt.Sprintf("OrderSubmittedBatch { len: %d }", len(batch.Events))
}
