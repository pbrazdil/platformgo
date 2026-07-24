package defi

import (
	"fmt"
	"math/big"

	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/money"
)

type TokenBalance struct {
	Amount    *big.Int
	AmountUSD *decimal.Quantity
	Token     Token
}

func NewTokenBalance(amount *big.Int, token Token) TokenBalance {
	if amount == nil {
		amount = new(big.Int)
	}
	return TokenBalance{Amount: new(big.Int).Set(amount), Token: token}
}

func (b TokenBalance) Quantity() (decimal.Quantity, error) {
	return decimal.QuantityFromU256(b.Amount, b.Token.Decimals)
}

func (b *TokenBalance) SetAmountUSD(amount decimal.Quantity) {
	copy := amount
	b.AmountUSD = &copy
}

func (b TokenBalance) String() string {
	quantity, err := b.Quantity()
	if err != nil {
		quantity = decimal.Quantity{}
	}
	if b.AmountUSD == nil {
		return fmt.Sprintf("TokenBalance(token=%s, amount=%s)", b.Token.Symbol, quantity.Decimal())
	}
	usd := b.AmountUSD.Decimal().Quantize(2, decimal.RoundHalfEven)
	return fmt.Sprintf("TokenBalance(token=%s, amount=%s, usd=$%s)", b.Token.Symbol, quantity.Decimal(), usd)
}

type WalletBalance struct {
	NativeCurrency *money.Money
	TokenBalances  []TokenBalance
	TokenUniverse  map[string]struct{}
}

func NewWalletBalance(tokenUniverse map[string]struct{}) *WalletBalance {
	copy := make(map[string]struct{}, len(tokenUniverse))
	for address := range tokenUniverse {
		copy[address] = struct{}{}
	}
	return &WalletBalance{TokenUniverse: copy}
}

func (w *WalletBalance) IsTokenUniverseInitialized() bool { return len(w.TokenUniverse) != 0 }

func (w *WalletBalance) SetNativeCurrencyBalance(balance money.Money) {
	copy := balance
	w.NativeCurrency = &copy
}

func (w *WalletBalance) AddTokenBalance(balance TokenBalance) {
	w.TokenBalances = append(w.TokenBalances, balance)
}
