package edge

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const (
	brokerFundingKeyPrefix         = "xbk_brokerfunding"
	brokerFundingNoScopeKeyPrefix  = "xbk_brokerfundingnoscope"
	brokerFundingWildcardKeyPrefix = "xbk_brokerfundingwildcard"
)

func brokerFundingAPIKey(prefix string) string {
	return prefix + "." + "secret"
}

func TestBrokerFundingRouteIsAnAuthenticatedCompatibilityBoundary(t *testing.T) {
	authenticator := brokerFundingAuthenticator(t)
	handler := NewServer(ServerConfig{
		Authenticator: authenticator,
		RequestID:     func() string { return "broker-funding-red" },
	}).Handler()
	const (
		canonicalPath = "/broker/v1/accounts/urn:xb:account:" +
			"00000000-0000-4000-8000-000000000901/funding"
		malformedPath = "/broker/v1/accounts/urn:xb:account:not-a-uuid/funding"
	)
	for _, test := range []struct {
		name   string
		path   string
		apiKey string
		status int
	}{
		{
			name:   "authentication dominates malformed path",
			path:   malformedPath,
			status: http.StatusUnauthorized,
		},
		{
			name:   "scope dominates malformed path",
			path:   malformedPath,
			apiKey: brokerFundingAPIKey(brokerFundingNoScopeKeyPrefix),
			status: http.StatusForbidden,
		},
		{
			name:   "canonical parsing follows scope",
			path:   malformedPath,
			apiKey: brokerFundingAPIKey(brokerFundingKeyPrefix),
			status: http.StatusBadRequest,
		},
		{
			name:   "query parsing precedes storage",
			path:   canonicalPath + "?cursor=not-base64",
			apiKey: brokerFundingAPIKey(brokerFundingKeyPrefix),
			status: http.StatusBadRequest,
		},
		{
			name:   "valid request reaches storage boundary",
			path:   canonicalPath,
			apiKey: brokerFundingAPIKey(brokerFundingKeyPrefix),
			status: http.StatusServiceUnavailable,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, test.path, nil)
			if test.apiKey != "" {
				request.Header.Set("x-api-key", test.apiKey)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf(
					"status=%d body=%q, want %d",
					response.Code,
					response.Body.String(),
					test.status,
				)
			}
		})
	}
}

func brokerFundingAuthenticator(t *testing.T) Authenticator {
	t.Helper()
	authenticator, err := NewHMACAuthenticator(HMACAuthenticatorConfig{
		ClientTokenSecret: []byte("broker-funding-client-secret-0123456789"),
		BrokerCredentials: []BrokerCredential{
			{
				Prefix:     brokerFundingKeyPrefix,
				SecretHash: HashBrokerSecret("secret"),
				Subject:    "urn:xb:apikey:broker-funding",
				Tenant:     "urn:xb:tenant:broker-funding",
				Scopes:     []string{"accounts:read"},
			},
			{
				Prefix:     brokerFundingNoScopeKeyPrefix,
				SecretHash: HashBrokerSecret("secret"),
				Subject:    "urn:xb:apikey:broker-funding-no-scope",
				Tenant:     "urn:xb:tenant:broker-funding",
				Scopes:     []string{"accounts:write"},
			},
			{
				Prefix:     brokerFundingWildcardKeyPrefix,
				SecretHash: HashBrokerSecret("secret"),
				Subject:    "urn:xb:apikey:broker-funding-wildcard",
				Tenant:     "urn:xb:tenant:broker-funding",
				Scopes:     []string{"*"},
			},
		},
	})
	if err != nil {
		t.Fatalf("create broker funding authenticator: %v", err)
	}
	return authenticator
}

type recordingBrokerFundingReader struct {
	calls     int
	tenant    string
	accountID string
	params    PageParams
	page      FundingPage
	err       error
}

func (reader *recordingBrokerFundingReader) BrokerFunding(
	_ context.Context,
	tenant string,
	accountID string,
	params PageParams,
) (FundingPage, error) {
	reader.calls++
	reader.tenant = tenant
	reader.accountID = accountID
	reader.params = params
	return reader.page, reader.err
}

