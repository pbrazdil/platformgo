package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/upcomers-org/platformgo/internal/edge"
	"github.com/upcomers-org/platformgo/internal/engine"
)

type fixedClock struct{ value time.Time }

func (clock fixedClock) Now() time.Time { return clock.value }

type memoryJournal struct {
	next                 uint64
	requests             map[string]BeginCommandRequest
	results              map[string]BeginCommandResult
	last                 BeginCommandRequest
	gapOnce              bool
	beginCalls           int
	configurationVersion uint64
	replayHook           func(context.Context)
}

func (journal *memoryJournal) ConfigurationVersion(context.Context) (uint64, error) {
	if journal.configurationVersion == 0 {
		return 1, nil
	}
	return journal.configurationVersion, nil
}

func (journal *memoryJournal) NextAccountSequence(context.Context, string) (uint64, error) {
	if journal.next == 0 {
		return 1, nil
	}
	return journal.next, nil
}

func (journal *memoryJournal) InstrumentVersion(context.Context, string) (uint64, error) {
	return 13, nil
}

func (journal *memoryJournal) Replay(
	ctx context.Context,
	scope string,
	key string,
	requestHash [32]byte,
) (BeginCommandResult, bool, error) {
	if journal.replayHook != nil {
		journal.replayHook(ctx)
	}
	if journal.requests == nil {
		return BeginCommandResult{}, false, nil
	}
	previous, exists := journal.requests[scope+"\x00"+key]
	if !exists {
		return BeginCommandResult{}, false, nil
	}
	if previous.RequestHash != requestHash {
		return BeginCommandResult{}, true, ErrIdempotencyConflict
	}
	result := journal.results[scope+"\x00"+key]
	result.Created = false
	return result, true, nil
}

func (journal *memoryJournal) Begin(
	_ context.Context,
	request BeginCommandRequest,
) (BeginCommandResult, error) {
	journal.beginCalls++
	journal.last = request
	if journal.gapOnce {
		journal.gapOnce = false
		journal.next = request.AccountSequence + 1
		return BeginCommandResult{}, ErrCommandSequenceGap
	}
	if journal.requests == nil {
		journal.requests = make(map[string]BeginCommandRequest)
		journal.results = make(map[string]BeginCommandResult)
	}
	key := request.Scope + "\x00" + request.IdempotencyKey
	if previous, exists := journal.requests[key]; exists {
		if previous.RequestHash != request.RequestHash {
			return BeginCommandResult{}, ErrIdempotencyConflict
		}
		result := journal.results[key]
		result.Created = false
		return result, nil
	}
	result := BeginCommandResult{
		Created:   true,
		CommandID: request.CommandID,
		State:     IdempotencyInProgress,
		Response:  request.Response,
	}
	journal.requests[key] = request
	journal.results[key] = result
	journal.next = request.AccountSequence + 1
	return result, nil
}

func TestOrderSubmissionBuildsDurableEngineCommandAndReplays(t *testing.T) {
	journal := &memoryJournal{gapOnce: true, configurationVersion: 11}
	submission, err := NewOrderSubmission(journal, OrderSubmissionConfig{
		ShardID:        7,
		IdempotencyTTL: 24 * time.Hour,
		Clock: fixedClock{value: time.Date(
			2026, time.July, 25, 10, 11, 12, 123, time.UTC,
		)},
	})
	if err != nil {
		t.Fatal(err)
	}
	principal := edge.Principal{
		Subject: "urn:xb:user:user-7", Audience: edge.AudienceClient,
	}
	request := edge.SubmitOrderRequest{
		IntentID: "intent-7", Symbol: "BTC-PERP", Side: "BUY",
		Type: "LIMIT", Quantity: "1.250", Price: ptr("100.50"),
	}
	first, err := submission.SubmitOrder(
		context.Background(), principal, "urn:xb:account:acct-7", "idem-7", request,
	)
	if err != nil {
		t.Fatal(err)
	}
	second, err := submission.SubmitOrder(
		context.Background(), principal, "urn:xb:account:acct-7", "idem-7", request,
	)
	if err != nil {
		t.Fatal(err)
	}
	if first.OrderID == "" ||
		first.IntentID != request.IntentID ||
		first.OrderAccepted != second.OrderAccepted ||
		!bytes.Equal(first.Response.Body, second.Response.Body) {
		t.Fatalf("results = %#v, %#v", first, second)
	}
	if journal.beginCalls != 2 {
		t.Fatalf("Begin calls = %d, want only the gap retry", journal.beginCalls)
	}
	var admitted BeginCommandRequest
	for _, stored := range journal.requests {
		admitted = stored
	}
	input, action, err := engine.DecodeInputMessage(admitted.OutboxPayload)
	if err != nil {
		t.Fatal(err)
	}
	if input.ShardID != 7 || input.SourceSequence != 2 ||
		input.ConfigurationVersion != 11 || input.InstrumentVersion != 13 {
		t.Fatalf("input = %#v", input)
	}
	if action.SubmitOrder == nil ||
		"urn:xb:order:"+action.SubmitOrder.OrderID.String() != first.OrderID ||
		action.SubmitOrder.Quantity != "1.25" ||
		action.SubmitOrder.Price != "100.5" {
		t.Fatalf("action = %#v", action)
	}
}

