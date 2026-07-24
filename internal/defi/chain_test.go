package defi

import "testing"

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/chain.rs:550
//	test: test_ethereum_chain
func TestEthereumChain(t *testing.T) {
	chain, ok := ChainFromID(1)
	if !ok || chain.String() != "Chain(name=Ethereum, id=1)" ||
		chain.Name != Ethereum || chain.ChainID != 1 ||
		chain.HyperSyncURL != "https://1.hypersync.xyz" {
		t.Fatalf("chain = %#v, %v", chain, ok)
	}
	native, err := chain.NativeCurrency()
	if err != nil || native.Code != "ETH" || native.Precision != 18 || native.Name != "Ethereum" {
		t.Fatalf("native = %#v, %v", native, err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/chain.rs:565
//	test: test_arbitrum_chain
func TestArbitrumChain(t *testing.T) {
	chain, ok := ChainFromID(42161)
	if !ok || chain.String() != "Chain(name=Arbitrum, id=42161)" ||
		chain.Name != Arbitrum || chain.ChainID != 42161 ||
		chain.HyperSyncURL != "https://42161.hypersync.xyz" {
		t.Fatalf("chain = %#v, %v", chain, ok)
	}
	native, err := chain.NativeCurrency()
	if err != nil || native.Code != "ETH" || native.Precision != 18 || native.Name != "Ethereum" {
		t.Fatalf("native = %#v, %v", native, err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/chain.rs:583
//	test: test_chain_constructor
func TestChainConstructor(t *testing.T) {
	chain := NewChain(Polygon, 137)
	if chain.Name != Polygon || chain.ChainID != 137 ||
		chain.HyperSyncURL != "https://137.hypersync.xyz" ||
		chain.RPCURL != nil || chain.NativeCurrencyDecimals != 18 {
		t.Fatalf("chain = %#v", chain)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/chain.rs:594
//	test: test_chain_set_rpc_url
func TestChainSetRPCURL(t *testing.T) {
	chain := NewChain(Ethereum, 1)
	if chain.RPCURL != nil {
		t.Fatal("new chain has RPC URL")
	}
	const url = "https://mainnet.infura.io/v3/YOUR-PROJECT-ID"
	chain.SetRPCURL(url)
	if chain.RPCURL == nil || *chain.RPCURL != url {
		t.Fatalf("RPC URL = %v", chain.RPCURL)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/chain.rs:605
//	test: test_chain_from_chain_id_valid
func TestChainFromChainIDValid(t *testing.T) {
	for _, id := range []uint32{1, 137, 42161, 8453} {
		if _, ok := ChainFromID(id); !ok {
			t.Errorf("chain ID %d not found", id)
		}
	}
	ethereum, _ := ChainFromID(1)
	if ethereum.Name != Ethereum || ethereum.ChainID != 1 {
		t.Fatalf("Ethereum = %#v", ethereum)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/chain.rs:619
//	test: test_chain_from_chain_id_invalid
func TestChainFromChainIDInvalid(t *testing.T) {
	for _, id := range []uint32{999999, 0} {
		if _, ok := ChainFromID(id); ok {
			t.Errorf("unknown chain ID %d resolved", id)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/chain.rs:626
//	test: test_chain_from_chain_name_valid
func TestChainFromChainNameValid(t *testing.T) {
	for _, name := range []string{"Ethereum", "Polygon", "Arbitrum", "Base"} {
		if _, ok := ChainFromName(name); !ok {
			t.Errorf("chain name %q not found", name)
		}
	}
	ethereum, _ := ChainFromName("Ethereum")
	if ethereum.Name != Ethereum || ethereum.ChainID != 1 {
		t.Fatalf("Ethereum = %#v", ethereum)
	}
	nova, ok := ChainFromName("ArbitrumNova")
	if !ok || nova.Name != ArbitrumNova || nova.ChainID != 42170 {
		t.Fatalf("ArbitrumNova = %#v, %v", nova, ok)
	}
	bsc, ok := ChainFromName("Bsc")
	if !ok || bsc.Name != Bsc || bsc.ChainID != 56 {
		t.Fatalf("Bsc = %#v, %v", bsc, ok)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/chain.rs:650
//	test: test_chain_from_chain_name_invalid
func TestChainFromChainNameInvalid(t *testing.T) {
	for _, name := range []string{"InvalidChain", "", "NonExistentNetwork"} {
		if _, ok := ChainFromName(name); ok {
			t.Errorf("unknown chain name %q resolved", name)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/chain.rs:658
//	test: test_chain_from_chain_name_case_sensitive
func TestChainFromChainNameCaseInsensitive(t *testing.T) {
	for _, name := range []string{"Ethereum", "ethereum", "ETHEREUM", "EtHeReUm", "Arbitrum", "arbitrum"} {
		if _, ok := ChainFromName(name); !ok {
			t.Errorf("case-insensitive name %q not found", name)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/chain.rs:670
//	test: test_chain_from_chain_name_consistency_with_id
func TestChainFromChainNameConsistencyWithID(t *testing.T) {
	tests := []struct {
		name string
		id   uint32
	}{
		{"Ethereum", 1}, {"Polygon", 137}, {"Arbitrum", 42161}, {"Base", 8453},
		{"Optimism", 10}, {"Avalanche", 43114}, {"Fantom", 250}, {"Bsc", 56},
	}
	for _, test := range tests {
		byName, nameOK := ChainFromName(test.name)
		byID, idOK := ChainFromID(test.id)
		if !nameOK || !idOK || byName.Name != byID.Name ||
			byName.ChainID != byID.ChainID || byName.HyperSyncURL != byID.HyperSyncURL {
			t.Errorf("%s/%d mismatch: %#v %#v", test.name, test.id, byName, byID)
		}
	}
}
