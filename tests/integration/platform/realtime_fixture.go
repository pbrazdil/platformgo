package platform

import "encoding/json"

type realtimeEnvelope struct {
	Type      string            `json:"type"`
	AccountID string            `json:"accountId"`
	Timestamp int64             `json:"timestamp"`
	Data      map[string]string `json:"data"`
}

type realtimeFixture struct {
	history map[string][]realtimeEnvelope
}

func newRealtimeFixture() *realtimeFixture {
	return &realtimeFixture{history: make(map[string][]realtimeEnvelope)}
}

func realtimeUserChannel(userID string) string { return "user:" + userID }

func (realtime *realtimeFixture) publish(channel string, envelope realtimeEnvelope) int64 {
	realtime.history[channel] = append(realtime.history[channel], envelope)
	return int64(len(realtime.history[channel]))
}

func (realtime *realtimeFixture) channelHistory(channel string) []realtimeEnvelope {
	return append([]realtimeEnvelope(nil), realtime.history[channel]...)
}

type realtimeTokenResponse struct {
	Token    string   `json:"token"`
	Channels []string `json:"channels"`
}

func (realtime *realtimeFixture) token(authenticated bool, userID string) (int, realtimeTokenResponse) {
	if !authenticated {
		return 401, realtimeTokenResponse{}
	}
	return 200, realtimeTokenResponse{
		Token:    "fixture-token-" + userID,
		Channels: []string{realtimeUserChannel(userID)},
	}
}

func marshalRealtimeEnvelope(envelope realtimeEnvelope) []byte {
	raw, err := json.Marshal(envelope)
	if err != nil {
		panic(err)
	}
	return raw
}
