# Pinned Nautilus execution-document fragment

Source: `nautechsystems/nautilus_trader@116c9b5159ebeb6b578b737d72298cac8d723723`
`docs/concepts/execution.md`

This checked-in fragment keeps the native Go test source-free in CI while
preserving the upstream generated-table contract.

<!-- BEGIN GENERATED: order-denied-reasons -->

| Code                                  | Description                                                                       |
| ------------------------------------- | --------------------------------------------------------------------------------- |
| `CLIENT_VENUE_MISMATCH`               | The execution client does not handle the order venue.                             |
| `CUM_MARGIN_EXCEEDS_FREE_BALANCE`     | The cumulative initial margin exceeds the account free balance.                   |
| `CUM_NOTIONAL_EXCEEDS_FREE_BALANCE`   | The cumulative order notional exceeds the account free balance.                   |
| `EXPIRE_TIME_IN_PAST`                 | The order's expire time is in the past.                                           |
| `INSTRUMENT_NOT_FOUND`                | The instrument was not found in the cache.                                        |
| `INVALID_CLIENT_ORDER_ID`             | The client order ID is invalid for the venue.                                     |
| `INVALID_MAX_NOTIONAL_PER_ORDER`      | The configured maximum notional per order is invalid.                             |
| `INVALID_ORDER_SIDE`                  | The order side is invalid for this operation.                                     |
| `INVALID_POSITION_ID`                 | The supplied position ID is invalid for the order submission.                     |
| `MARGIN_EXCEEDS_FREE_BALANCE`         | The order initial margin exceeds the account free balance.                        |
| `MISSING_EXPIRE_TIME`                 | A GTD order is missing its expire time.                                           |
| `MISSING_TRAILING_OFFSET`             | The order is missing a required trailing offset.                                  |
| `MISSING_TRAILING_OFFSET_TYPE`        | The order is missing a required trailing offset type.                             |
| `MISSING_TRIGGER_TYPE`                | The order is missing a required trigger type.                                     |
| `NOTIONAL_BELOW_MINIMUM`              | The order notional is below the instrument minimum.                               |
| `NOTIONAL_EXCEEDS_FREE_BALANCE`       | The order notional exceeds the account free balance.                              |
| `NOTIONAL_EXCEEDS_MAXIMUM`            | The order notional exceeds the instrument maximum.                                |
| `NOTIONAL_EXCEEDS_MAX_PER_ORDER`      | The order notional exceeds the configured maximum per order.                      |
| `NO_EXECUTION_CLIENT`                 | No execution client was found for the routed command.                             |
| `ORDER_LIST_DENIED`                   | The order was denied because its order list failed risk checks.                   |
| `ORDER_LIST_INCOMPLETE`               | The order list is missing orders in the cache.                                    |
| `POSITION_NOT_FOUND`                  | The position for a reduce‑only order was not found.                               |
| `QUANTITY_BELOW_MINIMUM`              | The effective order quantity is below the instrument minimum.                     |
| `QUANTITY_CONVERSION_FAILED`          | The order quantity could not be converted for risk checks.                        |
| `QUANTITY_EXCEEDS_MAXIMUM`            | The effective order quantity exceeds the instrument maximum.                      |
| `RATE_LIMIT_EXCEEDED`                 | The order submission rate limit was exceeded.                                     |
| `REDUCE_ONLY_WOULD_INCREASE_POSITION` | A reduce‑only order would increase the position.                                  |
| `STREAM_RECONCILING`                  | A post‑reconnect stream reconciliation is in progress; retry once it completes.   |
| `SUBMIT_FAILED`                       | Submitting the order to the execution client failed.                              |
| `TRADING_HALTED`                      | Trading is halted; new orders are denied.                                         |
| `TRADING_STATE_REDUCING`              | Trading is reducing; the order would increase exposure.                           |
| `TRAILING_STOP_CALC_FAILED`           | The trailing stop trigger price could not be calculated.                          |
| `UNSUPPORTED_ORDER_LIST`              | The venue does not support the requested order list.                              |
| `UNSUPPORTED_ORDER_TYPE`              | The order type is not supported by the venue.                                     |
| `UNSUPPORTED_TIME_IN_FORCE`           | The order's time in force is not supported.                                       |
| `UNSUPPORTED_TP_SL`                   | The venue does not support the requested take‑profit/stop‑loss parameters.        |
| `UNSUPPORTED_TRAILING_OFFSET_TYPE`    | The order's trailing offset type is not supported.                                |
| `VALIDATION_FAILED`                   | The order failed adapter validation before submission.                            |

<!-- END GENERATED: order-denied-reasons -->