func TestOrderSubmissionScopesIdempotencyAndRejectsChangedPayload(t *testing.T) {
	journal := &memoryJournal{}
	submission, err := NewOrderSubmission(journal, OrderSubmissionConfig{
		ShardID:        1,
		IdempotencyTTL: time.Hour,
		Clock:          fixedClock{value: time.Unix(100, 0)},
	})
	if err != nil {
		t.Fatal(err)
	}
	principal := edge.Principal{Subject: "user-1", Audience: edge.AudienceClient}
	request := edge.SubmitOrderRequest{
		IntentID: "i", Symbol: "BTC-PERP", Side: "BUY",
		Type: "MARKET", Quantity: "1",
	}
	if _, err := submission.SubmitOrder(context.Background(), principal, "account-1", "key", request); err != nil {
		t.Fatal(err)
	}
	request.Quantity = "2"
	if _, err := submission.SubmitOrder(context.Background(), principal, "account-1", "key", request); !errors.Is(err, edge.ErrIdempotencyConflict) {
		t.Fatalf("error = %v, want idempotency conflict", err)
	}
	other := edge.Principal{Subject: "user-2", Audience: edge.AudienceClient}
	if _, err := submission.SubmitOrder(context.Background(), other, "account-1", "key", request); err != nil {
		t.Fatalf("principal-scoped key failed: %v", err)
	}
}

func TestOrderSubmissionCanonicalizesEquivalentRequestsBeforeIdempotency(t *testing.T) {
	journal := &memoryJournal{}
	submission, err := NewOrderSubmission(journal, OrderSubmissionConfig{
		ShardID:        1,
		IdempotencyTTL: time.Hour,
		Clock:          fixedClock{value: time.Unix(100, 0)},
	})
	if err != nil {
		t.Fatal(err)
	}
	principal := edge.Principal{Subject: "user-1", Audience: edge.AudienceClient}
	first := edge.SubmitOrderRequest{
		IntentID: "intent-1",
		Symbol:   " BTC-PERP ",
		Side:     "buy",
		Quantity: "1.000",
	}
	gtc := " gtc "
	second := edge.SubmitOrderRequest{
		IntentID:    "intent-1",
		Symbol:      "BTC-PERP",
		Side:        "BUY",
		Type:        "market",
		TimeInForce: &gtc,
		Quantity:    "1",
	}
	accepted, err := submission.SubmitOrder(
		context.Background(), principal, "account-1", "key", first,
	)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := submission.SubmitOrder(
		context.Background(), principal, "account-1", "key", second,
	)
	if err != nil {
		t.Fatal(err)
	}
	if accepted.OrderAccepted != replayed.OrderAccepted ||
		!bytes.Equal(accepted.Response.Body, replayed.Response.Body) ||
		journal.beginCalls != 1 {
		t.Fatalf(
			"accepted=%#v replayed=%#v begin calls=%d",
			accepted,
			replayed,
			journal.beginCalls,
		)
	}
}

