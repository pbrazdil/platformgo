package defi

import (
	"bytes"
	"encoding/json"
	"errors"
	"math/big"
	"strconv"
	"strings"
)

func ParseHexU64(value string) (uint64, error) {
	value = strings.TrimPrefix(strings.TrimPrefix(value, "0x"), "0X")
	if len(value) > 16 {
		return 0, strconv.ErrRange
	}
	return strconv.ParseUint(value, 16, 64)
}

func HexTimestampNanos(value string) (uint64, error) {
	seconds, err := ParseHexU64(value)
	if err != nil {
		return 0, err
	}
	const nanosPerSecond = uint64(1_000_000_000)
	if seconds > ^uint64(0)/nanosPerSecond {
		return 0, errors.New("UnixNanos overflow when converting timestamp")
	}
	return seconds * nanosPerSecond, nil
}

func ParseOptionalHexU256JSON(data []byte) (*big.Int, error) {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return nil, nil
	}
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, err
	}
	value = strings.TrimPrefix(strings.TrimPrefix(value, "0x"), "0X")
	if len(value) > 64 {
		return nil, errors.New("hex value exceeds U256")
	}
	result, ok := new(big.Int).SetString(value, 16)
	if !ok {
		return nil, errors.New("invalid hexadecimal U256")
	}
	return result, nil
}
