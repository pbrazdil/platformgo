package centrifugo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/upcomers-org/platformgo/internal/edge"
)

var eventTypes = map[string]struct{}{
	"order.created":            {},
	"order.updated":            {},
	"order.cancelled":          {},
	"order.triggered":          {},
	"order.filled":             {},
	"order.partially_filled":   {},
	"position.opened":          {},
	"position.updated":         {},
	"position.closed":          {},
	"position.liquidated":      {},
	"position.take_profit.hit": {},
	"position.stop_loss.hit":   {},
	"account.updated":          {},
	"trade.created":            {},
}

// PublishError classifies whether a failed committed publication can be
// retried without operator intervention.
type PublishError struct {
	err       error
	retryable bool
}

func (publishErr *PublishError) Error() string { return publishErr.err.Error() }
func (publishErr *PublishError) Unwrap() error { return publishErr.err }

// IsRetryablePublishError reports only failures explicitly classified as
// transient or ambiguous after a request may have reached Centrifugo.
func IsRetryablePublishError(err error) bool {
	var publishErr *PublishError
	return errors.As(err, &publishErr) && publishErr.retryable
}

func classifiedPublishError(retryable bool, err error) error {
	return &PublishError{err: err, retryable: retryable}
}

// Clock makes token expiry explicit at the delivery edge.
type Clock interface {
	Now() time.Time
}

type wallClock struct{}

func (wallClock) Now() time.Time { return time.Now() }

// Config contains the Centrifugo HTTP API and connection-token settings.
type Config struct {
	APIURL      string
	APIKey      string
	TokenSecret []byte
	TokenTTL    time.Duration
	HTTPClient  *http.Client
	Clock       Clock
}

// Gateway publishes committed outbox events and issues scoped connection
// tokens. It never participates in monetary decisions.
type Gateway struct {
	apiURL      string
	apiKey      string
	tokenSecret []byte
	tokenTTL    time.Duration
	httpClient  *http.Client
	clock       Clock
}

// New validates and constructs the delivery adapter.
func New(config Config) (*Gateway, error) {
	if len(config.TokenSecret) < 32 {
		return nil, errors.New("centrifugo: token secret must contain at least 32 bytes")
	}
	if config.TokenTTL <= 0 {
		return nil, errors.New("centrifugo: positive token TTL is required")
	}
	gateway, err := NewPublisher(config)
	if err != nil {
		return nil, err
	}
	gateway.tokenSecret = append([]byte(nil), config.TokenSecret...)
	gateway.tokenTTL = config.TokenTTL
	return gateway, nil
}

// NewPublisher constructs the publish/health-only adapter without loading
// connection-token signing authority into a delivery worker.
func NewPublisher(config Config) (*Gateway, error) {
	parsed, err := url.Parse(config.APIURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, errors.New("centrifugo: absolute API URL is required")
	}
	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{Timeout: 5 * time.Second}
	}
	if config.Clock == nil {
		config.Clock = wallClock{}
	}
	return &Gateway{
		apiURL:     strings.TrimRight(config.APIURL, "/"),
		apiKey:     config.APIKey,
		httpClient: config.HTTPClient,
		clock:      config.Clock,
	}, nil
}

// Envelope is the compatible realtime JSON shape plus additive recovery
// metadata required by the Go replacement.
type Envelope struct {
	Type          string          `json:"type"`
	AccountID     string          `json:"accountId"`
	Timestamp     int64           `json:"timestamp"`
	Data          json.RawMessage `json:"data"`
	EventID       string          `json:"eventId"`
	SchemaVersion uint32          `json:"schemaVersion"`
	Sequence      uint64          `json:"sequence"`
}

