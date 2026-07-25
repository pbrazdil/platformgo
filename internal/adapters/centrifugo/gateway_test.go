package centrifugo

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/upcomers-org/platformgo/internal/edge"
)

type testClock struct{ value time.Time }

func (clock testClock) Now() time.Time { return clock.value }

// Ported from:
// platform: 50141367492be46ebf5623f6191a14b94af2f2bd
// source: apps/app/tests/it/realtime/e2e_gateway.rs:15
// test: realtime_gateway_json_event_publish_and_token
func TestRealtimeGatewayJSONEventPublishAndToken(t *testing.T) {
	var (
		mu           sync.Mutex
		publications []Envelope
	)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/publish":
			if request.Header.Get("x-api-key") != "api-key" {
				t.Error("missing API key")
			}
			var body struct {
				Channel string   `json:"channel"`
				Data    Envelope `json:"data"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body.Channel != "user:user-7" {
				t.Errorf("channel = %q", body.Channel)
			}
			mu.Lock()
			publications = append(publications, body.Data)
			mu.Unlock()
			writer.Header().Set("content-type", "application/json")
			_, _ = writer.Write([]byte(`{"result":{}}`))
		case "/health":
			writer.WriteHeader(http.StatusOK)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	secret := []byte("0123456789abcdef0123456789abcdef")
	now := time.Date(2026, time.July, 25, 12, 0, 0, 0, time.UTC)
	gateway, err := New(Config{
		APIURL: server.URL, APIKey: "api-key", TokenSecret: secret,
		TokenTTL: time.Hour, HTTPClient: server.Client(), Clock: testClock{value: now},
	})
	if err != nil {
		t.Fatal(err)
	}
	envelope := Envelope{
		Type: "order.updated", AccountID: "urn:xb:account:acct-7",
		Timestamp: 123, Data: json.RawMessage(`{"symbol":"BTC-PERP","status":"working"}`),
		EventID: "event-7", SchemaVersion: 1, Sequence: 7,
	}
	if publishErr := gateway.Publish(context.Background(), "user:user-7", envelope); publishErr != nil {
		t.Fatal(publishErr)
	}
	mu.Lock()
	got := append([]Envelope(nil), publications...)
	mu.Unlock()
	if len(got) != 1 || got[0].Type != "order.updated" ||
		got[0].AccountID != "urn:xb:account:acct-7" {
		t.Fatalf("publications = %#v", got)
	}
	if healthErr := gateway.Healthy(context.Background()); healthErr != nil {
		t.Fatal(healthErr)
	}

	token, err := gateway.IssueClientToken(context.Background(), edge.Principal{
		Subject: "urn:xb:user:user-7", Audience: edge.AudienceClient,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(token.Channels, []string{"user:user-7"}) {
		t.Fatalf("channels = %v", token.Channels)
	}
	claims := verifyToken(t, token.Token, secret)
	if claims.Subject != "urn:xb:user:user-7" || claims.ExpiresAt == nil ||
		claims.ExpiresAt.Unix() != now.Add(time.Hour).Unix() ||
		!reflect.DeepEqual(claims.Channels, token.Channels) {
		t.Fatalf("claims = %#v", claims)
	}
}

func TestEnvelopeRejectsInternalNamesAndInvalidSequences(t *testing.T) {
	valid := Envelope{
		Type: "order.updated", AccountID: "urn:xb:account:acct-7",
		Timestamp: 123, Data: json.RawMessage(`{"status":"working"}`),
		EventID: "event-7", SchemaVersion: 1, Sequence: 7,
	}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	invalid := valid
	invalid.Data = json.RawMessage(`{"mode":"BBOOK"}`)
	if err := invalid.Validate(); err == nil {
		t.Fatal("internal venue name accepted")
	}
	invalid = valid
	invalid.Sequence = 0
	if err := invalid.Validate(); err == nil {
		t.Fatal("zero sequence accepted")
	}
}

func TestSequenceGapRequiresAuthoritativeSnapshot(t *testing.T) {
	if NeedsSnapshot(7, 8) {
		t.Fatal("contiguous event requested snapshot")
	}
	for _, next := range []uint64{0, 7, 9} {
		if !NeedsSnapshot(7, next) {
			t.Fatalf("gap 7 -> %d did not request snapshot", next)
		}
	}
}

func TestDuplicatePublicationKeepsStableBusinessIdentity(t *testing.T) {
	var eventIDs []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body struct {
			Data Envelope `json:"data"`
		}
		_ = json.NewDecoder(request.Body).Decode(&body)
		eventIDs = append(eventIDs, body.Data.EventID)
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	gateway, err := New(Config{
		APIURL: server.URL, TokenSecret: []byte("0123456789abcdef0123456789abcdef"),
		TokenTTL: time.Hour, HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	event := Envelope{
		Type: "order.filled", AccountID: "urn:xb:account:a",
		Data:    json.RawMessage(`{"fillId":"fill-1"}`),
		EventID: "event-1", SchemaVersion: 1, Sequence: 1,
	}
	if err := gateway.Publish(context.Background(), "user:u", event); err != nil {
		t.Fatal(err)
	}
	if err := gateway.Publish(context.Background(), "user:u", event); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(eventIDs, []string{"event-1", "event-1"}) {
		t.Fatalf("event IDs = %v", eventIDs)
	}
}

type tokenClaims struct {
	jwt.RegisteredClaims
	Channels []string `json:"channels"`
}

func verifyToken(t *testing.T, token string, secret []byte) tokenClaims {
	t.Helper()
	var claims tokenClaims
	parsed, err := jwt.ParseWithClaims(
		token,
		&claims,
		func(candidate *jwt.Token) (any, error) {
			if candidate.Method != jwt.SigningMethodHS256 {
				t.Fatalf("algorithm = %s", candidate.Method.Alg())
			}
			return secret, nil
		},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithoutClaimsValidation(),
	)
	if err != nil || !parsed.Valid {
		t.Fatal(err)
	}
	return claims
}
