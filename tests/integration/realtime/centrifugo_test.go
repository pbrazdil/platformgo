package realtime_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/upcomers-org/platformgo/internal/adapters/centrifugo"
	"github.com/upcomers-org/platformgo/internal/edge"
)

// Ported from:
// platform: 50141367492be46ebf5623f6191a14b94af2f2bd
// source: apps/app/tests/it/realtime/e2e_gateway.rs:15
// test: realtime_gateway_json_event_publish_and_token
func TestRealtimeGatewayJSONEventPublishAndTokenAgainstCentrifugo(t *testing.T) {
	apiURL := os.Getenv("PLATFORMGO_TEST_CENTRIFUGO_URL")
	if apiURL == "" {
		t.Skip("PLATFORMGO_TEST_CENTRIFUGO_URL is not configured")
	}
	apiKey := os.Getenv("PLATFORMGO_TEST_CENTRIFUGO_API_KEY")
	secret := []byte(os.Getenv("PLATFORMGO_TEST_CENTRIFUGO_TOKEN_SECRET"))
	gateway, err := centrifugo.New(centrifugo.Config{
		APIURL: apiURL, APIKey: apiKey, TokenSecret: secret,
		TokenTTL: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := gateway.Healthy(ctx); err != nil {
		t.Fatal(err)
	}
	channel := centrifugo.UserChannel("urn:xb:user:user-7")
	envelope := centrifugo.Envelope{
		Type: "order.updated", AccountID: "urn:xb:account:acct-7",
		Timestamp: 123,
		Data: json.RawMessage(
			`{"symbol":"BTC-PERP","status":"working"}`,
		),
		EventID: "event-phase3-7", SchemaVersion: 1, Sequence: 7,
	}
	if err := gateway.Publish(ctx, channel, envelope); err != nil {
		t.Fatal(err)
	}
	historyBody, err := json.Marshal(map[string]any{
		"channel": channel,
		"limit":   10,
	})
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		strings.TrimRight(apiURL, "/")+"/api/history",
		bytes.NewReader(historyBody),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("content-type", "application/json")
	request.Header.Set("x-api-key", apiKey)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	var history struct {
		Result struct {
			Publications []struct {
				Data centrifugo.Envelope `json:"data"`
			} `json:"publications"`
		} `json:"result"`
	}
	if err := json.NewDecoder(response.Body).Decode(&history); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK ||
		len(history.Result.Publications) == 0 {
		t.Fatalf("history status=%d body=%#v", response.StatusCode, history)
	}
	got := history.Result.Publications[0].Data
	if got.EventID != envelope.EventID ||
		got.Sequence != envelope.Sequence ||
		got.Type != envelope.Type ||
		got.AccountID != "urn:xb:account:acct-7" {
		t.Fatalf("history envelope=%#v", got)
	}
	wireEnvelope, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, internalName := range []string{"SHAREDNETTING", "SHAREDHEDGING", "BBOOK"} {
		if bytes.Contains(wireEnvelope, []byte(internalName)) {
			t.Fatalf("history exposed internal name %q: %s", internalName, wireEnvelope)
		}
	}
	token, err := gateway.IssueClientToken(ctx, edge.Principal{
		Subject: "urn:xb:user:user-7", Audience: edge.AudienceClient,
	})
	if err != nil {
		t.Fatal(err)
	}
	if token.Token == "" || len(token.Channels) != 1 ||
		token.Channels[0] != channel {
		t.Fatalf("token=%#v", token)
	}
}
