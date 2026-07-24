package orderbook

import (
	"crypto/sha256"
	"encoding/binary"
	"math/big"
)

var (
	minSigned128 = new(big.Int).Neg(new(big.Int).Lsh(big.NewInt(1), 127))
	maxSigned128 = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 127), big.NewInt(1))
	modulus128   = new(big.Int).Lsh(big.NewInt(1), 128)
)

// PriceToOrderID deterministically compresses a signed 128-bit raw price into
// a synthetic 64-bit order ID. A fixed cryptographic hash avoids the structural
// collisions caused by truncation or XOR-folding at 64-bit boundaries.
func PriceToOrderID(priceRaw *big.Int) uint64 {
	if priceRaw == nil || priceRaw.Cmp(minSigned128) < 0 || priceRaw.Cmp(maxSigned128) > 0 {
		panic("raw price is outside signed 128-bit range")
	}

	unsigned := new(big.Int).Set(priceRaw)
	if unsigned.Sign() < 0 {
		unsigned.Add(unsigned, modulus128)
	}
	var encoded [16]byte
	bytes := unsigned.Bytes()
	copy(encoded[len(encoded)-len(bytes):], bytes)
	sum := sha256.Sum256(encoded[:])
	return binary.LittleEndian.Uint64(sum[:8])
}

// PreProcessOrder normalizes synthetic IDs for aggregated book data.
func PreProcessOrder(bookType BookType, order Order, flags uint8) Order {
	switch bookType {
	case L1MBP:
		order.ID = uint64(order.Side)
	case L2MBP:
		order.ID = PriceToOrderID(fixedRaw(order.Price.Decimal(), false))
	case L3MBO:
		switch {
		case flags&FlagTOB != 0:
			order.ID = uint64(order.Side)
		case flags&FlagMBP != 0:
			order.ID = PriceToOrderID(fixedRaw(order.Price.Decimal(), false))
		}
	}
	return order
}
