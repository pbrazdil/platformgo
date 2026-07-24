package defi

import (
	"encoding/json"
	"math/big"
	"testing"
	"time"
)

func parseBlockTestResponse(t *testing.T, payload string) Block {
	t.Helper()
	var response RPCNodeWSSResponse[Block]
	if err := json.Unmarshal([]byte(payload), &response); err != nil {
		t.Fatal(err)
	}
	return response.Params.Result
}

func requireBlockBig(t *testing.T, got *big.Int, want uint64) {
	t.Helper()
	if got == nil || got.Cmp(new(big.Int).SetUint64(want)) != 0 {
		t.Fatalf("integer = %v, want %d", got, want)
	}
}

func unixNanosUTC(year int, month time.Month, day, hour, minute, second int) UnixNanos {
	return UnixNanos(time.Date(year, month, day, hour, minute, second, 0, time.UTC).UnixNano())
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/data/block.rs:347
//	test: test_block_set_chain
func TestBlockSetChain(t *testing.T) {
	block := NewBlock(
		"0x1234567890abcdef",
		"0xabcdef1234567890",
		12_345,
		"0x742E4422b21FB8B4dF463F28689AC98bD56c39e0",
		21_000,
		20_000,
		1_640_995_200_000_000_000,
		nil,
	)
	if block.Chain != nil {
		t.Fatalf("initial chain = %v, want nil", block.Chain)
	}

	block.SetChain(Ethereum)

	if block.Chain == nil || *block.Chain != Ethereum {
		t.Fatalf("chain = %v, want Ethereum", block.Chain)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/data/block.rs:368
//	test: test_ethereum_block_parsing
func TestEthereumBlockParsing(t *testing.T) {
	block := parseBlockTestResponse(t, `{
		"jsonrpc":"2.0","method":"eth_subscription","params":{"subscription":"test","result":{
			"baseFeePerGas":"0x1862a795","blobGasUsed":"0xc0000","excessBlobGas":"0x4840000",
			"gasLimit":"0x223b4a1","gasUsed":"0xde3909",
			"hash":"0x71ece187051700b814592f62774e6ebd8ebdf5efbb54c90859a7d1522ce38e0a",
			"miner":"0x4838b106fce9647bdf1e7877bf73ce8b0bad5f97","number":"0x1542e9f",
			"parentHash":"0x2abcce1ac985ebea2a2d6878a78387158f46de8d6db2cefca00ea36df4030a40",
			"timestamp":"0x6801f4bb"
		}}}`)
	block.SetChain(Ethereum)

	const expected = "Block(chain=Ethereum, number=22294175, timestamp=2025-04-18T06:44:11+00:00, hash=0x71ece187051700b814592f62774e6ebd8ebdf5efbb54c90859a7d1522ce38e0a)"
	if block.String() != expected ||
		block.Hash != "0x71ece187051700b814592f62774e6ebd8ebdf5efbb54c90859a7d1522ce38e0a" ||
		block.ParentHash != "0x2abcce1ac985ebea2a2d6878a78387158f46de8d6db2cefca00ea36df4030a40" ||
		block.Number != 22_294_175 ||
		block.Miner != "0x4838b106fce9647bdf1e7877bf73ce8b0bad5f97" ||
		block.Timestamp != unixNanosUTC(2025, time.April, 18, 6, 44, 11) ||
		block.GasUsed != 14_563_593 ||
		block.GasLimit != 35_894_433 {
		t.Fatalf("Ethereum block = %#v", block)
	}
	requireBlockBig(t, block.BaseFeePerGas, 0x1862_a795)
	requireBlockBig(t, block.BlobGasUsed, 0xc0000)
	requireBlockBig(t, block.ExcessBlobGas, 0x0484_0000)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/data/block.rs:404
//	test: test_polygon_block_parsing
func TestPolygonBlockParsing(t *testing.T) {
	block := parseBlockTestResponse(t, `{
		"jsonrpc":"2.0","method":"eth_subscription","params":{"subscription":"test","result":{
			"baseFeePerGas":"0x19e","gasLimit":"0x1c9c380","gasUsed":"0x1270f14",
			"hash":"0x38ca655a2009e1748097f5559a0c20de7966243b804efeb53183614e4bebe199",
			"miner":"0x0000000000000000000000000000000000000000","number":"0x43309ed",
			"parentHash":"0xf25e108267e3d6e1e4aaf4e329872273f2b1ad6186a4a22e370623aa8d021c50",
			"timestamp":"0x680250d5"
		}}}`)
	block.SetChain(Polygon)

	const expected = "Block(chain=Polygon, number=70453741, timestamp=2025-04-18T13:17:09+00:00, hash=0x38ca655a2009e1748097f5559a0c20de7966243b804efeb53183614e4bebe199)"
	if block.String() != expected ||
		block.Hash != "0x38ca655a2009e1748097f5559a0c20de7966243b804efeb53183614e4bebe199" ||
		block.ParentHash != "0xf25e108267e3d6e1e4aaf4e329872273f2b1ad6186a4a22e370623aa8d021c50" ||
		block.Number != 70_453_741 ||
		block.Miner != "0x0000000000000000000000000000000000000000" ||
		block.Timestamp != unixNanosUTC(2025, time.April, 18, 13, 17, 9) ||
		block.GasUsed != 19_336_980 ||
		block.GasLimit != 30_000_000 ||
		block.BlobGasUsed != nil ||
		block.ExcessBlobGas != nil {
		t.Fatalf("Polygon block = %#v", block)
	}
	requireBlockBig(t, block.BaseFeePerGas, 0x19e)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/data/block.rs:439
//	test: test_base_block_parsing
func TestBaseBlockParsing(t *testing.T) {
	block := parseBlockTestResponse(t, `{
		"jsonrpc":"2.0","method":"eth_subscription","params":{"subscription":"test","result":{
			"baseFeePerGas":"0xaae54","blobGasUsed":"0x0","excessBlobGas":"0x0",
			"gasLimit":"0x7270e00","gasUsed":"0x56fce26",
			"hash":"0x14575c65070d455e6d20d5ee17be124917a33ce4437dd8615a56d29e8279b7ad",
			"miner":"0x4200000000000000000000000000000000000011","number":"0x1bca2ac",
			"parentHash":"0x9a6ad4ffb258faa47ecd5eea9e7a9d8fa1772aa6232bc7cb4bbad5bc30786258",
			"timestamp":"0x6803a23b"
		}}}`)
	block.SetChain(Base)

	const expected = "Block(chain=Base, number=29139628, timestamp=2025-04-19T13:16:43+00:00, hash=0x14575c65070d455e6d20d5ee17be124917a33ce4437dd8615a56d29e8279b7ad)"
	if block.String() != expected ||
		block.Hash != "0x14575c65070d455e6d20d5ee17be124917a33ce4437dd8615a56d29e8279b7ad" ||
		block.ParentHash != "0x9a6ad4ffb258faa47ecd5eea9e7a9d8fa1772aa6232bc7cb4bbad5bc30786258" ||
		block.Number != 29_139_628 ||
		block.Miner != "0x4200000000000000000000000000000000000011" ||
		block.Timestamp != unixNanosUTC(2025, time.April, 19, 13, 16, 43) ||
		block.GasUsed != 91_213_350 ||
		block.GasLimit != 120_000_000 {
		t.Fatalf("Base block = %#v", block)
	}
	requireBlockBig(t, block.BaseFeePerGas, 0xaae54)
	requireBlockBig(t, block.BlobGasUsed, 0)
	requireBlockBig(t, block.ExcessBlobGas, 0)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/data/block.rs:475
//	test: test_arbitrum_block_parsing
func TestArbitrumBlockParsing(t *testing.T) {
	block := parseBlockTestResponse(t, `{
		"jsonrpc":"2.0","method":"eth_subscription","params":{"subscription":"test","result":{
			"baseFeePerGas":"0x989680","gasLimit":"0x4000000000000","gasUsed":"0x17af4",
			"hash":"0x724a0af4720fd7624976f71b16163de25f8532e87d0e7058eb0c1d3f6da3c1f8",
			"miner":"0xa4b000000000000000000073657175656e636572","number":"0x138d1ab4",
			"parentHash":"0xe7176e201c2db109be479770074ad11b979de90ac850432ed38ed335803861b6",
			"timestamp":"0x6803a606"
		}}}`)
	block.SetChain(Arbitrum)

	const expected = "Block(chain=Arbitrum, number=328014516, timestamp=2025-04-19T13:32:54+00:00, hash=0x724a0af4720fd7624976f71b16163de25f8532e87d0e7058eb0c1d3f6da3c1f8)"
	if block.String() != expected ||
		block.Hash != "0x724a0af4720fd7624976f71b16163de25f8532e87d0e7058eb0c1d3f6da3c1f8" ||
		block.ParentHash != "0xe7176e201c2db109be479770074ad11b979de90ac850432ed38ed335803861b6" ||
		block.Number != 328_014_516 ||
		block.Miner != "0xa4b000000000000000000073657175656e636572" ||
		block.Timestamp != unixNanosUTC(2025, time.April, 19, 13, 32, 54) ||
		block.GasUsed != 97_012 ||
		block.GasLimit != 1_125_899_906_842_624 ||
		block.BlobGasUsed != nil ||
		block.ExcessBlobGas != nil {
		t.Fatalf("Arbitrum block = %#v", block)
	}
	requireBlockBig(t, block.BaseFeePerGas, 0x0098_9680)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/data/block.rs:511
//	test: test_block_builder_helpers
func TestBlockBuilderHelpers(t *testing.T) {
	chain := Arbitrum
	block := NewBlock(
		"0xabc",
		"0xdef",
		1,
		"0x0000000000000000000000000000000000000000",
		100_000,
		50_000,
		1_700_000_000,
		&chain,
	).
		WithBaseFee(new(big.Int).SetUint64(1_000)).
		WithBlobGas(new(big.Int).SetUint64(0x10), new(big.Int).SetUint64(0x20)).
		WithL1FeeComponents(new(big.Int).SetUint64(30_000), 1_234, 1_000_000)

	if block.Chain == nil || *block.Chain != Arbitrum ||
		block.L1GasUsed == nil || *block.L1GasUsed != 1_234 ||
		block.L1FeeScalar == nil || *block.L1FeeScalar != 1_000_000 {
		t.Fatalf("builder block = %#v", block)
	}
	requireBlockBig(t, block.BaseFeePerGas, 1_000)
	requireBlockBig(t, block.BlobGasUsed, 0x10)
	requireBlockBig(t, block.ExcessBlobGas, 0x20)
	requireBlockBig(t, block.L1GasPrice, 30_000)
}
