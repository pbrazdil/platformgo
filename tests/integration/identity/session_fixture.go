package identity

import (
	"fmt"
	"net/http"
	"strings"
)

const (
	sessionWebOrigin    = "https://admin.web.test"
	sessionRefreshName  = "uzo_admin_refresh"
	sessionRefreshPath  = "/admin/v1/auth"
	sessionCorrectLogin = "root"
	sessionCorrectPW    = "admin-pw"
)

type sessionClock struct {
	second int64
}

func (clock *sessionClock) advance(seconds int64) {
	clock.second += seconds
}

type sessionThrottleConfig struct {
	lockoutThreshold int
	lockoutBaseSecs  int64
	lockoutCapSecs   int64
	rateLimitMax     int
	rateLimitWindow  int64
}

type sessionLoginState struct {
	failures    int
	lockedUntil int64
}

type sessionRateWindow struct {
	start int64
	count int
}

type sessionResponse struct {
	status  int
	headers http.Header
	body    map[string]string
}

type sessionThrottleFixture struct {
	clock       *sessionClock
	config      sessionThrottleConfig
	users       map[string]string
	loginStates map[string]sessionLoginState
	rateWindows map[string]sessionRateWindow
	audit       map[string]int
}

func newSessionThrottleFixture(config sessionThrottleConfig) *sessionThrottleFixture {
	return &sessionThrottleFixture{
		clock:       &sessionClock{},
		config:      config,
		users:       make(map[string]string),
		loginStates: make(map[string]sessionLoginState),
		rateWindows: make(map[string]sessionRateWindow),
		audit:       make(map[string]int),
	}
}

func (fixture *sessionThrottleFixture) register(login, password string) {
	fixture.users[login] = password
}

func (fixture *sessionThrottleFixture) login(ip, login, password string) sessionResponse {
	if fixture.rateLimited(ip) {
		return sessionResponse{
			status:  http.StatusTooManyRequests,
			headers: http.Header{"Retry-After": []string{fixture.retryAfter(ip)}},
		}
	}

	expected, exists := fixture.users[login]
	state := fixture.loginStates[login]
	locked := fixture.clock.second < state.lockedUntil
	if !exists || password != expected {
		fixture.audit["login.failure"]++
		if exists {
			state.failures++
			if state.failures >= fixture.config.lockoutThreshold {
				window := fixture.config.lockoutBaseSecs
				if fixture.config.lockoutCapSecs > 0 && window > fixture.config.lockoutCapSecs {
					window = fixture.config.lockoutCapSecs
				}
				state.lockedUntil = fixture.clock.second + window
			}
			fixture.loginStates[login] = state
		}
		// Wrong credentials stay indistinguishable even after account lockout.
		return sessionResponse{status: http.StatusUnauthorized, headers: make(http.Header)}
	}
	if locked {
		fixture.audit["login.failure"]++
		return sessionResponse{
			status:  http.StatusTooManyRequests,
			headers: http.Header{"Retry-After": []string{fmt.Sprint(state.lockedUntil - fixture.clock.second)}},
		}
	}

	delete(fixture.loginStates, login)
	return sessionResponse{status: http.StatusOK, headers: make(http.Header)}
}

func (fixture *sessionThrottleFixture) rateLimited(ip string) bool {
	window := fixture.rateWindows[ip]
	if fixture.clock.second-window.start >= fixture.config.rateLimitWindow {
		window = sessionRateWindow{start: fixture.clock.second}
	}
	window.count++
	fixture.rateWindows[ip] = window
	return window.count > fixture.config.rateLimitMax
}

func (fixture *sessionThrottleFixture) retryAfter(ip string) string {
	window := fixture.rateWindows[ip]
	remaining := fixture.config.rateLimitWindow - (fixture.clock.second - window.start)
	if remaining < 0 {
		remaining = 0
	}
	return fmt.Sprint(remaining)
}

type sessionToken struct {
	family  int
	used    bool
	revoked bool
}

type sessionAuthFixture struct {
	nextToken       int
	nextFamily      int
	tokens          map[string]*sessionToken
	familyRevoked   map[int]bool
	currentByCaller map[string]string
	audit           map[string]int
}

