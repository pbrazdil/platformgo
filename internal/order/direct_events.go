package order

import "fmt"

func optionalEventID[T fmt.Stringer](value *T) string {
	if value == nil {
		return "None"
	}
	return (*value).String()
}

func (event OrderAcceptedEvent) String() string {
	return fmt.Sprintf(
		"OrderAccepted(instrument_id=%s, client_order_id=%s, venue_order_id=%s, account_id=%s, ts_event=%d)",
		event.InstrumentID, event.ClientOrderID, event.VenueOrderID, event.AccountID, event.TsEvent,
	)
}

func (event OrderCancelRejectedEvent) String() string {
	return fmt.Sprintf(
		"OrderCancelRejected(instrument_id=%s, client_order_id=%s, venue_order_id=%s, account_id=%s, reason='%s', ts_event=%d)",
		event.InstrumentID, event.ClientOrderID, optionalEventID(event.VenueOrderID),
		optionalEventID(event.AccountID), event.Reason, event.TsEvent,
	)
}

func (event OrderEmulatedEvent) String() string {
	return fmt.Sprintf(
		"OrderEmulated(instrument_id=%s, client_order_id=%s)",
		event.InstrumentID, event.ClientOrderID,
	)
}

func (event OrderExpiredEvent) String() string {
	return fmt.Sprintf(
		"OrderExpired(instrument_id=%s, client_order_id=%s, venue_order_id=%s, account_id=%s, ts_event=%d)",
		event.InstrumentID, event.ClientOrderID, optionalEventID(event.VenueOrderID),
		optionalEventID(event.AccountID), event.TsEvent,
	)
}

func (event OrderModifyRejectedEvent) String() string {
	return fmt.Sprintf(
		"OrderModifyRejected(instrument_id=%s, client_order_id=%s, venue_order_id=%s, account_id=%s, reason='%s', ts_event=%d)",
		event.InstrumentID, event.ClientOrderID, optionalEventID(event.VenueOrderID),
		optionalEventID(event.AccountID), event.Reason, event.TsEvent,
	)
}

func (event OrderPendingCancelEvent) String() string {
	return fmt.Sprintf(
		"OrderPendingCancel(instrument_id=%s, client_order_id=%s, venue_order_id=%s, account_id=%s, ts_event=%d)",
		event.InstrumentID, event.ClientOrderID, optionalEventID(event.VenueOrderID),
		optionalEventID(event.AccountID), event.TsEvent,
	)
}

func (event OrderPendingUpdateEvent) String() string {
	return fmt.Sprintf(
		"OrderPendingUpdate(instrument_id=%s, client_order_id=%s, venue_order_id=%s, account_id=%s, ts_event=%d)",
		event.InstrumentID, event.ClientOrderID, optionalEventID(event.VenueOrderID),
		optionalEventID(event.AccountID), event.TsEvent,
	)
}

func (event OrderRejectedEvent) String() string {
	return fmt.Sprintf(
		"OrderRejected(instrument_id=%s, client_order_id=%s, account_id=%s, reason='%s', due_post_only=%t, ts_event=%d)",
		event.InstrumentID, event.ClientOrderID, event.AccountID, event.Reason,
		event.DuePostOnly, event.TsEvent,
	)
}

func (event OrderReleasedEvent) String() string {
	return fmt.Sprintf(
		"OrderReleased(instrument_id=%s, client_order_id=%s, released_price=%s)",
		event.InstrumentID, event.ClientOrderID, event.ReleasedPrice.FormattedString(),
	)
}

func (event OrderSubmittedEvent) String() string {
	return fmt.Sprintf(
		"OrderSubmitted(instrument_id=%s, client_order_id=%s, account_id=%s, ts_event=%d)",
		event.InstrumentID, event.ClientOrderID, event.AccountID, event.TsEvent,
	)
}

func (event OrderTriggeredEvent) String() string {
	return fmt.Sprintf(
		"OrderTriggered(instrument_id=%s, client_order_id=%s, venue_order_id=%s, account_id=%s, ts_event=%d)",
		event.InstrumentID, event.ClientOrderID, optionalEventID(event.VenueOrderID),
		optionalEventID(event.AccountID), event.TsEvent,
	)
}

