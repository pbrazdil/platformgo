package orderbook

import (
	"math"
	"math/big"
	"slices"
	"strings"
	"testing"

	"github.com/upcomers-org/platformgo/internal/decimal"
)

func price(text string) decimal.Price       { return decimal.MustPrice(text) }
func quantity(text string) decimal.Quantity { return decimal.MustQuantity(text) }

func order(side Side, p, q string, id uint64) Order {
	return NewOrder(side, price(p), quantity(q), id)
}

func requireDecimal(t *testing.T, got decimal.Decimal, want string) {
	t.Helper()
	expected := decimal.MustParse(want)
	if !got.Equal(expected) {
		t.Fatalf("got %s, want %s", got, expected)
	}
}

func requirePrice(t *testing.T, got decimal.Price, want string) {
	t.Helper()
	if !got.Equal(price(want)) {
		t.Fatalf("got price %s, want %s", got, want)
	}
}

func requireQuantity(t *testing.T, got decimal.Quantity, want string) {
	t.Helper()
	if !got.Equal(quantity(want)) {
		t.Fatalf("got quantity %s, want %s", got, want)
	}
}

func requirePanicContains(t *testing.T, text string, fn func()) {
	t.Helper()
	defer func() {
		recovered := recover()
		if recovered == nil || !strings.Contains(recovered.(string), text) {
			t.Fatalf("panic = %v, want containing %q", recovered, text)
		}
	}()
	fn()
}

// Ported from nautilus-trader@116c9b5159ebeb6b578b737d72298cac8d723723
// source: crates/model/src/orderbook/level.rs:304 test_empty_level
func checkLevelEmpty(t *testing.T) {
	level := NewLevel(NewBookPrice(price("1.00"), Buy))
	if _, ok := level.First(); ok {
		t.Fatal("empty level has a first order")
	}
	if level.Side() != Buy {
		t.Fatalf("side = %s, want BUY", level.Side())
	}
}

// Ported from nautilus-trader@116c9b5159ebeb6b578b737d72298cac8d723723
// source: crates/model/src/orderbook/level.rs:311 test_level_from_order
func checkLevelFromOrder(t *testing.T) {
	input := order(Buy, "1.00", "10", 1)
	level := LevelFromOrder(input)
	requirePrice(t, level.Price.Value, "1.00")
	if level.Price.Side != Buy || level.Len() != 1 {
		t.Fatalf("side/length = %s/%d", level.Price.Side, level.Len())
	}
	first, ok := level.First()
	if !ok || first.ID != input.ID {
		t.Fatalf("first = %+v, %v", first, ok)
	}
	requireDecimal(t, level.Size(), "10")
}

// Ported from nautilus-trader@116c9b5159ebeb6b578b737d72298cac8d723723
// sources:
//
//	crates/model/src/orderbook/level.rs:324 test_add_order_incorrect_price_level
//	crates/model/src/orderbook/level.rs:334 test_add_bulk_orders_incorrect_price
//	crates/model/src/orderbook/level.rs:534 test_update_order_incorrect_price
func checkLevelRejectsIncorrectPrice(t *testing.T) {
	t.Run("add", func(t *testing.T) {
		level := NewLevel(NewBookPrice(price("1.00"), Buy))
		requirePanicContains(t, "does not match", func() { level.Add(order(Buy, "2.00", "10", 1)) })
	})
	t.Run("bulk", func(t *testing.T) {
		level := NewLevel(NewBookPrice(price("1.00"), Buy))
		requirePanicContains(t, "does not match", func() {
			level.AddBulk([]Order{order(Buy, "1.00", "10", 1), order(Buy, "2.00", "20", 2)})
		})
	})
	t.Run("update", func(t *testing.T) {
		level := LevelFromOrder(order(Buy, "1.00", "10", 1))
		requirePanicContains(t, "does not match", func() { level.Update(order(Buy, "2.00", "20", 1)) })
	})
}

// Ported from nautilus-trader@116c9b5159ebeb6b578b737d72298cac8d723723
// source: crates/model/src/orderbook/level.rs:345 test_add_bulk_empty
func checkLevelAddBulkEmpty(t *testing.T) {
	level := NewLevel(NewBookPrice(price("1.00"), Buy))
	level.AddBulk(nil)
	if !level.Empty() {
		t.Fatal("empty bulk changed the level")
	}
}