func newSessionAuthFixture() *sessionAuthFixture {
	return &sessionAuthFixture{
		tokens:          make(map[string]*sessionToken),
		familyRevoked:   make(map[int]bool),
		currentByCaller: make(map[string]string),
		audit:           make(map[string]int),
	}
}

func (fixture *sessionAuthFixture) login(caller string) sessionResponse {
	fixture.nextFamily++
	refresh := fixture.mint(fixture.nextFamily)
	fixture.currentByCaller[caller] = refresh
	return sessionResponse{
		status:  http.StatusOK,
		headers: make(http.Header),
		body: map[string]string{
			"accessToken":  fixture.accessToken(),
			"refreshToken": refresh,
		},
	}
}

func (fixture *sessionAuthFixture) refresh(caller, refresh string) sessionResponse {
	token, exists := fixture.tokens[refresh]
	if !exists || token.revoked || fixture.familyRevoked[token.family] {
		return sessionResponse{status: http.StatusUnauthorized, headers: make(http.Header)}
	}
	if token.used {
		fixture.revokeFamily(token.family)
		fixture.audit["session.revoke.success"]++
		return sessionResponse{status: http.StatusUnauthorized, headers: make(http.Header)}
	}

	token.used = true
	rotated := fixture.mint(token.family)
	fixture.currentByCaller[caller] = rotated
	return sessionResponse{
		status:  http.StatusOK,
		headers: make(http.Header),
		body: map[string]string{
			"accessToken":  fixture.accessToken(),
			"refreshToken": rotated,
		},
	}
}

func (fixture *sessionAuthFixture) logout(caller string) int {
	refresh, exists := fixture.currentByCaller[caller]
	if !exists {
		return http.StatusUnauthorized
	}
	fixture.revokeFamily(fixture.tokens[refresh].family)
	delete(fixture.currentByCaller, caller)
	fixture.audit["session.revoke.success"]++
	return http.StatusOK
}

func (fixture *sessionAuthFixture) mint(family int) string {
	fixture.nextToken++
	value := fmt.Sprintf("refresh-%d", fixture.nextToken)
	fixture.tokens[value] = &sessionToken{family: family}
	return value
}

func (fixture *sessionAuthFixture) accessToken() string {
	return fmt.Sprintf("access-%d", fixture.nextToken)
}

func (fixture *sessionAuthFixture) revokeFamily(family int) {
	fixture.familyRevoked[family] = true
	for _, token := range fixture.tokens {
		if token.family == family {
			token.revoked = true
		}
	}
}

type sessionWebFixture struct {
	auth *sessionAuthFixture
}

func newSessionWebFixture() *sessionWebFixture {
	return &sessionWebFixture{auth: newSessionAuthFixture()}
}

func (fixture *sessionWebFixture) login(origin string) sessionResponse {
	response := fixture.auth.login(sessionCorrectLogin)
	if origin == "" {
		return response
	}
	refresh := response.body["refreshToken"]
	delete(response.body, "refreshToken")
	response.headers.Set("Access-Control-Allow-Credentials", "true")
	response.headers.Set("Access-Control-Allow-Origin", origin)
	response.headers.Set("Set-Cookie", sessionCookie(refresh))
	return response
}

func (fixture *sessionWebFixture) refresh(origin, cookie, bodyRefresh string) sessionResponse {
	refresh := bodyRefresh
	web := origin != ""
	if web {
		refresh = sessionCookieToken(cookie)
	}
	if refresh == "" {
		return sessionResponse{status: http.StatusUnauthorized, headers: make(http.Header)}
	}
	response := fixture.auth.refresh(sessionCorrectLogin, refresh)
	if response.status != http.StatusOK || !web {
		return response
	}
	rotated := response.body["refreshToken"]
	delete(response.body, "refreshToken")
	response.headers.Set("Set-Cookie", sessionCookie(rotated))
	return response
}

func sessionCookie(token string) string {
	return fmt.Sprintf("%s=%s; HttpOnly; Path=%s; SameSite=Lax", sessionRefreshName, token, sessionRefreshPath)
}

func sessionCookieToken(cookie string) string {
	for _, part := range strings.Split(cookie, ";") {
		name, value, found := strings.Cut(strings.TrimSpace(part), "=")
		if found && name == sessionRefreshName {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
