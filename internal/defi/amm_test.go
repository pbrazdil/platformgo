package defi

import "testing"

func uint32Pointer(value uint32) *uint32 { return &value }

func poolFixture(t *testing.T) Pool {
	t.Helper()
	chain, _ := ChainFromName("Ethereum")
	dex, err := NewDex(chain, UniswapV3, "0x1F98431c8aD98523631AE4a59f267346ea31F984")
	if err != nil {
		t.Fatal(err)
	}
	token0 := NewToken(chain, "0xA0b86a33E6441b936662bb6B5d1F8Fb0E2b57A5D", "Wrapped Ether", "WETH", 18)
	token1 := NewToken(chain, "0xdAC17F958D2ee523a2206206994597C13D831ec7", "Tether USD", "USDT", 6)
	identifier, err := PoolIdentifierFromAddress("0x11b815efB8f581194ae79006d24E0d814B7697F6")
	if err != nil {
		t.Fatal(err)
	}
	pool, err := NewPool(
		chain, dex, identifier.String(), identifier, 12_345_678, token0, token1,
		uint32Pointer(3000), uint32Pointer(60), 1_234_567_890_000_000_000,
	)
	if err != nil {
		t.Fatal(err)
	}
	return pool
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/amm.rs:285
//	test: test_pool_constructor_and_methods
func TestPoolConstructorAndMethods(t *testing.T) {
	pool := poolFixture(t)
	if pool.Chain.ChainID != 1 || pool.Dex.Name != UniswapV3 ||
		pool.Address != "0x11b815efB8f581194ae79006d24E0d814B7697F6" ||
		pool.CreationBlock != 12_345_678 || pool.Token0.Symbol != "WETH" ||
		pool.Token1.Symbol != "USDT" || pool.Fee == nil || *pool.Fee != 3000 ||
		pool.TickSpacing == nil || *pool.TickSpacing != 60 ||
		pool.TsInit != 1_234_567_890_000_000_000 {
		t.Fatalf("pool = %#v", pool)
	}
	if pool.InstrumentID.Symbol != "0x11b815efB8f581194ae79006d24E0d814B7697F6" ||
		pool.InstrumentID.Venue != "Ethereum:UniswapV3" {
		t.Fatalf("instrument = %s", pool.InstrumentID)
	}
	if pool.BaseToken().Symbol != "WETH" || pool.QuoteToken().Symbol != "USDT" ||
		pool.IsBaseQuoteInverted() {
		t.Fatalf("base/quote = %s/%s inverted=%v",
			pool.BaseToken().Symbol, pool.QuoteToken().Symbol, pool.IsBaseQuoteInverted())
	}
	if got := pool.FullSpecification(); got != "WETH/USDT-3000.Ethereum:UniswapV3" {
		t.Fatalf("specification = %q", got)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/amm.rs:364
//	test: test_pool_instrument_id_format
func TestPoolInstrumentIDFormat(t *testing.T) {
	if got := poolFixture(t).InstrumentID.String(); got !=
		"0x11b815efB8f581194ae79006d24E0d814B7697F6.Ethereum:UniswapV3" {
		t.Fatalf("instrument ID = %q", got)
	}
}
