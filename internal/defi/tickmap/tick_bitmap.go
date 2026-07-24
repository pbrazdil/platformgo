package tickmap

import "math/big"

func TickPosition(tick int32) (int16, uint8) {
	return int16(tick >> 8), uint8(tick & 0xff)
}

type TickBitmap struct {
	words       map[int16]*big.Int
	tickSpacing int32
}

func NewTickBitmap(tickSpacing uint32) *TickBitmap {
	if tickSpacing == 0 {
		panic("Tick spacing must be greater than zero")
	}
	return &TickBitmap{
		words:       make(map[int16]*big.Int),
		tickSpacing: int32(tickSpacing),
	}
}

func (b *TickBitmap) FlipTick(tick int32) {
	remainder := tick % b.tickSpacing
	if remainder != 0 {
		panic("Tick must be multiple of tick spacing")
	}
	compressed := tick / b.tickSpacing
	wordPosition, bitPosition := TickPosition(compressed)
	word := new(big.Int)
	if existing := b.words[wordPosition]; existing != nil {
		word.Set(existing)
	}
	word.Xor(word, new(big.Int).Lsh(big.NewInt(1), uint(bitPosition)))
	if word.Sign() == 0 {
		delete(b.words, wordPosition)
	} else {
		b.words[wordPosition] = word
	}
}

func (b *TickBitmap) IsInitialized(tick int32) bool {
	compressed := tick / b.tickSpacing
	wordPosition, bitPosition := TickPosition(compressed)
	word := b.words[wordPosition]
	return word != nil && word.Bit(int(bitPosition)) == 1
}

func (b *TickBitmap) NextInitializedTickWithinOneWord(tick int32, lessThanOrEqual bool) (int32, bool) {
	compressed := tick / b.tickSpacing
	if tick < 0 && tick%b.tickSpacing != 0 {
		compressed--
	}

	if lessThanOrEqual {
		wordPosition, bitPosition := TickPosition(compressed)
		word := b.words[wordPosition]
		for bit := int(bitPosition); bit >= 0; bit-- {
			if word != nil && word.Bit(bit) == 1 {
				return (compressed - int32(bitPosition) + int32(bit)) * b.tickSpacing, true
			}
		}
		return (compressed - int32(bitPosition)) * b.tickSpacing, false
	}

	nextCompressed := compressed + 1
	wordPosition, bitPosition := TickPosition(nextCompressed)
	word := b.words[wordPosition]
	for bit := int(bitPosition); bit <= 255; bit++ {
		if word != nil && word.Bit(bit) == 1 {
			return (nextCompressed + int32(bit) - int32(bitPosition)) * b.tickSpacing, true
		}
	}
	return (nextCompressed + 255 - int32(bitPosition)) * b.tickSpacing, false
}
