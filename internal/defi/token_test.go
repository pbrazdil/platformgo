package defi

import "testing"

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/token.rs:158
//	test: test_token_constructor
func TestTokenConstructor(t *testing.T) {
	chain, _ := ChainFromName("Arbitrum")
	token := NewToken(chain, "0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2", "Wrapped Ether", "WETH", 18)
	if token.Chain.ChainID != 42161 || token.Name != "Wrapped Ether" ||
		token.Symbol != "WETH" || token.Decimals != 18 || !token.IsNativeCurrency() {
		t.Fatalf("token = %#v", token)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/token.rs:167
//	test: test_token_display_with_special_characters
func TestTokenDisplayWithSpecialCharacters(t *testing.T) {
	chain, _ := ChainFromName("Ethereum")
	token := NewToken(chain, "0xA0b86a33E6441b936662bb6B5d1F8Fb0E2b57A5D",
		"Test Token (with parentheses)", "TEST-1", 18)
	if got := token.String(); got != "Token(symbol=TEST-1, name=Test Token (with parentheses))" {
		t.Fatalf("display = %q", got)
	}
	if token.IsNativeCurrency() || token.IsStablecoin() || token.Priority() != 3 {
		t.Fatalf("classification = native:%v stable:%v priority:%d",
			token.IsNativeCurrency(), token.IsStablecoin(), token.Priority())
	}
}