func TestOrderSubmissionReplaysWhileUnreadyButRejectsNewWork(t *testing.T) {
	journal := &memoryJournal{}
	ready := true
	submission, err := NewOrderSubmission(journal, OrderSubmissionConfig{
		ShardID:        1,
		IdempotencyTTL: time.Hour,
		Clock:          fixedClock{value: time.Unix(100, 0)},
		Readiness: func(context.Context) error {
			if ready {
				return nil
			}
			return errors.New("workers unavailable")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	principal := edge.Principal{Subject: "user-1", Audience: edge.AudienceClient}
	request := edge.SubmitOrderRequest{
		IntentID: "intent-1", Symbol: "BTC-PERP", Side: "BUY",
		Type: "MARKET", Quantity: "1",
	}
	first, err := submission.SubmitOrder(
		context.Background(), principal, "account-1", "key", request,
	)
	if err != nil {
		t.Fatal(err)
	}
	ready = false
	replayed, err := submission.SubmitOrder(
		context.Background(), principal, "account-1", "key", request,
	)
	if err != nil || first.OrderAccepted != replayed.OrderAccepted ||
		!bytes.Equal(first.Response.Body, replayed.Response.Body) {
		t.Fatalf("replay=%#v first=%#v err=%v", replayed, first, err)
	}
	changed := request
	changed.Quantity = "2"
	if _, err := submission.SubmitOrder(
		context.Background(), principal, "account-1", "key", changed,
	); !errors.Is(err, edge.ErrIdempotencyConflict) {
		t.Fatalf("changed replay error=%v, want idempotency conflict", err)
	}
	if _, err := submission.SubmitOrder(
		context.Background(), principal, "account-1", "new-key", request,
	); err == nil {
		t.Fatal("new command was admitted while readiness was false")
	}
	if journal.beginCalls != 1 || len(journal.requests) != 1 {
		t.Fatalf(
			"unready effects begin=%d requests=%d",
			journal.beginCalls,
			len(journal.requests),
		)
	}
	if !journal.last.RequireRuntimeReady {
		t.Fatal("production-readiness configuration was not bound to Begin")
	}
}

type readinessRaceContextKey struct{}

func TestOrderSubmissionLinearizesReplayAcrossReadinessLoss(t *testing.T) {
	for _, test := range []struct {
		name     string
		quantity string
		conflict bool
	}{
		{name: "same request replays", quantity: "1"},
		{name: "changed request conflicts", quantity: "2", conflict: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			journal := &memoryJournal{}
			atReadiness := make(chan struct{})
			releaseReadiness := make(chan struct{})
			submission, err := NewOrderSubmission(
				journal,
				OrderSubmissionConfig{
					ShardID:        1,
					IdempotencyTTL: time.Hour,
					Clock:          fixedClock{value: time.Unix(100, 0)},
					Readiness: func(ctx context.Context) error {
						if ctx.Value(readinessRaceContextKey{}) == nil {
							return nil
						}
						close(atReadiness)
						<-releaseReadiness
						return errors.New("workers unavailable")
					},
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			principal := edge.Principal{
				Subject: "user-1", Audience: edge.AudienceClient,
			}
			firstRequest := edge.SubmitOrderRequest{
				IntentID: "intent-1", Symbol: "BTC-PERP", Side: "BUY",
				Type: "MARKET", Quantity: "1",
			}
			retryRequest := firstRequest
			retryRequest.Quantity = test.quantity
			type retryResult struct {
				admission edge.OrderAdmission
				err       error
			}
			resultChannel := make(chan retryResult, 1)
			retryContext := context.WithValue(
				context.Background(),
				readinessRaceContextKey{},
				true,
			)
			go func() {
				admission, submitErr := submission.SubmitOrder(
					retryContext,
					principal,
					"account-1",
					"key",
					retryRequest,
				)
				resultChannel <- retryResult{
					admission: admission,
					err:       submitErr,
				}
			}()
			<-atReadiness
			first, err := submission.SubmitOrder(
				context.Background(),
				principal,
				"account-1",
				"key",
				firstRequest,
			)
			if err != nil {
				t.Fatal(err)
			}
			close(releaseReadiness)
			retry := <-resultChannel
			if test.conflict {
				if !errors.Is(retry.err, edge.ErrIdempotencyConflict) {
					t.Fatalf(
						"retry error=%v, want idempotency conflict",
						retry.err,
					)
				}
			} else if retry.err != nil ||
				first.OrderAccepted != retry.admission.OrderAccepted ||
				first.Response.Status != retry.admission.Response.Status ||
				!bytes.Equal(
					first.Response.Headers,
					retry.admission.Response.Headers,
				) ||
				!bytes.Equal(
					first.Response.Body,
					retry.admission.Response.Body,
				) {
				t.Fatalf(
					"first=%#v retry=%#v error=%v",
					first,
					retry.admission,
					retry.err,
				)
			}
			if journal.beginCalls != 1 || len(journal.requests) != 1 {
				t.Fatalf(
					"effects begin=%d requests=%d, want 1 and 1",
					journal.beginCalls,
					len(journal.requests),
				)
			}
		})
	}
}

func TestOrderSubmissionRejectsUnsupportedTrailingOrderBeforePersistence(t *testing.T) {
	journal := &memoryJournal{}
	submission, err := NewOrderSubmission(journal, OrderSubmissionConfig{
		ShardID:        1,
		IdempotencyTTL: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = submission.SubmitOrder(
		context.Background(),
		edge.Principal{Subject: "user-1", Audience: edge.AudienceClient},
		"account-1",
		"key",
		edge.SubmitOrderRequest{
			IntentID: "i", Symbol: "BTC-PERP", Side: "BUY",
			Type: "TRAILING_STOP_MARKET", Quantity: "1",
			TrailingOffset: ptr("5"),
		},
	)
	if err == nil || journal.beginCalls != 0 {
		t.Fatalf("error = %v, Begin calls = %d", err, journal.beginCalls)
	}
}

func TestCompletedReplayMustMatchAcceptedResponse(t *testing.T) {
	journal := &memoryJournal{}
	submission, err := NewOrderSubmission(journal, OrderSubmissionConfig{
		ShardID:        1,
		IdempotencyTTL: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	principal := edge.Principal{Subject: "user-1", Audience: edge.AudienceClient}
	request := edge.SubmitOrderRequest{
		IntentID: "i", Symbol: "BTC-PERP", Side: "BUY",
		Type: "MARKET", Quantity: "1",
	}
	accepted, err := submission.SubmitOrder(context.Background(), principal, "account-1", "key", request)
	if err != nil {
		t.Fatal(err)
	}
	for key, result := range journal.results {
		body, marshalErr := json.Marshal(accepted)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		result.Created = false
		result.State = IdempotencyCompleted
		result.Response = StoredResponse{Status: 202, Body: body}
		journal.results[key] = result
	}
	if _, err := submission.SubmitOrder(context.Background(), principal, "account-1", "key", request); err != nil {
		t.Fatalf("consistent completed replay: %v", err)
	}
}

func ptr(value string) *string { return &value }
