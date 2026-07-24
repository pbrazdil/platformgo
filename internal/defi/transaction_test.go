package defi

import (
	"encoding/json"
	"math/big"
	"testing"
)

func parseTransactionResponse(t *testing.T, input string) Transaction {
	t.Helper()
	var response RPCNodeHTTPResponse[Transaction]
	if err := json.Unmarshal([]byte(input), &response); err != nil {
		t.Fatal(err)
	}
	if response.Result == nil {
		t.Fatal("RPC result is nil")
	}
	return *response.Result
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/data/transaction.rs:175
//	test: test_eth_transfer_tx
func TestETHTransferTransaction(t *testing.T) {
	input := `{"jsonrpc":"2.0","id":1,"result":{"blockHash":"0xfdba50e306d1b0ebd1971ec0440799b324229841637d8c56afbd1d6950bb09f0","blockNumber":"0x154a1d6","chainId":"0x1","from":"0xd6a8749e224ecdfcc79d473d3355b1b0eb51d423","gas":"0x5208","gasPrice":"0x2d7a7174","hash":"0x6d0b33a68953fdfa280a3a3d7a21e9513aed38d8587682f03728bc178b52b824","to":"0x3c9af20c7b7809a825373881f61b5a69ef8bc6bd","transactionIndex":"0x99","value":"0x5f5e100"}}`
	tx := parseTransactionResponse(t, input)
	if tx.Chain.Name != Ethereum ||
		tx.Hash != "0x6d0b33a68953fdfa280a3a3d7a21e9513aed38d8587682f03728bc178b52b824" ||
		tx.BlockHash != "0xfdba50e306d1b0ebd1971ec0440799b324229841637d8c56afbd1d6950bb09f0" ||
		tx.BlockNumber != 22_323_670 ||
		tx.From != "0xd6a8749e224ecdfcc79d473d3355b1b0eb51d423" ||
		tx.To != "0x3c9af20c7b7809a825373881f61b5a69ef8bc6bd" ||
		tx.Gas.Cmp(big.NewInt(21000)) != 0 || tx.GasPrice.Cmp(big.NewInt(762999156)) != 0 ||
		tx.TransactionIndex != 153 || tx.Value.Cmp(big.NewInt(100000000)) != 0 {
		t.Fatalf("transaction = %#v", tx)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/data/transaction.rs:211
//	test: test_smart_contract_interaction_tx
func TestSmartContractInteractionTransaction(t *testing.T) {
	input := `{"jsonrpc":"2.0","id":1,"result":{"blockHash":"0xfdba50e306d1b0ebd1971ec0440799b324229841637d8c56afbd1d6950bb09f0","blockNumber":"0x154a1d6","chainId":"0x1","from":"0x2b711ee00b50d67667c4439c28aeaf7b75cb6e0d","gas":"0xe4e1c0","gasPrice":"0x536bc8dc","hash":"0x6ba6dd4a82101d8a0387f4cb4ce57a2eb64a1e1bd0679a9d4ea8448a27004a57","to":"0x8c0bfc04ada21fd496c55b8c50331f904306f564","transactionIndex":"0x4a","value":"0x0"}}`
	tx := parseTransactionResponse(t, input)
	if tx.Chain.Name != Ethereum ||
		tx.Hash != "0x6ba6dd4a82101d8a0387f4cb4ce57a2eb64a1e1bd0679a9d4ea8448a27004a57" ||
		tx.BlockHash != "0xfdba50e306d1b0ebd1971ec0440799b324229841637d8c56afbd1d6950bb09f0" ||
		tx.From != "0x2b711ee00b50d67667c4439c28aeaf7b75cb6e0d" ||
		tx.To != "0x8c0bfc04ada21fd496c55b8c50331f904306f564" ||
		tx.Gas.Cmp(big.NewInt(15000000)) != 0 || tx.GasPrice.Cmp(big.NewInt(1399572700)) != 0 ||
		tx.TransactionIndex != 74 || tx.Value.Sign() != 0 {
		t.Fatalf("transaction = %#v", tx)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/data/transaction.rs:246
//	test: test_transaction_with_large_values
func TestTransactionWithLargeValues(t *testing.T) {
	input := `{"jsonrpc":"2.0","id":1,"result":{"blockHash":"0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef","blockNumber":"0x1000000","chainId":"0x1","from":"0x0000000000000000000000000000000000000001","gas":"0xffffffffffffffff","gasPrice":"0xde0b6b3a7640000","hash":"0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890","to":"0x0000000000000000000000000000000000000002","transactionIndex":"0x0","value":"0xde0b6b3a7640000"}}`
	tx := parseTransactionResponse(t, input)
	maxU64 := new(big.Int).SetUint64(^uint64(0))
	oneETH := big.NewInt(1_000_000_000_000_000_000)
	if tx.Gas.Cmp(maxU64) != 0 || tx.GasPrice.Cmp(oneETH) != 0 ||
		tx.Value.Cmp(oneETH) != 0 || tx.BlockNumber != 16_777_216 {
		t.Fatalf("large transaction = %#v", tx)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/data/transaction.rs:278
//	test: test_transaction_parsing_with_invalid_address_should_fail
func TestTransactionParsingWithInvalidAddressShouldFail(t *testing.T) {
	input := `{"jsonrpc":"2.0","id":1,"result":{"blockHash":"0x1","blockNumber":"0x1","chainId":"0x1","from":"0xinvalid_address","gas":"0x5208","gasPrice":"0x1","hash":"0x2","to":"0x0000000000000000000000000000000000000002","transactionIndex":"0x0","value":"0x0"}}`
	var response RPCNodeHTTPResponse[Transaction]
	if err := json.Unmarshal([]byte(input), &response); err == nil {
		t.Fatal("invalid address parsed successfully")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/data/transaction.rs:301
//	test: test_transaction_parsing_with_unknown_chain_should_fail
func TestTransactionParsingWithUnknownChainShouldFail(t *testing.T) {
	input := `{"jsonrpc":"2.0","id":1,"result":{"blockHash":"0x1","blockNumber":"0x1","chainId":"0x999999","from":"0x0000000000000000000000000000000000000001","gas":"0x5208","gasPrice":"0x1","hash":"0x2","to":"0x0000000000000000000000000000000000000002","transactionIndex":"0x0","value":"0x0"}}`
	var response RPCNodeHTTPResponse[Transaction]
	if err := json.Unmarshal([]byte(input), &response); err == nil {
		t.Fatal("unknown chain parsed successfully")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/data/transaction.rs:324
//	test: test_transaction_creation_with_constructor
func TestTransactionCreationWithConstructor(t *testing.T) {
	tx := NewTransaction(
		NewChain(Ethereum, 1),
		"0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890",
		"0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef",
		123456,
		"0x0000000000000000000000000000000000000001",
		"0x0000000000000000000000000000000000000002",
		big.NewInt(21000),
		big.NewInt(20000000000),
		0,
		big.NewInt(1_000_000_000_000_000_000),
	)
	if tx.From != "0x0000000000000000000000000000000000000001" ||
		tx.To != "0x0000000000000000000000000000000000000002" ||
		tx.Gas.Cmp(big.NewInt(21000)) != 0 || tx.GasPrice.Cmp(big.NewInt(20000000000)) != 0 ||
		tx.Value.Cmp(big.NewInt(1_000_000_000_000_000_000)) != 0 {
		t.Fatalf("transaction = %#v", tx)
	}
}