func (event OrderUpdatedEvent) String() string {
	price, triggerPrice, protectionPrice := "None", "None", "None"
	if event.Price != nil {
		price = event.Price.FormattedString()
	}
	if event.TriggerPrice != nil {
		triggerPrice = event.TriggerPrice.FormattedString()
	}
	if event.ProtectionPrice != nil {
		protectionPrice = event.ProtectionPrice.FormattedString()
	}
	return fmt.Sprintf(
		"OrderUpdated(instrument_id=%s, client_order_id=%s, venue_order_id=%s, account_id=%s, quantity=%s, price=%s, trigger_price=%s, protection_price=%s, ts_event=%d)",
		event.InstrumentID, event.ClientOrderID, optionalEventID(event.VenueOrderID),
		optionalEventID(event.AccountID), event.Quantity.FormattedString(), price,
		triggerPrice, protectionPrice, event.TsEvent,
	)
}

func (event OrderInitializedSpecEvent) String() string {
	price := "None"
	if event.Price != nil {
		price = event.Price.FormattedString()
	}
	emulationTrigger := "None"
	if event.EmulationTrigger != nil {
		emulationTrigger = event.EmulationTrigger.String()
	}
	triggerInstrumentID := optionalEventID(event.TriggerInstrumentID)
	contingencyType := "None"
	if event.ContingencyType != nil {
		contingencyType = event.ContingencyType.String()
	}
	orderListID := optionalEventID(event.OrderListID)
	linkedOrderIDs := "None"
	if event.LinkedOrderIDs != nil {
		linkedOrderIDs = fmt.Sprint(event.LinkedOrderIDs)
	}
	execAlgorithmParams := "None"
	if event.ExecAlgorithmParams != nil {
		execAlgorithmParams = fmt.Sprint(event.ExecAlgorithmParams)
	}
	tags := "None"
	if event.Tags != nil {
		tags = fmt.Sprint(event.Tags)
	}
	return fmt.Sprintf(
		"OrderInitialized(instrument_id=%s, client_order_id=%s, side=%s, type=%s, quantity=%s, time_in_force=%s, post_only=%t, reduce_only=%t, quote_quantity=%t, price=%s, emulation_trigger=%s, trigger_instrument_id=%s, contingency_type=%s, order_list_id=%s, linked_order_ids=%s, parent_order_id=%s, exec_algorithm_id=%s, exec_algorithm_params=%s, exec_spawn_id=%s, tags=%s)",
		event.InstrumentID, event.ClientOrderID, event.OrderSide, event.OrderType,
		event.Quantity.FormattedString(), event.TimeInForce, event.PostOnly, event.ReduceOnly,
		event.QuoteQuantity, price, emulationTrigger, triggerInstrumentID, contingencyType,
		orderListID, linkedOrderIDs, optionalEventID(event.ParentOrderID),
		optionalEventID(event.ExecAlgorithmID), execAlgorithmParams,
		optionalEventID(event.ExecSpawnID), tags,
	)
}

// OrderDeniedDirectEvent adds the optional causation ID carried by the direct
// event without changing the already accepted spec event.
type OrderDeniedDirectEvent struct {
	OrderDeniedEvent
	CausationID *string `json:"causation_id,omitempty"`
}

func (event OrderDeniedDirectEvent) String() string {
	return fmt.Sprintf(
		"OrderDenied(instrument_id=%s, client_order_id=%s, reason='%s')",
		event.InstrumentID, event.ClientOrderID, event.Reason,
	)
}

// OrderEventAny is the direct-event polymorphic surface needed by the native
// filled conversion path.
type OrderEventAny struct {
	filled   *OrderFilledEvent
	accepted *OrderAcceptedEvent
}

func AnyFilled(event OrderFilledEvent) OrderEventAny {
	return OrderEventAny{filled: &event}
}

func AnyAccepted(event OrderAcceptedEvent) OrderEventAny {
	return OrderEventAny{accepted: &event}
}

func (event OrderEventAny) IntoFilled() OrderFilledEvent {
	if event.filled == nil {
		panic("Invalid `OrderEventAny` not `OrderFilled`")
	}
	return *event.filled
}

func (event OrderEventAny) String() string {
	if event.filled != nil {
		return event.filled.String()
	}
	if event.accepted != nil {
		return event.accepted.String()
	}
	return ""
}
