package defi

import (
	"fmt"
	"strings"

	"github.com/upcomers-org/platformgo/internal/currency"
)

type Blockchain string

const (
	Ethereum     Blockchain = "Ethereum"
	Polygon      Blockchain = "Polygon"
	Arbitrum     Blockchain = "Arbitrum"
	ArbitrumNova Blockchain = "ArbitrumNova"
	Base         Blockchain = "Base"
	Optimism     Blockchain = "Optimism"
	Avalanche    Blockchain = "Avalanche"
	Fantom       Blockchain = "Fantom"
	Bsc          Blockchain = "Bsc"
)

type Chain struct {
	Name                   Blockchain `json:"name"`
	ChainID                uint32     `json:"chain_id"`
	HyperSyncURL           string     `json:"hypersync_url"`
	RPCURL                 *string    `json:"rpc_url,omitempty"`
	NativeCurrencyDecimals uint8      `json:"native_currency_decimals"`
}

func NewChain(name Blockchain, chainID uint32) Chain {
	return Chain{
		Name: name, ChainID: chainID,
		HyperSyncURL:           fmt.Sprintf("https://%d.hypersync.xyz", chainID),
		NativeCurrencyDecimals: 18,
	}
}

func (c *Chain) SetRPCURL(url string) {
	c.RPCURL = &url
}

func (c Chain) String() string {
	return fmt.Sprintf("Chain(name=%s, id=%d)", c.Name, c.ChainID)
}

func (c Chain) NativeCurrency() (currency.Currency, error) {
	switch c.Name {
	case Ethereum, Arbitrum, ArbitrumNova, Base, Optimism:
		return currency.New("ETH", c.NativeCurrencyDecimals, 0, "Ethereum", currency.Crypto)
	case Polygon:
		return currency.New("POL", c.NativeCurrencyDecimals, 0, "Polygon", currency.Crypto)
	case Avalanche:
		return currency.New("AVAX", c.NativeCurrencyDecimals, 0, "Avalanche", currency.Crypto)
	case Bsc:
		return currency.New("BNB", c.NativeCurrencyDecimals, 0, "Binance Coin", currency.Crypto)
	default:
		return currency.Currency{}, fmt.Errorf("native currency not specified for chain %s", c.Name)
	}
}

var chains = []Chain{
	NewChain(Ethereum, 1),
	NewChain(Polygon, 137),
	NewChain(Arbitrum, 42161),
	NewChain(ArbitrumNova, 42170),
	NewChain(Base, 8453),
	NewChain(Optimism, 10),
	NewChain(Avalanche, 43114),
	NewChain(Fantom, 250),
	NewChain(Bsc, 56),
}

func ChainFromID(chainID uint32) (Chain, bool) {
	for _, chain := range chains {
		if chain.ChainID == chainID {
			return chain, true
		}
	}
	return Chain{}, false
}

func ChainFromName(name string) (Chain, bool) {
	for _, chain := range chains {
		if strings.EqualFold(string(chain.Name), name) {
			return chain, true
		}
	}
	return Chain{}, false
}
