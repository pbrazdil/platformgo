package edge

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// recordingPredictionMarketsReader is deliberately narrower than the other
// trading readers. The public catalog has no principal, query, or pagination
// input; the future edge dependency should expose only this one read.
type recordingPredictionMarketsReader struct {
	calls   int
	markets []PredictionMarketView
	err     error
}

func (reader *recordingPredictionMarketsReader) PredictionMarkets(
	context.Context,
) ([]PredictionMarketView, error) {
	reader.calls++
	return reader.markets, reader.err
}

var _ PredictionMarketsReader = (*recordingPredictionMarketsReader)(nil)

// TestPredictionMarketsRouteIsPublicAndPreservesExactCatalog proves the
// public GET boundary and its byte-level JSON contract.
//
// Source contract:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	route declaration: apps/app/src/api/client/trading.rs:47-53
//	handler function: apps/app/src/api/client/trading.rs:148-162 (list_prediction_markets)
//	DTO declarations: shared/api/src/prediction.rs:7-45
//
// Source-preserved assertions:
//   - GET /v1/prediction-markets is public and reaches the catalog query.
//   - Prediction-market DTO declaration order, camel-case names, optional omission, nested event/legs, and the already-formatted resolution timestamp remain exact.
//
// Go contract strengthenings:
//   - The narrow reader call count, exact native JSON bytes, and trailing newline are asserted; the newline is canonical Go encoding, not Axum byte parity.
func TestPredictionMarketsRouteIsPublicAndPreservesExactCatalog(t *testing.T) {
	reader := &recordingPredictionMarketsReader{
		markets: predictionMarketsFixture(),
	}
	handler := newPredictionMarketsServer(reader, "prediction-markets-public").Handler()
	response := predictionMarketsRequest(t, handler, http.MethodGet)

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q, want 200", response.Code, response.Body.String())
	}
	if got := response.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("content type=%q, want application/json", got)
	}
	if reader.calls != 1 {
		t.Fatalf("reader calls=%d, want 1", reader.calls)
	}
	const want = `[{"sourceVenue":"polymarket","marketKey":"election-2099","question":"Who wins the 2099 cup?","resolutionTime":"2099-07-30T12:34:56.123456+00:00","mutuallyExclusive":true,"status":"open","stageLabel":"final","stageOrdinal":2,"event":{"eventKey":"election-2099","title":"2099 Cup Winner","series":"world-cup","status":"open"},"legs":[{"symbol":"2099-CUP-ALPHA","displayName":"Alpha","outcomeIndex":0,"outcomeLabel":"Alpha","priceIncrement":"0.01","sizeIncrement":"1"},{"symbol":"2099-CUP-BRAVO","displayName":"Bravo","outcomeIndex":1,"outcomeLabel":"Bravo","priceIncrement":"0.01","sizeIncrement":"1"}]},{"sourceVenue":"polymarket","marketKey":"season-2099","question":"Who wins the 2099 season?","mutuallyExclusive":true,"status":"open","event":{"eventKey":"season-2099","title":"2099 Season Winner","status":"open"},"legs":[{"symbol":"2099-SEASON-ALPHA","displayName":"Alpha","outcomeIndex":0,"outcomeLabel":"Alpha","priceIncrement":"0.01","sizeIncrement":"1"}]},{"sourceVenue":"polymarket","marketKey":"binary-2099","question":"Will rain fall?","mutuallyExclusive":false,"status":"open","legs":[{"symbol":"RAIN-YES","displayName":"Yes","outcomeIndex":0,"outcomeLabel":"Yes","priceIncrement":"0.001","sizeIncrement":"1"},{"symbol":"RAIN-NO","displayName":"No","outcomeIndex":1,"outcomeLabel":"No","priceIncrement":"0.001","sizeIncrement":"1"}]}]` + "\n"
	if got := response.Body.String(); got != want {
		t.Fatalf("body=%q, want %q", got, want)
	}
}