// Ported from nautilus-trader@116c9b5159ebeb6b578b737d72298cac8d723723
// sources:
//
//	crates/model/src/orderbook/level.rs:353 test_comparisons_bid_side
//	crates/model/src/orderbook/level.rs:361 test_comparisons_ask_side
//	crates/model/src/orderbook/level.rs:375 test_book_level_sorting
func checkLevelComparisonAndSorting(t *testing.T) {
	bid0 := NewLevel(NewBookPrice(price("1.00"), Buy))
	bid1 := NewLevel(NewBookPrice(price("1.01"), Buy))
	if bid0.Price.Compare(bid0.Price) != 0 || bid0.Price.Compare(bid1.Price) <= 0 {
		t.Fatal("bid price ordering is not descending")
	}
	ask0 := NewLevel(NewBookPrice(price("1.00"), Sell))
	ask1 := NewLevel(NewBookPrice(price("1.01"), Sell))
	if ask0.Price.Compare(ask0.Price) != 0 || ask0.Price.Compare(ask1.Price) >= 0 {
		t.Fatal("ask price ordering is not ascending")
	}
	levels := []*Level{
		NewLevel(NewBookPrice(price("1.00"), Sell)),
		NewLevel(NewBookPrice(price("1.02"), Sell)),
		NewLevel(NewBookPrice(price("1.01"), Sell)),
	}
	slices.SortFunc(levels, func(a, b *Level) int { return a.Price.Compare(b.Price) })
	for i, want := range []string{"1.00", "1.01", "1.02"} {
		requirePrice(t, levels[i].Price.Value, want)
	}
}

// Ported from nautilus-trader@116c9b5159ebeb6b578b737d72298cac8d723723
// sources:
//
//	crates/model/src/orderbook/level.rs:397 test_add_single_order
//	crates/model/src/orderbook/level.rs:410 test_add_multiple_orders
//	crates/model/src/orderbook/level.rs:425 test_get_orders
//	crates/model/src/orderbook/level.rs:441 test_iter_returns_fifo
func checkLevelAddAndFIFO(t *testing.T) {
	level := NewLevel(NewBookPrice(price("2.00"), Buy))
	first := order(Buy, "2.00", "10", 0)
	second := order(Buy, "2.00", "20", 1)
	level.Add(first)
	if level.Empty() || level.Len() != 1 {
		t.Fatalf("after one add: empty=%v len=%d", level.Empty(), level.Len())
	}
	gotFirst, ok := level.First()
	if !ok || gotFirst.ID != first.ID {
		t.Fatalf("first = %+v, %v", gotFirst, ok)
	}
	level.Add(second)
	if level.Len() != 2 {
		t.Fatalf("len = %d, want 2", level.Len())
	}
	requireDecimal(t, level.Size(), "30")
	requireDecimal(t, level.Exposure(), "60")
	orders := level.Orders()
	if len(orders) != 2 || orders[0].ID != first.ID || orders[1].ID != second.ID {
		t.Fatalf("FIFO orders = %+v", orders)
	}
}

// Ported from nautilus-trader@116c9b5159ebeb6b578b737d72298cac8d723723
// sources:
//
//	crates/model/src/orderbook/level.rs:454 test_update_order
//	crates/model/src/orderbook/level.rs:468 test_update_inserts_if_missing
//	crates/model/src/orderbook/level.rs:478 test_update_zero_size_nonexistent
//	crates/model/src/orderbook/level.rs:487 test_fifo_order_after_updates
//	crates/model/src/orderbook/level.rs:509 test_insertion_order_after_mixed_operations
//	crates/model/src/orderbook/level.rs:550 test_update_order_with_zero_size
func checkLevelUpdateSemantics(t *testing.T) {
	t.Run("replace", func(t *testing.T) {
		level := LevelFromOrder(order(Buy, "1.00", "10", 0))
		level.Update(order(Buy, "1.00", "20", 0))
		if level.Len() != 1 {
			t.Fatalf("len = %d", level.Len())
		}
		requireDecimal(t, level.Size(), "20")
		requireDecimal(t, level.Exposure(), "20")
	})
	t.Run("upsert and zero", func(t *testing.T) {
		level := NewLevel(NewBookPrice(price("1.00"), Buy))
		inserted := order(Buy, "1.00", "10", 1)
		level.Update(inserted)
		first, ok := level.First()
		if !ok || first.ID != inserted.ID {
			t.Fatalf("first = %+v, %v", first, ok)
		}
		level.Update(order(Buy, "1.00", "0", 99))
		if level.Len() != 1 {
			t.Fatalf("zero nonexistent changed len to %d", level.Len())
		}
		level.Update(order(Buy, "1.00", "0", 1))
		if !level.Empty() {
			t.Fatal("zero update did not delete")
		}
		requireDecimal(t, level.Size(), "0")
		requireDecimal(t, level.Exposure(), "0")
	})
	t.Run("preserves position", func(t *testing.T) {
		level := NewLevel(NewBookPrice(price("1.00"), Buy))
		level.AddBulk([]Order{
			order(Buy, "1.00", "10", 1),
			order(Buy, "1.00", "20", 2),
			order(Buy, "1.00", "30", 3),
		})
		level.Update(order(Buy, "1.00", "25", 2))
		got := level.Orders()
		if got[0].ID != 1 || got[1].ID != 2 || got[2].ID != 3 {
			t.Fatalf("order after update = %+v", got)
		}
		requireQuantity(t, got[1].Quantity, "25")
		level.Delete(got[0])
		got = level.Orders()
		if len(got) != 2 || got[0].ID != 2 || got[1].ID != 3 {
			t.Fatalf("order after mixed operations = %+v", got)
		}
	})
}

