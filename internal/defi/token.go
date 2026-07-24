package defi

import "fmt"

type Token struct {
	Chain    Chain  `json:"chain"`
	Address  string `json:"address"`
	Name     string `json:"name"`
	Symbol   string `json:"symbol"`
	Decimals uint8  `json:"decimals"`
}

func NewToken(chain Chain, address, name, symbol string, decimals uint8) Token {
	return Token{Chain: chain, Address: address, Name: name, Symbol: symbol, Decimals: decimals}
}

func (t Token) IsStablecoin() bool {
	switch t.Symbol {
	case "USDC", "USDT", "DAI", "BUSD", "FRAX", "LUSD", "TUSD", "USDP", "GUSD",
		"SUSD", "UST", "USDD", "CUSD", "EUROC", "EURT", "EURS", "AGEUR", "MIM",
		"FEI", "OUSD", "USDB":
		return true
	default:
		return false
	}
}

func (t Token) IsNativeCurrency() bool {
	switch t.Symbol {
	case "WETH", "ETH", "WMATIC", "MATIC", "WBNB", "BNB", "WAVAX", "AVAX", "WFTM", "FTM":
		return true
	default:
		return false
	}
}

func (t Token) Priority() uint8 {
	if t.IsStablecoin() {
		return 1
	}
	if t.IsNativeCurrency() {
		return 2
	}
	return 3
}

func (t Token) String() string {
	return fmt.Sprintf("Token(symbol=%s, name=%s)", t.Symbol, t.Name)
}
