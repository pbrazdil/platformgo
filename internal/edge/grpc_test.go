package edge

import (
	"context"
	"net"
	"testing"

	platformv1 "github.com/upcomers-org/platformgo/contracts/gen/platform/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

func TestGRPCSubmitOrderMatchesRESTCommandContract(t *testing.T) {
	client, closeServer := newGRPCTestClient(t)
	defer closeServer()
	ctx := metadata.NewOutgoingContext(
		context.Background(),
		metadata.Pairs("authorization", "Bearer client-token"),
	)
	price := "100.50"
	request := &platformv1.SubmitOrderRequest{
		AccountId:      "urn:xb:account:acct-7",
		IntentId:       "intent-7",
		Symbol:         "BTC-PERP",
		Side:           platformv1.Side_SIDE_BUY,
		Type:           platformv1.OrderType_ORDER_TYPE_LIMIT,
		Quantity:       "1.250",
		Price:          &price,
		TimeInForce:    platformv1.TimeInForce_TIME_IN_FORCE_GTC,
		IdempotencyKey: "idem-7",
	}
	first, err := client.SubmitOrder(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := client.SubmitOrder(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if first.GetOrderId() == "" || first.GetIntentId() != "intent-7" ||
		first.GetOrderId() != second.GetOrderId() {
		t.Fatalf("responses = %#v, %#v", first, second)
	}
}

func TestGRPCStatusMappingAndInvalidEnum(t *testing.T) {
	client, closeServer := newGRPCTestClient(t)
	defer closeServer()
	request := &platformv1.SubmitOrderRequest{
		AccountId: "urn:xb:account:acct-7", IntentId: "intent-7",
		Symbol: "BTC-PERP", Side: platformv1.Side_SIDE_UNSPECIFIED,
		Type: platformv1.OrderType_ORDER_TYPE_MARKET, Quantity: "1",
	}
	if _, err := client.SubmitOrder(context.Background(), request); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("anonymous code = %s", status.Code(err))
	}
	ctx := metadata.NewOutgoingContext(
		context.Background(),
		metadata.Pairs("authorization", "Bearer client-token"),
	)
	if _, err := client.SubmitOrder(ctx, request); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("invalid enum code = %s, error = %v", status.Code(err), err)
	}
	request.Side = platformv1.Side_SIDE_BUY
	request.AccountId = "urn:xb:account:other"
	if _, err := client.SubmitOrder(ctx, request); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("ownership code = %s", status.Code(err))
	}
}

func newGRPCTestClient(
	t *testing.T,
) (platformv1.TradingServiceClient, func()) {
	t.Helper()
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	platformv1.RegisterTradingServiceServer(
		server,
		NewGRPCServer(testAuthenticator{}, &testCommands{}),
	)
	go func() {
		_ = server.Serve(listener)
	}()
	connection, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
	)
	if err != nil {
		server.Stop()
		t.Fatal(err)
	}
	return platformv1.NewTradingServiceClient(connection), func() {
		_ = connection.Close()
		server.Stop()
		_ = listener.Close()
	}
}
