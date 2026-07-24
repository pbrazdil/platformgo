package defi

import (
	"math/big"
	"strings"
	"testing"

	"github.com/upcomers-org/platformgo/internal/currency"
	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/money"
)

func walletToken(symbol string, decimals uint8) Token {
	chain, _ := ChainFromName("Ethereum")
	return NewToken(chain, "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48", symbol+" Token", symbol, decimals)
}

func bigDecimal(value string) *big.Int {
	result, ok := new(big.Int).SetString(value, 10)
	if !ok {
		panic("invalid test integer")
	}
	return result
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/wallet.rs:156
//	test: test_token_balance_as_quantity_18_decimals
func TestTokenBalanceAsQuantity18Decimals(t *testing.T) {
	balance := NewTokenBalance(bigDecimal("10342000000000000000000"), walletToken("NU", 18))
	quantity, err := balance.Quantity()
	if err != nil || quantity.Decimal().String() != "10342.000000000000000000" {
		t.Fatalf("quantity = %s, %v", quantity.Decimal(), err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/wallet.rs:178
//	test: test_token_balance_as_quantity_6_decimals
func TestTokenBalanceAsQuantity6Decimals(t *testing.T) {
	balance := NewTokenBalance(big.NewInt(92_220_728_254), walletToken("USDC", 6))
	quantity, err := balance.Quantity()
	if err != nil || quantity.Decimal().String() != "92220.728254" {
		t.Fatalf("quantity = %s, %v", quantity.Decimal(), err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/wallet.rs:191
//	test: test_token_balance_as_quantity_fractional_18_decimals
func TestTokenBalanceAsQuantityFractional18Decimals(t *testing.T) {
	balance := NewTokenBalance(big.NewInt(758_325_512_078_001_391), walletToken("mETH", 18))
	quantity, err := balance.Quantity()
	if err != nil || quantity.Decimal().String() != "0.758325512078001391" {
		t.Fatalf("quantity = %s, %v", quantity.Decimal(), err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/wallet.rs:210
//	test: test_token_balance_display_18_decimals
func TestTokenBalanceDisplay18Decimals(t *testing.T) {
	balance := NewTokenBalance(bigDecimal("7922013795343949480329"), walletToken("ARB", 18))
	display := balance.String()
	if !strings.Contains(display, "ARB") || !strings.Contains(display, "7922.013795343949480329") {
		t.Fatalf("display = %q", display)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/wallet.rs:229
//	test: test_token_balance_display_6_decimals
func TestTokenBalanceDisplay6Decimals(t *testing.T) {
	display := NewTokenBalance(big.NewInt(92_220_728_254), walletToken("USDC", 6)).String()
	if !strings.Contains(display, "USDC") || !strings.Contains(display, "92220.728254") {
		t.Fatalf("display = %q", display)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/wallet.rs:241
//	test: test_token_balance_set_amount_usd
func TestTokenBalanceSetAmountUSD(t *testing.T) {
	balance := NewTokenBalance(bigDecimal("1000000000000000000"), walletToken("WETH", 18))
	if balance.AmountUSD != nil {
		t.Fatal("new balance has USD amount")
	}
	usd := decimal.MustQuantity("3500.00")
	balance.SetAmountUSD(usd)
	if balance.AmountUSD == nil || balance.AmountUSD.Decimal().String() != "3500.00" {
		t.Fatalf("USD amount = %v", balance.AmountUSD)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/wallet.rs:258
//	test: test_wallet_balance_new_empty
func TestWalletBalanceNewEmpty(t *testing.T) {
	wallet := NewWalletBalance(nil)
	if wallet.NativeCurrency != nil || len(wallet.TokenBalances) != 0 || wallet.IsTokenUniverseInitialized() {
		t.Fatalf("wallet = %#v", wallet)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/wallet.rs:267
//	test: test_wallet_balance_with_token_universe
func TestWalletBalanceWithTokenUniverse(t *testing.T) {
	wallet := NewWalletBalance(map[string]struct{}{
		"0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48": {},
		"0x912CE59144191C1204E64559FE8253a0e49E6548": {},
	})
	if !wallet.IsTokenUniverseInitialized() || len(wallet.TokenUniverse) != 2 {
		t.Fatalf("universe = %#v", wallet.TokenUniverse)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/wallet.rs:279
//	test: test_wallet_balance_set_native_currency
//
// Adaptations:
//   - Native balance enters as an exact decimal string.
func TestWalletBalanceSetNativeCurrency(t *testing.T) {
	wallet := NewWalletBalance(nil)
	eth := currency.MustNew("ETH", 8, 0, "Ethereum", currency.Crypto)
	balance := money.MustNew("50.936054", eth)
	wallet.SetNativeCurrencyBalance(balance)
	if wallet.NativeCurrency == nil || !wallet.NativeCurrency.Equal(balance) {
		t.Fatalf("native balance = %v", wallet.NativeCurrency)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/wallet.rs:291
//	test: test_wallet_balance_add_token_balance
func TestWalletBalanceAddTokenBalance(t *testing.T) {
	wallet := NewWalletBalance(nil)
	wallet.AddTokenBalance(NewTokenBalance(big.NewInt(100_000_000), walletToken("USDC", 6)))
	wallet.AddTokenBalance(NewTokenBalance(bigDecimal("1000000000000000000"), walletToken("WETH", 18)))
	if len(wallet.TokenBalances) != 2 ||
		wallet.TokenBalances[0].Token.Symbol != "USDC" ||
		wallet.TokenBalances[1].Token.Symbol != "WETH" {
		t.Fatalf("balances = %#v", wallet.TokenBalances)
	}
}
