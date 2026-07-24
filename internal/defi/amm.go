package defi

import (
	"fmt"

	"github.com/upcomers-org/platformgo/internal/ids"
)

type Pool struct {
	Chain          Chain
	Dex            Dex
	Address        string
	PoolIdentifier PoolIdentifier
	InstrumentID   ids.InstrumentID
	CreationBlock  uint64
	Token0         Token
	Token1         Token
	Fee            *uint32
	TickSpacing    *uint32
	TsEvent        uint64
	TsInit         uint64
}

func NewPool(
	chain Chain,
	dex Dex,
	address string,
	identifier PoolIdentifier,
	creationBlock uint64,
	token0, token1 Token,
	fee, tickSpacing *uint32,
	tsInit uint64,
) (Pool, error) {
	instrumentID, err := ids.NewInstrumentID(identifier.String(), string(chain.Name)+":"+dex.Name.String())
	if err != nil {
		return Pool{}, err
	}
	return Pool{
		Chain: chain, Dex: dex, Address: address, PoolIdentifier: identifier,
		InstrumentID: instrumentID, CreationBlock: creationBlock,
		Token0: token0, Token1: token1, Fee: fee, TickSpacing: tickSpacing,
		TsEvent: tsInit, TsInit: tsInit,
	}, nil
}

func (p Pool) BaseToken() Token {
	if p.Token0.Priority() < p.Token1.Priority() {
		return p.Token1
	}
	return p.Token0
}

func (p Pool) QuoteToken() Token {
	if p.Token0.Priority() < p.Token1.Priority() {
		return p.Token0
	}
	return p.Token1
}

func (p Pool) IsBaseQuoteInverted() bool {
	return p.Token0.Priority() < p.Token1.Priority()
}

func (p Pool) FullSpecification() string {
	fee := uint32(0)
	if p.Fee != nil {
		fee = *p.Fee
	}
	return fmt.Sprintf("%s/%s-%d.%s", p.Token0.Symbol, p.Token1.Symbol, fee, p.InstrumentID.Venue)
}