// TestPredictionMarketsRouteSerializesNilReaderResultAsNonNullArray keeps an
// empty public catalog distinct from a missing JSON value.
//
// Source contract:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	route declaration: apps/app/src/api/client/trading.rs:47-53
//	handler function: apps/app/src/api/client/trading.rs:148-162 (list_prediction_markets)
//	DTO declarations: shared/api/src/prediction.rs:7-45
//
// Source-preserved assertion:
//   - A successful empty catalog is represented by the public array DTO.
//
// Go contract strengthenings:
//   - A nil reader slice is normalized to [] rather than null, with the canonical native Go trailing newline.
func TestPredictionMarketsRouteSerializesNilReaderResultAsNonNullArray(t *testing.T) {
	reader := &recordingPredictionMarketsReader{}
	handler := newPredictionMarketsServer(reader, "prediction-markets-empty").Handler()
	response := predictionMarketsRequest(t, handler, http.MethodGet)

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q, want 200", response.Code, response.Body.String())
	}
	if got := response.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("content type=%q, want application/json", got)
	}
	if got := response.Body.String(); got != "[]\n" {
		t.Fatalf("body=%q, want non-null empty array", got)
	}
	if reader.calls != 1 {
		t.Fatalf("reader calls=%d, want 1", reader.calls)
	}
}

// TestPredictionMarketsRouteReconstructedServersReturnIdenticalBytes proves
// that rebuilding the edge with the same reader result does not alter bytes.
//
// Source contract:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	route declaration: apps/app/src/api/client/trading.rs:47-53
//	handler function: apps/app/src/api/client/trading.rs:148-162 (list_prediction_markets)
//	DTO declarations: shared/api/src/prediction.rs:7-45
//
// Source-preserved assertion:
//   - The same catalog values remain nested under the same public DTO fields.
//
// Go contract strengthenings:
//   - Reconstructed native servers must return byte-identical bodies across repeated reads; this is a determinism invariant, not an Axum byte-parity claim.
func TestPredictionMarketsRouteReconstructedServersReturnIdenticalBytes(t *testing.T) {
	var want []byte
	for attempt := 0; attempt < 20; attempt++ {
		reader := &recordingPredictionMarketsReader{
			markets: predictionMarketsFixture(),
		}
		handler := newPredictionMarketsServer(reader, "prediction-markets-repeat").Handler()
		response := predictionMarketsRequest(t, handler, http.MethodGet)
		if response.Code != http.StatusOK {
			t.Fatalf("attempt=%d status=%d body=%q, want 200", attempt, response.Code, response.Body.String())
		}
		if reader.calls != 1 {
			t.Fatalf("attempt=%d reader calls=%d, want 1", attempt, reader.calls)
		}
		body := append([]byte(nil), response.Body.Bytes()...)
		if attempt == 0 {
			want = body
			continue
		}
		if !bytes.Equal(body, want) {
			t.Fatalf("attempt=%d body=%q, want bytes %q", attempt, body, want)
		}
	}
}

// TestPredictionMarketsRouteFailsClosedWithStableOpaqueUnavailable proves
// that reader failures and a missing dependency never expose partial catalog
// rows, sensitive reader errors, or an unstable implementation detail.
//
// Source contract:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	route declaration: apps/app/src/api/client/trading.rs:47-53
//	handler function: apps/app/src/api/client/trading.rs:148-162 (list_prediction_markets)
//	DTO declarations: shared/api/src/prediction.rs:7-45
//
// Source-preserved assertion:
//   - The public handler returns no partial market DTO after a failed query.
//
// Go contract strengthenings:
//   - Reader errors and nil dependencies map to the same opaque 503, and the fixed request ID makes the error body stable without exposing implementation details.
func TestPredictionMarketsRouteFailsClosedWithStableOpaqueUnavailable(t *testing.T) {
	const wantBody = `{"code":"unavailable","message":"catalog unavailable","requestId":"prediction-markets-error"}` + "\n"
	tests := []struct {
		name       string
		reader     PredictionMarketsReader
		wantCalls  int
		mustNotSee []string
	}{
		{
			name: "partial rows and reader error",
			reader: &recordingPredictionMarketsReader{
				markets: predictionMarketsFixture(),
				err:     errors.New("database unavailable: secret-market-key"),
			},
			wantCalls: 1,
			mustNotSee: []string{
				"secret-market-key",
				"election-2099",
				"partial",
			},
		},
		{
			name:      "nil reader dependency",
			wantCalls: 0,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := newPredictionMarketsServer(test.reader, "prediction-markets-error").Handler()
			response := predictionMarketsRequest(t, handler, http.MethodGet)

			if response.Code != http.StatusServiceUnavailable {
				t.Fatalf("status=%d body=%q, want 503", response.Code, response.Body.String())
			}
			if got := response.Header().Get("Content-Type"); got != "application/json" {
				t.Fatalf("content type=%q, want application/json", got)
			}
			if got := response.Body.String(); got != wantBody {
				t.Fatalf("body=%q, want opaque %q", got, wantBody)
			}
			for _, forbidden := range test.mustNotSee {
				if strings.Contains(response.Body.String(), forbidden) {
					t.Fatalf("body=%q leaks forbidden value %q", response.Body.String(), forbidden)
				}
			}
			if test.reader != nil {
				reader, ok := test.reader.(*recordingPredictionMarketsReader)
				if !ok {
					t.Fatalf("reader type %T, want recording reader", test.reader)
				}
				if reader.calls != test.wantCalls {
					t.Fatalf("reader calls=%d, want %d", reader.calls, test.wantCalls)
				}
			}
		})
	}
}

