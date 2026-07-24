package defi

type RPCNodeWSSResponse[T any] struct {
	JSONRPC string                         `json:"jsonrpc"`
	Method  string                         `json:"method"`
	Params  RPCNodeSubscriptionResponse[T] `json:"params"`
}

type RPCNodeSubscriptionResponse[T any] struct {
	Subscription string `json:"subscription"`
	Result       T      `json:"result"`
}

type RPCNodeHTTPResponse[T any] struct {
	JSONRPC *string   `json:"jsonrpc"`
	ID      *uint64   `json:"id"`
	Result  *T        `json:"result"`
	Error   *RPCError `json:"error"`
	Code    *int32    `json:"code"`
	Message *string   `json:"message"`
}

type RPCError struct {
	Code    int32  `json:"code"`
	Message string `json:"message"`
}

type RPCLog struct {
	Removed          bool     `json:"removed"`
	LogIndex         *string  `json:"logIndex"`
	TransactionIndex *string  `json:"transactionIndex"`
	TransactionHash  *string  `json:"transactionHash"`
	BlockHash        *string  `json:"blockHash"`
	BlockNumber      *string  `json:"blockNumber"`
	Address          string   `json:"address"`
	Data             string   `json:"data"`
	Topics           []string `json:"topics"`
}
