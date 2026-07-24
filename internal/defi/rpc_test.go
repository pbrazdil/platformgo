package defi

import (
	"encoding/json"
	"testing"
)

func assertRPCLog(t *testing.T, input, transactionHash, blockHash, blockNumber, data string, topics []string) {
	t.Helper()
	var log RPCLog
	if err := json.Unmarshal([]byte(input), &log); err != nil {
		t.Fatal(err)
	}
	if log.Removed || value(log.LogIndex) != "0x0" || value(log.TransactionIndex) != "0x0" ||
		value(log.TransactionHash) != transactionHash || value(log.BlockHash) != blockHash ||
		value(log.BlockNumber) != blockNumber || log.Address != "0x1f98431c8ad98523631ae4a59f267346ea31f984" ||
		log.Data != data || len(log.Topics) != len(topics) {
		t.Fatalf("log = %#v", log)
	}
	for index := range topics {
		if log.Topics[index] != topics[index] {
			t.Errorf("topic[%d] = %s", index, log.Topics[index])
		}
	}
}

func value(pointer *string) string {
	if pointer == nil {
		return ""
	}
	return *pointer
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/rpc.rs:113
//	test: test_rpc_log_deserialize_pool_created_block_185
func TestRPCLogDeserializePoolCreatedBlock185(t *testing.T) {
	input := `{"removed":false,"logIndex":"0x0","transactionIndex":"0x0","transactionHash":"0x24058dde7caf5b8b70041de8b27731f20f927365f210247c3e720e947b9098e7","blockHash":"0xd371b6c7b04ec33d6470f067a82e87d7b294b952bea7a46d7b939b4c7addc275","blockNumber":"0xb9","address":"0x1f98431c8ad98523631ae4a59f267346ea31f984","data":"0x000000000000000000000000000000000000000000000000000000000000003c000000000000000000000000b9fc136980d98c034a529aadbd5651c087365d5f","topics":["0x783cca1c0412dd0d695e784568c96da2e9c22ff989357a2e8b1d9b2b4e6b7118","0x0000000000000000000000002e5353426c89f4ecd52d1036da822d47e73376c4","0x000000000000000000000000838930cfe7502dd36b0b1ebbef8001fbf94f3bfb","0x0000000000000000000000000000000000000000000000000000000000000bb8"]}`
	assertRPCLog(t, input,
		"0x24058dde7caf5b8b70041de8b27731f20f927365f210247c3e720e947b9098e7",
		"0xd371b6c7b04ec33d6470f067a82e87d7b294b952bea7a46d7b939b4c7addc275",
		"0xb9",
		"0x000000000000000000000000000000000000000000000000000000000000003c000000000000000000000000b9fc136980d98c034a529aadbd5651c087365d5f",
		[]string{
			"0x783cca1c0412dd0d695e784568c96da2e9c22ff989357a2e8b1d9b2b4e6b7118",
			"0x0000000000000000000000002e5353426c89f4ecd52d1036da822d47e73376c4",
			"0x000000000000000000000000838930cfe7502dd36b0b1ebbef8001fbf94f3bfb",
			"0x0000000000000000000000000000000000000000000000000000000000000bb8",
		})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/rpc.rs:170
//	test: test_rpc_log_deserialize_pool_created_block_540
func TestRPCLogDeserializePoolCreatedBlock540(t *testing.T) {
	input := `{"removed":false,"logIndex":"0x0","transactionIndex":"0x0","transactionHash":"0x0810b3488eba9b0264d3544b4548b70d0c8667e05ac4a5d90686f4a9f70509df","blockHash":"0x59bb10cdfd586affc6aa4a0b12f0662ec04599a1a459ac5b33129bc2c8705ccd","blockNumber":"0x21c","address":"0x1f98431c8ad98523631ae4a59f267346ea31f984","data":"0x000000000000000000000000000000000000000000000000000000000000000a0000000000000000000000007d25de0bb3e4e4d5f7b399db5a0bca9f60dd66e4","topics":["0x783cca1c0412dd0d695e784568c96da2e9c22ff989357a2e8b1d9b2b4e6b7118","0x0000000000000000000000008dd7c686b11c115ffaba245cbfc418b371087f68","0x000000000000000000000000be5381d826375492e55e05039a541eb2cb978e76","0x00000000000000000000000000000000000000000000000000000000000001f4"]}`
	assertRPCLog(t, input,
		"0x0810b3488eba9b0264d3544b4548b70d0c8667e05ac4a5d90686f4a9f70509df",
		"0x59bb10cdfd586affc6aa4a0b12f0662ec04599a1a459ac5b33129bc2c8705ccd",
		"0x21c",
		"0x000000000000000000000000000000000000000000000000000000000000000a0000000000000000000000007d25de0bb3e4e4d5f7b399db5a0bca9f60dd66e4",
		[]string{
			"0x783cca1c0412dd0d695e784568c96da2e9c22ff989357a2e8b1d9b2b4e6b7118",
			"0x0000000000000000000000008dd7c686b11c115ffaba245cbfc418b371087f68",
			"0x000000000000000000000000be5381d826375492e55e05039a541eb2cb978e76",
			"0x00000000000000000000000000000000000000000000000000000000000001f4",
		})
}