// TestPredictionMarketsRoutePreservesAxumMethodSemantics keeps the source
// GET-only route's HEAD and method-not-allowed behavior exact.
//
// Source contract:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	route declaration: apps/app/src/api/client/trading.rs:47-53
//	handler function: apps/app/src/api/client/trading.rs:148-162 (list_prediction_markets)
//	DTO declarations: shared/api/src/prediction.rs:7-45
//	primary router source: axum 0.8.9 method_routing.rs:2771-2822,3092-3105
//
// Source-preserved assertions:
//   - Axum's get(handler) route calls the handler for HEAD, strips the body, and preserves response headers.
//   - Other methods return 405 with the exact comma-joined Allow value GET,HEAD and an empty fallback body.
//
// Go contract strengthening:
//   - Reader call counts and native response headers are asserted directly; OPTIONS is covered separately by the global CORS test below.
func TestPredictionMarketsRoutePreservesAxumMethodSemantics(t *testing.T) {
	t.Run("head calls reader and strips body", func(t *testing.T) {
		reader := &recordingPredictionMarketsReader{
			markets: predictionMarketsFixture(),
		}
		handler := newPredictionMarketsServer(reader, "prediction-markets-head").Handler()
		response := predictionMarketsRequest(t, handler, http.MethodHead)

		if response.Code != http.StatusOK {
			t.Fatalf("method=HEAD status=%d, want 200", response.Code)
		}
		if got := response.Header().Get("Content-Type"); got != "application/json" {
			t.Fatalf("method=HEAD content type=%q, want application/json", got)
		}
		if response.Body.Len() != 0 {
			t.Fatalf("method=HEAD body=%q, want stripped body", response.Body.String())
		}
		if reader.calls != 1 {
			t.Fatalf("method=HEAD reader calls=%d, want 1", reader.calls)
		}
	})

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			reader := &recordingPredictionMarketsReader{
				markets: predictionMarketsFixture(),
			}
			handler := newPredictionMarketsServer(reader, "prediction-markets-method").Handler()
			response := predictionMarketsRequest(t, handler, method)

			if response.Code != http.StatusMethodNotAllowed {
				t.Fatalf("method=%s status=%d body=%q, want 405", method, response.Code, response.Body.String())
			}
			if got := response.Header().Get("Allow"); got != "GET,HEAD" {
				t.Fatalf("method=%s Allow=%q, want GET,HEAD", method, got)
			}
			if response.Body.Len() != 0 {
				t.Fatalf("method=%s body=%q, want empty Axum fallback body", method, response.Body.String())
			}
			if reader.calls != 0 {
				t.Fatalf("method=%s reader calls=%d, want 0", method, reader.calls)
			}
		})
	}
}