func TestBrokerFundingRoutePassesTenantAccountAndCurrentGoPageParams(
	t *testing.T,
) {
	accountLogin := int64(4901)
	total := int64(1)
	nextCursor := "next-page"
	reader := &recordingBrokerFundingReader{
		page: FundingPage{
			Items: []FundingView{{
				FundingID:              "00000000-0000-4000-8000-000000000902",
				Symbol:                 "BTC-PERP",
				PositionID:             "30303030303030302d303030302d343030302d383030302d303030303030303030393033",
				PositionSignedQuantity: "-2.5",
				OraclePrice:            "100000",
				FundingRate:            "0.0000125",
				FundingAmount:          "3.125",
				Currency:               "USDC",
				FundingTime:            "2026-07-30T12:00:00Z",
				AccountLogin:           &accountLogin,
			}},
			NextCursor: &nextCursor,
			Total:      &total,
		},
	}
	handler := NewServer(ServerConfig{
		Authenticator: brokerFundingAuthenticator(t),
		BrokerFunding: reader,
		RequestID:     func() string { return "broker-funding-success" },
	}).Handler()
	rawCursor := "1785412800000000000:00000000-0000-4000-8000-000000000902"
	cursor := base64.RawURLEncoding.EncodeToString([]byte(rawCursor))
	request := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		"/broker/v1/accounts/urn:xb:account:"+
			"00000000-0000-4000-8000-000000000901/funding"+
			"?limit=-7&direction=sideways&cursor="+cursor,
		nil,
	)
	request.Header.Set("x-api-key", brokerFundingAPIKey(brokerFundingWildcardKeyPrefix))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s, want 200", response.Code, response.Body.String())
	}
	if reader.calls != 1 {
		t.Fatalf("reader calls=%d, want 1", reader.calls)
	}
	if reader.tenant != "urn:xb:tenant:broker-funding" {
		t.Fatalf("tenant=%q, want authenticated tenant", reader.tenant)
	}
	if reader.tenant == "urn:xb:apikey:broker-funding-wildcard" {
		t.Fatal("reader received API-key subject as tenant authority")
	}
	if reader.accountID !=
		"urn:xb:account:00000000-0000-4000-8000-000000000901" {
		t.Fatalf("account=%q, want canonical account URN", reader.accountID)
	}
	if reader.params != (PageParams{
		Limit:     -7,
		Cursor:    cursor,
		Direction: "sideways",
	}) {
		t.Fatalf("params=%#v, want current-Go page params", reader.params)
	}
	const want = `{"items":[{"fundingId":"00000000-0000-4000-8000-000000000902",` +
		`"symbol":"BTC-PERP",` +
		`"positionId":"30303030303030302d303030302d343030302d383030302d303030303030303030393033",` +
		`"positionSignedQty":"-2.5","oraclePrice":"100000","fundingRate":"0.0000125",` +
		`"fundingAmount":"3.125","currency":"USDC","fundingTime":"2026-07-30T12:00:00Z",` +
		`"accountLogin":4901}],"nextCursor":"next-page","total":1}` + "\n"
	if response.Body.String() != want {
		t.Fatalf("body=%q, want %q", response.Body.String(), want)
	}
}

