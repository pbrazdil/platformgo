package edge

import (
	"context"
	"errors"
	"strings"

	platformv1 "github.com/upcomers-org/platformgo/contracts/gen/platform/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// GRPCServer is the versioned protobuf mirror of command admission.
type GRPCServer struct {
	platformv1.UnimplementedTradingServiceServer
	auth     Authenticator
	commands CommandSubmitter
}

// NewGRPCServer builds a TradingService implementation.
func NewGRPCServer(
	authenticator Authenticator,
	commands CommandSubmitter,
) *GRPCServer {
	return &GRPCServer{auth: authenticator, commands: commands}
}

// SubmitOrder authenticates, authorizes, validates, and durably admits one
// order using the same application boundary as REST.
func (server *GRPCServer) SubmitOrder(
	ctx context.Context,
	request *platformv1.SubmitOrderRequest,
) (*platformv1.OrderAccepted, error) {
	principal, err := server.clientPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if request == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if !principal.OwnsAccount(request.GetAccountId()) {
		return nil, status.Error(codes.PermissionDenied, "forbidden")
	}
	converted, err := grpcSubmitOrderRequest(request)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if server.commands == nil {
		return nil, status.Error(codes.Unavailable, "command service unavailable")
	}
	idempotencyKey := request.GetIdempotencyKey()
	if idempotencyKey == "" {
		idempotencyKey = "intent:" + request.GetIntentId()
	}
	accepted, err := server.commands.SubmitOrder(
		ctx,
		principal,
		request.GetAccountId(),
		idempotencyKey,
		converted,
	)
	switch {
	case errors.Is(err, ErrIdempotencyConflict):
		return nil, status.Error(codes.AlreadyExists, "idempotency key conflicts with another request")
	case errors.Is(err, ErrForbidden):
		return nil, status.Error(codes.PermissionDenied, "forbidden")
	case err != nil:
		return nil, status.Error(codes.Unavailable, "command admission unavailable")
	default:
		return &platformv1.OrderAccepted{
			OrderId: accepted.OrderID, IntentId: accepted.IntentID,
		}, nil
	}
}

func (server *GRPCServer) clientPrincipal(
	ctx context.Context,
) (Principal, error) {
	values := metadata.ValueFromIncomingContext(ctx, "authorization")
	if len(values) != 1 || !strings.HasPrefix(values[0], "Bearer ") ||
		server.auth == nil {
		return Principal{}, status.Error(codes.Unauthenticated, "unauthorized")
	}
	principal, err := server.auth.AuthenticateClient(
		ctx,
		strings.TrimPrefix(values[0], "Bearer "),
	)
	if err != nil || principal.Audience != AudienceClient {
		return Principal{}, status.Error(codes.Unauthenticated, "unauthorized")
	}
	return principal, nil
}

func grpcSubmitOrderRequest(
	request *platformv1.SubmitOrderRequest,
) (SubmitOrderRequest, error) {
	side := map[platformv1.Side]string{
		platformv1.Side_SIDE_BUY:  "BUY",
		platformv1.Side_SIDE_SELL: "SELL",
	}[request.GetSide()]
	orderType := map[platformv1.OrderType]string{
		platformv1.OrderType_ORDER_TYPE_MARKET:             "MARKET",
		platformv1.OrderType_ORDER_TYPE_LIMIT:              "LIMIT",
		platformv1.OrderType_ORDER_TYPE_STOP_MARKET:        "STOP_MARKET",
		platformv1.OrderType_ORDER_TYPE_STOP_LIMIT:         "STOP_LIMIT",
		platformv1.OrderType_ORDER_TYPE_TAKE_PROFIT_MARKET: "TAKE_PROFIT_MARKET",
		platformv1.OrderType_ORDER_TYPE_TAKE_PROFIT_LIMIT:  "TAKE_PROFIT_LIMIT",
	}[request.GetType()]
	timeInForce := map[platformv1.TimeInForce]string{
		platformv1.TimeInForce_TIME_IN_FORCE_GTC: "GTC",
		platformv1.TimeInForce_TIME_IN_FORCE_IOC: "IOC",
		platformv1.TimeInForce_TIME_IN_FORCE_FOK: "FOK",
	}[request.GetTimeInForce()]
	if side == "" || orderType == "" {
		return SubmitOrderRequest{}, errors.New("side and type must be specified")
	}
	var timeInForcePointer *string
	if timeInForce != "" {
		timeInForcePointer = &timeInForce
	}
	converted := SubmitOrderRequest{
		IntentID: request.GetIntentId(), Symbol: request.GetSymbol(),
		Side: side, Type: orderType, Quantity: request.GetQuantity(),
		Price: request.Price, TriggerPrice: request.TriggerPrice,
		ReduceOnly: request.GetReduceOnly(), TimeInForce: timeInForcePointer,
		MaxSlippageBPS: request.MaxSlippageBps,
	}
	if err := ValidateSubmitOrder(converted); err != nil {
		return SubmitOrderRequest{}, err
	}
	return converted, nil
}