// Ported from nautilus-trader@116c9b5159ebeb6b578b737d72298cac8d723723
// sources:
//
//	crates/model/src/orderbook/level.rs:564 test_delete_nonexistent_order
//	crates/model/src/orderbook/level.rs:573 test_delete_order
//	crates/model/src/orderbook/level.rs:601 test_remove_order_by_id
//	crates/model/src/orderbook/level.rs:675 test_remove_nonexistent_order
func checkLevelDeleteAndRemove(t *testing.T) {
	level := NewLevel(NewBookPrice(price("1.00"), Buy))
	level.Delete(order(Buy, "1.00", "10", 99))
	if level.Len() != 0 {
		t.Fatalf("len = %d", level.Len())
	}
	level.AddBulk([]Order{order(Buy, "1.00", "10", 0), order(Buy, "1.00", "20", 1)})
	level.Delete(order(Buy, "1.00", "999", 0))
	if level.Len() != 1 || !level.Contains(1) {
		t.Fatalf("delete left level %+v", level.Orders())
	}
	requireDecimal(t, level.Size(), "20")
	requireDecimal(t, level.Exposure(), "20")
	level.Add(order(Buy, "1.00", "10", 0))
	level.RemoveByID(1, 0, 0)
	if level.Len() != 1 || !level.Contains(0) {
		t.Fatalf("remove left level %+v", level.Orders())
	}
	requirePanicContains(t, "order_id=7, sequence=8, ts_event=9", func() {
		level.RemoveByID(7, 8, 9)
	})
}

// Ported from nautilus-trader@116c9b5159ebeb6b578b737d72298cac8d723723
// sources:
//
//	crates/model/src/orderbook/level.rs:629 test_add_bulk_orders
//	crates/model/src/orderbook/level.rs:655 test_maximum_order_id
//	crates/model/src/orderbook/level.rs:682 test_size
//	crates/model/src/orderbook/level.rs:694 test_size_raw
//	crates/model/src/orderbook/level.rs:709 test_size_decimal
//	crates/model/src/orderbook/level.rs:721 test_exposure
func checkLevelBulkSizeAndExposure(t *testing.T) {
	level := NewLevel(NewBookPrice(price("2.00"), Buy))
	level.AddBulk([]Order{
		order(Buy, "2.00", "10", math.MaxUint64),
		order(Buy, "2.00", "20", 1),
	})
	if level.Len() != 2 {
		t.Fatalf("len = %d", level.Len())
	}
	first, ok := level.First()
	if !ok || first.ID != math.MaxUint64 {
		t.Fatalf("first = %+v", first)
	}
	requireDecimal(t, level.Size(), "30")
	requireDecimal(t, level.Exposure(), "60")
	wantRaw, _ := new(big.Int).SetString("300000000000000000", 10)
	if level.SizeRaw().Cmp(wantRaw) != 0 {
		t.Fatalf("raw size = %s, want %s", level.SizeRaw(), wantRaw)
	}
}

