package defi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"time"
)

type UnixNanos uint64

// BlockPosition identifies an event's exact location within a blockchain.
type BlockPosition struct {
	Number           uint64 `json:"number"`
	TransactionHash  string `json:"transaction_hash"`
	TransactionIndex uint32 `json:"transaction_index"`
	LogIndex         uint32 `json:"log_index"`
}

func NewBlockPosition(number uint64, transactionHash string, index, logIndex uint32) BlockPosition {
	return BlockPosition{
		Number: number, TransactionHash: transactionHash,
		TransactionIndex: index, LogIndex: logIndex,
	}
}

// Block is the essential metadata of an Ethereum-compatible block.
type Block struct {
	Chain         *Blockchain `json:"-"`
	Hash          string      `json:"hash"`
	Number        uint64      `json:"number"`
	ParentHash    string      `json:"parentHash"`
	Miner         string      `json:"miner"`
	GasLimit      uint64      `json:"gasLimit"`
	GasUsed       uint64      `json:"gasUsed"`
	BaseFeePerGas *big.Int    `json:"baseFeePerGas,omitempty"`
	BlobGasUsed   *big.Int    `json:"blobGasUsed,omitempty"`
	ExcessBlobGas *big.Int    `json:"excessBlobGas,omitempty"`
	L1GasPrice    *big.Int    `json:"l1GasPrice,omitempty"`
	L1GasUsed     *uint64     `json:"l1GasUsed,omitempty"`
	L1FeeScalar   *uint64     `json:"l1FeeScalar,omitempty"`
	Timestamp     UnixNanos   `json:"timestamp"`
}

func NewBlock(
	hash, parentHash string,
	number uint64,
	miner string,
	gasLimit, gasUsed uint64,
	timestamp UnixNanos,
	chain *Blockchain,
) Block {
	return Block{
		Chain: copyBlockPointer(chain), Hash: hash, ParentHash: parentHash,
		Number: number, Miner: miner, GasLimit: gasLimit, GasUsed: gasUsed,
		Timestamp: timestamp,
	}
}

func (b Block) Blockchain() Blockchain {
	if b.Chain == nil {
		panic("Must have the `chain` field set")
	}
	return *b.Chain
}

func (b *Block) SetChain(chain Blockchain) {
	b.Chain = copyBlockPointer(&chain)
}

func (b Block) WithBaseFee(fee *big.Int) Block {
	b.BaseFeePerGas = copyBlockBig(fee)
	return b
}

func (b Block) WithBlobGas(used, excess *big.Int) Block {
	b.BlobGasUsed = copyBlockBig(used)
	b.ExcessBlobGas = copyBlockBig(excess)
	return b
}

func (b Block) WithL1FeeComponents(price *big.Int, gasUsed, scalar uint64) Block {
	b.L1GasPrice = copyBlockBig(price)
	b.L1GasUsed = copyBlockPointer(&gasUsed)
	b.L1FeeScalar = copyBlockPointer(&scalar)
	return b
}

func (b Block) Equal(other Block) bool {
	return b.Hash == other.Hash
}

func (b Block) String() string {
	timestamp := time.Unix(0, int64(b.Timestamp)).UTC().
		Format("2006-01-02T15:04:05-07:00")
	return fmt.Sprintf(
		"Block(chain=%s, number=%d, timestamp=%s, hash=%s)",
		b.Blockchain(), b.Number, timestamp, b.Hash,
	)
}

func (b *Block) UnmarshalJSON(data []byte) error {
	var wire struct {
		Hash          string          `json:"hash"`
		Number        string          `json:"number"`
		ParentHash    string          `json:"parentHash"`
		Miner         string          `json:"miner"`
		GasLimit      string          `json:"gasLimit"`
		GasUsed       string          `json:"gasUsed"`
		BaseFee       json.RawMessage `json:"baseFeePerGas"`
		BlobGasUsed   json.RawMessage `json:"blobGasUsed"`
		ExcessBlobGas json.RawMessage `json:"excessBlobGas"`
		L1GasPrice    json.RawMessage `json:"l1GasPrice"`
		L1GasUsed     json.RawMessage `json:"l1GasUsed"`
		L1FeeScalar   json.RawMessage `json:"l1FeeScalar"`
		Timestamp     string          `json:"timestamp"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	number, err := ParseHexU64(wire.Number)
	if err != nil {
		return fmt.Errorf("parse block number: %w", err)
	}
	gasLimit, err := ParseHexU64(wire.GasLimit)
	if err != nil {
		return fmt.Errorf("parse gas limit: %w", err)
	}
	gasUsed, err := ParseHexU64(wire.GasUsed)
	if err != nil {
		return fmt.Errorf("parse gas used: %w", err)
	}
	timestamp, err := HexTimestampNanos(wire.Timestamp)
	if err != nil {
		return fmt.Errorf("parse timestamp: %w", err)
	}
	baseFee, err := parseBlockOptionalU256(wire.BaseFee)
	if err != nil {
		return fmt.Errorf("parse base fee per gas: %w", err)
	}
	blobGasUsed, err := parseBlockOptionalU256(wire.BlobGasUsed)
	if err != nil {
		return fmt.Errorf("parse blob gas used: %w", err)
	}
	excessBlobGas, err := parseBlockOptionalU256(wire.ExcessBlobGas)
	if err != nil {
		return fmt.Errorf("parse excess blob gas: %w", err)
	}
	l1GasPrice, err := parseBlockOptionalU256(wire.L1GasPrice)
	if err != nil {
		return fmt.Errorf("parse L1 gas price: %w", err)
	}
	l1GasUsed, err := parseBlockOptionalU64(wire.L1GasUsed)
	if err != nil {
		return fmt.Errorf("parse L1 gas used: %w", err)
	}
	l1FeeScalar, err := parseBlockOptionalU64(wire.L1FeeScalar)
	if err != nil {
		return fmt.Errorf("parse L1 fee scalar: %w", err)
	}
	*b = Block{
		Hash: wire.Hash, Number: number, ParentHash: wire.ParentHash, Miner: wire.Miner,
		GasLimit: gasLimit, GasUsed: gasUsed, BaseFeePerGas: baseFee,
		BlobGasUsed: blobGasUsed, ExcessBlobGas: excessBlobGas,
		L1GasPrice: l1GasPrice, L1GasUsed: l1GasUsed, L1FeeScalar: l1FeeScalar,
		Timestamp: UnixNanos(timestamp),
	}
	return nil
}

func parseBlockOptionalU256(data json.RawMessage) (*big.Int, error) {
	if len(data) == 0 || bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return nil, nil
	}
	return ParseOptionalHexU256JSON(data)
}

func parseBlockOptionalU64(data json.RawMessage) (*uint64, error) {
	if len(data) == 0 || bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return nil, nil
	}
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return nil, err
	}
	value, err := ParseHexU64(text)
	if err != nil {
		return nil, err
	}
	return &value, nil
}

func copyBlockBig(value *big.Int) *big.Int {
	if value == nil {
		return nil
	}
	return new(big.Int).Set(value)
}

func copyBlockPointer[T any](value *T) *T {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}
