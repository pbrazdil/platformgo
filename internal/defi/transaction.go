package defi

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
	"strings"
)

type Transaction struct {
	Chain            Chain
	Hash             string
	BlockHash        string
	BlockNumber      uint64
	From             string
	To               string
	Value            *big.Int
	TransactionIndex uint64
	Gas              *big.Int
	GasPrice         *big.Int
}

func NewTransaction(
	chain Chain,
	hash string,
	blockHash string,
	blockNumber uint64,
	from string,
	to string,
	gas *big.Int,
	gasPrice *big.Int,
	transactionIndex uint64,
	value *big.Int,
) Transaction {
	return Transaction{
		Chain: chain, Hash: hash, BlockHash: blockHash, BlockNumber: blockNumber,
		From: from, To: to, Gas: copyBig(gas), GasPrice: copyBig(gasPrice),
		TransactionIndex: transactionIndex, Value: copyBig(value),
	}
}

func (t *Transaction) UnmarshalJSON(data []byte) error {
	var wire struct {
		ChainID          string `json:"chainId"`
		Hash             string `json:"hash"`
		BlockHash        string `json:"blockHash"`
		BlockNumber      string `json:"blockNumber"`
		From             string `json:"from"`
		To               string `json:"to"`
		Value            string `json:"value"`
		TransactionIndex string `json:"transactionIndex"`
		Gas              string `json:"gas"`
		GasPrice         string `json:"gasPrice"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	chainID, err := parseHexUint32(wire.ChainID)
	if err != nil {
		return fmt.Errorf("parse chain ID: %w", err)
	}
	chain, ok := ChainFromID(chainID)
	if !ok {
		return fmt.Errorf("Unknown chain ID: %d", chainID)
	}
	if err := validateEVMAddress(wire.From); err != nil {
		return fmt.Errorf("invalid from address: %w", err)
	}
	if err := validateEVMAddress(wire.To); err != nil {
		return fmt.Errorf("invalid to address: %w", err)
	}
	blockNumber, err := parseHexUint64(wire.BlockNumber)
	if err != nil {
		return err
	}
	transactionIndex, err := parseHexUint64(wire.TransactionIndex)
	if err != nil {
		return err
	}
	gas, err := parseHexBig(wire.Gas)
	if err != nil {
		return err
	}
	gasPrice, err := parseHexBig(wire.GasPrice)
	if err != nil {
		return err
	}
	value, err := parseHexBig(wire.Value)
	if err != nil {
		return err
	}
	*t = NewTransaction(
		chain, wire.Hash, wire.BlockHash, blockNumber, wire.From, wire.To,
		gas, gasPrice, transactionIndex, value,
	)
	return nil
}

func parseHexUint32(value string) (uint32, error) {
	parsed, err := strconv.ParseUint(strings.TrimPrefix(value, "0x"), 16, 32)
	return uint32(parsed), err
}

func parseHexUint64(value string) (uint64, error) {
	return strconv.ParseUint(strings.TrimPrefix(value, "0x"), 16, 64)
}

func parseHexBig(value string) (*big.Int, error) {
	result, ok := new(big.Int).SetString(strings.TrimPrefix(value, "0x"), 16)
	if !ok {
		return nil, fmt.Errorf("invalid hexadecimal integer %q", value)
	}
	return result, nil
}

func copyBig(value *big.Int) *big.Int {
	if value == nil {
		return new(big.Int)
	}
	return new(big.Int).Set(value)
}

func validateEVMAddress(value string) error {
	if len(value) != 42 || !strings.HasPrefix(value, "0x") {
		return fmt.Errorf("address must contain 20 hexadecimal bytes")
	}
	if _, err := hex.DecodeString(value[2:]); err != nil {
		return fmt.Errorf("address is not hexadecimal: %w", err)
	}
	return nil
}