func TestBrokerFundingRouteRejectsMalformedInputBeforeRead(t *testing.T) {
	reader := &recordingBrokerFundingReader{}
	handler := NewServer(ServerConfig{
		Authenticator: brokerFundingAuthenticator(t),
		BrokerFunding: reader,
		RequestID:     func() string { return "broker-funding-invalid" },
	}).Handler()
	validPath := "/broker/v1/accounts/urn:xb:account:" +
		"00000000-0000-4000-8000-000000000901/funding"
	for _, path := range []string{
		"/broker/v1/accounts/00000000-0000-4000-8000-000000000901/funding",
		"/broker/v1/accounts/urn:xb:account:00000000-0000-4000-8000-00000000090A/funding",
		validPath + "?limit=999999999999999999999",
		validPath + "?cursor=not-base64",
		validPath + "?cursor=" + base64.RawURLEncoding.EncodeToString([]byte("missing-colon")),
		validPath + "?cursor=" + base64.RawURLEncoding.EncodeToString([]byte(
			"not-an-int:00000000-0000-4000-8000-000000000902",
		)),
		validPath + "?cursor=" + base64.RawURLEncoding.EncodeToString([]byte(
			"1785412800000000000:00000000-0000-4000-8000-00000000090A",
		)),
	} {
		request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
		request.Header.Set("x-api-key", brokerFundingAPIKey(brokerFundingKeyPrefix))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf(
				"path=%q status=%d body=%s, want 400",
				path,
				response.Code,
				response.Body.String(),
			)
		}
		if reader.calls != 0 {
			t.Fatalf("path=%q reached reader %d times", path, reader.calls)
		}
	}
}

func TestBrokerFundingRouteMapsReaderErrorsWithoutLeakingPartialPage(
	t *testing.T,
) {
	for _, test := range []struct {
		name   string
		err    error
		status int
		body   string
	}{
		{
			name:   "tenant denial",
			err:    ErrForbidden,
			status: http.StatusForbidden,
			body: `{"code":"forbidden","message":"forbidden","requestId":"broker-funding-error"}` +
				"\n",
		},
		{
			name:   "invalid cursor race",
			err:    ErrInvalidRequest,
			status: http.StatusBadRequest,
			body: `{"code":"invalid_request","message":"invalid funding page","requestId":"broker-funding-error"}` +
				"\n",
		},
		{
			name:   "storage failure",
			err:    errors.New("contains-sensitive-funding-id"),
			status: http.StatusServiceUnavailable,
			body: `{"code":"unavailable","message":"trading views unavailable","requestId":"broker-funding-error"}` +
				"\n",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			total := int64(91)
			reader := &recordingBrokerFundingReader{
				page: FundingPage{
					Items: []FundingView{{
						FundingID:     "must-not-leak",
						FundingAmount: "sensitive-amount",
					}},
					Total: &total,
				},
				err: test.err,
			}
			handler := NewServer(ServerConfig{
				Authenticator: brokerFundingAuthenticator(t),
				BrokerFunding: reader,
				RequestID:     func() string { return "broker-funding-error" },
			}).Handler()
			request := httptest.NewRequestWithContext(
				t.Context(),
				http.MethodGet,
				"/broker/v1/accounts/urn:xb:account:"+
					"00000000-0000-4000-8000-000000000901/funding",
				nil,
			)
			request.Header.Set("x-api-key", brokerFundingAPIKey(brokerFundingKeyPrefix))
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.status || response.Body.String() != test.body {
				t.Fatalf(
					"status=%d body=%q, want status=%d body=%q",
					response.Code,
					response.Body.String(),
					test.status,
					test.body,
				)
			}
			if strings.Contains(response.Body.String(), "must-not-leak") ||
				strings.Contains(response.Body.String(), "sensitive") ||
				strings.Contains(response.Body.String(), "91") {
				t.Fatalf("response leaked page or storage detail: %s", response.Body.String())
			}
		})
	}
}

func TestBrokerFundingRouteReturnsNonNullEmptyItems(t *testing.T) {
	reader := &recordingBrokerFundingReader{}
	handler := NewServer(ServerConfig{
		Authenticator: brokerFundingAuthenticator(t),
		BrokerFunding: reader,
	}).Handler()
	request := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		"/broker/v1/accounts/urn:xb:account:"+
			"00000000-0000-4000-8000-000000000901/funding",
		nil,
	)
	request.Header.Set("x-api-key", brokerFundingAPIKey(brokerFundingKeyPrefix))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != `{"items":[]}`+"\n" {
		t.Fatalf(
			"status=%d body=%q, want 200 non-null items",
			response.Code,
			response.Body.String(),
		)
	}
}
