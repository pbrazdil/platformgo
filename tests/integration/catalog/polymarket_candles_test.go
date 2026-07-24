package catalog

import "testing"

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/catalog/e2e_polymarket_candles.rs:31
//	test: polymarket_candle_history_maps_price_points_to_candles
func TestPolymarketCandleHistoryMapsPricePointsToCandles(t *testing.T) {
	candles := polymarketCandles()
	if len(candles) != 3 {
		t.Fatalf("candle count = %d, want 3", len(candles))
	}
	first := candles[0]
	if first.Open != "0.61" || first.High != "0.61" ||
		first.Low != "0.61" || first.Close != "0.61" {
		t.Fatalf("first candle = %#v", first)
	}
	if candles[1].Close != "0.62" || candles[2].Close != "0.6" {
		t.Fatalf("later closes = %q/%q", candles[1].Close, candles[2].Close)
	}
	if first.OpenTime != 1_700_000_000_000 ||
		first.CloseTime-first.OpenTime != 3_600_000 ||
		first.Volume != "0" || first.NumTrades != 0 {
		t.Fatalf("first candle timing/volume = %#v", first)
	}
}