// Ported from nautilus-trader@116c9b5159ebeb6b578b737d72298cac8d723723
// sources:
//
//	crates/model/src/orderbook/level.rs:736 test_exposure_raw_exact_whole
//	crates/model/src/orderbook/level.rs:757 test_exposure_raw_truncates_sub_raw_unit
//	crates/model/src/orderbook/level.rs:768 test_exposure_raw_accumulates_exactly
//	crates/model/src/orderbook/level.rs:792 test_exposure_raw_avoids_phantom_overflow
//	crates/model/src/orderbook/level.rs:810 test_exposure_raw_saturates_single_order
//	crates/model/src/orderbook/level.rs:833 test_exposure_raw_accumulation_saturates
//
// Adaptation: native Go exact Decimal values replace Rust raw integer constructors.
func checkLevelExposureRaw(t *testing.T) {
	cases := []struct {
		name, p, q, want string
	}{
		{"negative", "-2", "10", "0"},
		{"zero", "0", "1", "0"},
		{"whole", "2", "10", "200000000000000000"},
		{"sub raw truncation", "1.0000000000000001", "1.0000000000000001", "10000000000000002"},
		{"non overflow intermediate", "100000", "100", "100000000000000000000000"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			level := LevelFromOrder(order(Buy, tc.p, tc.q, 0))
			want, _ := new(big.Int).SetString(tc.want, 10)
			if got := level.ExposureRaw(); got.Cmp(want) != 0 {
				t.Fatalf("raw exposure = %s, want %s", got, want)
			}
		})
	}
	level := NewLevel(NewBookPrice(price("2.00"), Buy))
	level.AddBulk([]Order{order(Buy, "2.00", "10", 0), order(Buy, "2.00", "20", 1)})
	want, _ := new(big.Int).SetString("600000000000000000", 10)
	if level.ExposureRaw().Cmp(want) != 0 {
		t.Fatalf("accumulated raw = %s", level.ExposureRaw())
	}

	saturated := LevelFromOrder(order(Buy, "1000000000000.00", "1000000000000.00", 0))
	if saturated.ExposureRaw().Cmp(maxRawQuantity) != 0 {
		t.Fatalf("single order did not saturate: %s", saturated.ExposureRaw())
	}
	saturated = NewLevel(NewBookPrice(price("100000000000.0"), Buy))
	saturated.Add(order(Buy, "100000000000.0", "200000000000.0", 0))
	if saturated.ExposureRaw().Cmp(maxRawQuantity) >= 0 {
		t.Fatalf("first order unexpectedly saturated: %s", saturated.ExposureRaw())
	}
	saturated.Add(order(Buy, "100000000000.0", "200000000000.0", 1))
	if saturated.ExposureRaw().Cmp(maxRawQuantity) != 0 {
		t.Fatalf("accumulation did not saturate: %s", saturated.ExposureRaw())
	}
}

// Ported from nautilus-trader@116c9b5159ebeb6b578b737d72298cac8d723723
// sources:
//
//	crates/model/src/orderbook/level.rs:781 test_exposure_raw_preserves_non_saturating_raw_units
//	crates/model/src/orderbook/level.rs:867 test_exposure_raw_preserves_native_defi_scales
//	crates/model/src/orderbook/level.rs:888 test_exposure_raw_preserves_mixed_defi_scales
//
// Adaptations:
//   - Go has one fixed precision, so source feature-specific raw constructors
//     are represented by their exact economic decimal values.
//   - Expected raw values use the Go model's 10^16 fixed scalar.
func checkLevelExposureRawPreservesExactEconomicValueAcrossSourceScales(t *testing.T) {
	level := LevelFromOrder(order(Buy, "9007199253.999999999", "2.000000001", 0))
	requireDecimal(t, level.Exposure(), "18014398517.007199251999999999")
	want, _ := new(big.Int).SetString("180143985170071992519999999", 10)
	if got := level.ExposureRaw(); got.Cmp(want) != 0 {
		t.Fatalf("non-saturating raw exposure = %s, want %s", got, want)
	}

	for _, tc := range []struct {
		name, p, q string
	}{
		{"native", "1.25", "2.4"},
		{"native price mixed", "12.5", "0.24"},
		{"native size mixed", "0.125", "24"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			level := LevelFromOrder(order(Buy, tc.p, tc.q, 0))
			requireDecimal(t, level.Exposure(), "3")
			want := new(big.Int).Mul(big.NewInt(3), new(big.Int).Exp(big.NewInt(10), big.NewInt(16), nil))
			if got := level.ExposureRaw(); got.Cmp(want) != 0 {
				t.Fatalf("mixed-scale raw exposure = %s, want %s", got, want)
			}
		})
	}
}