// Validate rejects malformed or internally named wire events.
func (envelope Envelope) Validate() error {
	if _, ok := eventTypes[envelope.Type]; !ok {
		return fmt.Errorf("centrifugo: invalid event type %q", envelope.Type)
	}
	if !strings.HasPrefix(envelope.AccountID, "urn:xb:account:") {
		return errors.New("centrifugo: canonical account URN is required")
	}
	if envelope.EventID == "" || envelope.SchemaVersion == 0 || envelope.Sequence == 0 {
		return errors.New("centrifugo: event ID, schema version, and sequence are required")
	}
	if !json.Valid(envelope.Data) {
		return errors.New("centrifugo: event data must be JSON")
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	for _, forbidden := range []string{"SHAREDNETTING", "SHAREDHEDGING", "BBOOK"} {
		if bytes.Contains(encoded, []byte(forbidden)) {
			return fmt.Errorf("centrifugo: event exposes internal name %q", forbidden)
		}
	}
	return nil
}

// Publish sends one already-committed event to Centrifugo's HTTP API.
func (gateway *Gateway) Publish(
	ctx context.Context,
	channel string,
	envelope Envelope,
) error {
	if gateway == nil {
		return classifiedPublishError(false, errors.New("centrifugo: gateway is nil"))
	}
	if channel == "" || strings.Count(channel, ":") != 1 {
		return classifiedPublishError(
			false,
			errors.New("centrifugo: canonical channel is required"),
		)
	}
	if err := envelope.Validate(); err != nil {
		return classifiedPublishError(false, err)
	}
	body, err := json.Marshal(struct {
		Channel string   `json:"channel"`
		Data    Envelope `json:"data"`
	}{Channel: channel, Data: envelope})
	if err != nil {
		return classifiedPublishError(
			false,
			fmt.Errorf("centrifugo: encode publication: %w", err),
		)
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		gateway.apiURL+"/api/publish",
		bytes.NewReader(body),
	)
	if err != nil {
		return classifiedPublishError(
			false,
			fmt.Errorf("centrifugo: build publish request: %w", err),
		)
	}
	request.Header.Set("content-type", "application/json")
	if gateway.apiKey != "" {
		request.Header.Set("x-api-key", gateway.apiKey)
	}
	response, err := gateway.httpClient.Do(request)
	if err != nil {
		return classifiedPublishError(
			true,
			fmt.Errorf("centrifugo: publish: %w", err),
		)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		detail, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		retryable := response.StatusCode == http.StatusRequestTimeout ||
			response.StatusCode == http.StatusTooManyRequests ||
			response.StatusCode >= http.StatusInternalServerError
		return classifiedPublishError(
			retryable,
			fmt.Errorf(
				"centrifugo: publish HTTP %d: %s",
				response.StatusCode,
				strings.TrimSpace(string(detail)),
			),
		)
	}
	detail, err := io.ReadAll(io.LimitReader(response.Body, 4096))
	if err != nil {
		return classifiedPublishError(
			true,
			fmt.Errorf("centrifugo: read publish response: %w", err),
		)
	}
	var acknowledgment struct {
		Result map[string]json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(detail, &acknowledgment); err != nil {
		return classifiedPublishError(
			true,
			fmt.Errorf("centrifugo: decode publish response: %w", err),
		)
	}
	if acknowledgment.Error != nil {
		return classifiedPublishError(
			retryableApplicationCode(acknowledgment.Error.Code),
			fmt.Errorf(
				"centrifugo: publish error %d: %s",
				acknowledgment.Error.Code,
				acknowledgment.Error.Message,
			),
		)
	}
	if acknowledgment.Result == nil {
		return classifiedPublishError(
			true,
			errors.New("centrifugo: publish response omitted result"),
		)
	}
	return nil
}

func retryableApplicationCode(code int) bool {
	switch code {
	case 100, 111, 113:
		return true
	default:
		return false
	}
}

// Healthy checks the public Centrifugo health endpoint.
func (gateway *Gateway) Healthy(ctx context.Context) error {
	if gateway == nil {
		return errors.New("centrifugo: gateway is nil")
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		gateway.apiURL+"/health",
		nil,
	)
	if err != nil {
		return err
	}
	response, err := gateway.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("centrifugo: health: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("centrifugo: health HTTP %d", response.StatusCode)
	}
	return nil
}

// IssueClientToken implements edge.RealtimeTokenIssuer.
func (gateway *Gateway) IssueClientToken(
	_ context.Context,
	principal edge.Principal,
) (edge.RealtimeToken, error) {
	if gateway == nil {
		return edge.RealtimeToken{}, errors.New("centrifugo: gateway is nil")
	}
	if len(gateway.tokenSecret) < 32 || gateway.tokenTTL <= 0 {
		return edge.RealtimeToken{}, errors.New(
			"centrifugo: token issuer is not configured",
		)
	}
	if principal.Audience != edge.AudienceClient || principal.Subject == "" {
		return edge.RealtimeToken{}, edge.ErrUnauthorized
	}
	channel := UserChannel(principal.Subject)
	if channel == "" {
		return edge.RealtimeToken{}, edge.ErrUnauthorized
	}
	channels := []string{channel}
	claims := connectionClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: principal.Subject,
			ExpiresAt: jwt.NewNumericDate(
				gateway.clock.Now().Add(gateway.tokenTTL),
			),
		},
		Channels: channels,
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	token.Header["typ"] = "JWT"
	signed, err := token.SignedString(gateway.tokenSecret)
	if err != nil {
		return edge.RealtimeToken{}, err
	}
	return edge.RealtimeToken{Token: signed, Channels: channels}, nil
}

type connectionClaims struct {
	jwt.RegisteredClaims
	Channels []string `json:"channels"`
}

// UserChannel preserves the legacy namespace:id shape and strips a URN prefix.
func UserChannel(subject string) string {
	const prefix = "urn:xb:user:"
	if !strings.HasPrefix(subject, prefix) ||
		!validUserChannelSuffix(subject[len(prefix):]) {
		return ""
	}
	return "user:" + subject[len(prefix):]
}

func validUserChannelSuffix(value string) bool {
	if len(value) == 0 || len(value) > 250 {
		return false
	}
	for index := range len(value) {
		character := value[index]
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			character == '.' ||
			character == '_' ||
			character == '~' ||
			character == '-' {
			continue
		}
		return false
	}
	return true
}

// NeedsSnapshot reports a sequence gap that must reload authoritative state.
func NeedsSnapshot(lastSequence, nextSequence uint64) bool {
	return nextSequence == 0 || nextSequence != lastSequence+1
}
