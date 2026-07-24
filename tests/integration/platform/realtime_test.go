package platform

import (
	"reflect"
	"strings"
	"testing"
)

// Ported from:
// platform: 50141367492be46ebf5623f6191a14b94af2f2bd
// source: apps/app/tests/it/realtime/e2e_gateway.rs:15
// test: realtime_gateway_json_event_publish_and_token
func TestRealtimeGatewayJSONEventPublishAndToken(t *testing.T) {
	realtime := newRealtimeFixture()
	const userID = "user-7"
	channel := realtimeUserChannel(userID)
	envelope := realtimeEnvelope{
		Type: "order.updated", AccountID: "urn:xb:account:acct-7", Timestamp: 123,
		Data: map[string]string{"symbol": "BTC-PERP", "status": "working"},
	}
	if offset := realtime.publish(channel, envelope); offset < 1 {
		t.Fatalf("offset = %d, want at least 1", offset)
	}
	history := realtime.channelHistory(channel)
	if len(history) == 0 || history[0].Type != "order.updated" ||
		!strings.HasPrefix(history[0].AccountID, "urn:xb:account:") {
		t.Fatalf("history = %#v", history)
	}
	raw := string(marshalRealtimeEnvelope(history[0]))
	for _, forbidden := range []string{"SHAREDNETTING", "SHAREDHEDGING", "BBOOK"} {
		if strings.Contains(raw, forbidden) {
			t.Errorf("serialized event exposes %q: %s", forbidden, raw)
		}
	}
	status, token := realtime.token(true, userID)
	if status != 200 || token.Token == "" ||
		!reflect.DeepEqual(token.Channels, []string{channel}) {
		t.Fatalf("token response: status=%d body=%#v", status, token)
	}
	status, _ = realtime.token(false, userID)
	if status != 401 {
		t.Fatalf("unauthenticated token status = %d, want 401", status)
	}
}