// TestPredictionMarketsRouteLeavesOptionsToGlobalCORS keeps the route's
// OPTIONS behavior separate from Axum's per-path method router.
//
// Source contract:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	route declaration: apps/app/src/api/client/trading.rs:47-53
//	handler function: apps/app/src/api/client/trading.rs:148-162 (list_prediction_markets)
//	DTO declarations: shared/api/src/prediction.rs:7-45
//
// Source-preserved assertion:
//   - OPTIONS is not declared by the prediction-markets route.
//
// Go contract strengthening:
//   - The native edge's global CORS preflight remains 204 and never reaches the catalog reader.
func TestPredictionMarketsRouteLeavesOptionsToGlobalCORS(t *testing.T) {
	reader := &recordingPredictionMarketsReader{
		markets: predictionMarketsFixture(),
	}
	handler := newPredictionMarketsServer(reader, "prediction-markets-options").Handler()
	response := predictionMarketsRequest(t, handler, http.MethodOptions)

	if response.Code != http.StatusNoContent {
		t.Fatalf("method=OPTIONS status=%d body=%q, want 204", response.Code, response.Body.String())
	}
	if got := response.Header().Get("Access-Control-Allow-Methods"); got != "GET,POST,PUT,PATCH,DELETE,OPTIONS" {
		t.Fatalf("method=OPTIONS allow methods=%q, want global CORS list", got)
	}
	if got := response.Header().Get("Access-Control-Allow-Headers"); got != "Authorization,Content-Type,Idempotency-Key,X-API-Key" {
		t.Fatalf("method=OPTIONS allow headers=%q, want global CORS list", got)
	}
	if response.Body.Len() != 0 {
		t.Fatalf("method=OPTIONS body=%q, want empty preflight body", response.Body.String())
	}
	if reader.calls != 0 {
		t.Fatalf("method=OPTIONS reader calls=%d, want 0", reader.calls)
	}
}

func newPredictionMarketsServer(
	reader PredictionMarketsReader,
	requestID string,
) *Server {
	return NewServer(ServerConfig{
		PredictionMarkets: reader,
		RequestID: func() string {
			return requestID
		},
	})
}

func predictionMarketsRequest(
	t *testing.T,
	handler http.Handler,
	method string,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequestWithContext(
		t.Context(),
		method,
		"/v1/prediction-markets",
		nil,
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func predictionMarketsFixture() []PredictionMarketView {
	resolutionTime := "2099-07-30T12:34:56.123456+00:00"
	eventSeries := "world-cup"
	stageLabel := "final"
	stageOrdinal := 2
	return []PredictionMarketView{
		{
			SourceVenue:       "polymarket",
			MarketKey:         "election-2099",
			Question:          "Who wins the 2099 cup?",
			ResolutionTime:    &resolutionTime,
			MutuallyExclusive: true,
			Status:            "open",
			StageLabel:        &stageLabel,
			StageOrdinal:      &stageOrdinal,
			Event: &PredictionEventView{
				EventKey: "election-2099",
				Title:    "2099 Cup Winner",
				Series:   &eventSeries,
				Status:   "open",
			},
			Legs: []PredictionLegView{
				{
					Symbol:         "2099-CUP-ALPHA",
					DisplayName:    "Alpha",
					OutcomeIndex:   0,
					OutcomeLabel:   "Alpha",
					PriceIncrement: "0.01",
					SizeIncrement:  "1",
				},
				{
					Symbol:         "2099-CUP-BRAVO",
					DisplayName:    "Bravo",
					OutcomeIndex:   1,
					OutcomeLabel:   "Bravo",
					PriceIncrement: "0.01",
					SizeIncrement:  "1",
				},
			},
		},
		{
			SourceVenue:       "polymarket",
			MarketKey:         "season-2099",
			Question:          "Who wins the 2099 season?",
			MutuallyExclusive: true,
			Status:            "open",
			Event: &PredictionEventView{
				EventKey: "season-2099",
				Title:    "2099 Season Winner",
				Status:   "open",
			},
			Legs: []PredictionLegView{
				{
					Symbol:         "2099-SEASON-ALPHA",
					DisplayName:    "Alpha",
					OutcomeIndex:   0,
					OutcomeLabel:   "Alpha",
					PriceIncrement: "0.01",
					SizeIncrement:  "1",
				},
			},
		},
		{
			SourceVenue:       "polymarket",
			MarketKey:         "binary-2099",
			Question:          "Will rain fall?",
			MutuallyExclusive: false,
			Status:            "open",
			Legs: []PredictionLegView{
				{
					Symbol:         "RAIN-YES",
					DisplayName:    "Yes",
					OutcomeIndex:   0,
					OutcomeLabel:   "Yes",
					PriceIncrement: "0.001",
					SizeIncrement:  "1",
				},
				{
					Symbol:         "RAIN-NO",
					DisplayName:    "No",
					OutcomeIndex:   1,
					OutcomeLabel:   "No",
					PriceIncrement: "0.001",
					SizeIncrement:  "1",
				},
			},
		},
	}
}
