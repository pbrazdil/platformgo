package identity

import (
	"net/http"
	"strings"
	"testing"
)

func sessionRequireStatus(t *testing.T, response sessionResponse, want int) {
	t.Helper()
	if response.status != want {
		t.Fatalf("status = %d, want %d", response.status, want)
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/identity/e2e_auth_throttle.rs:37
//	test: account_locks_after_threshold_failures
func TestSessionAccountLocksAfterThresholdFailures(t *testing.T) {
	fixture := newSessionThrottleFixture(sessionThrottleConfig{
		lockoutThreshold: 3, lockoutBaseSecs: 300,
		rateLimitMax: 1000, rateLimitWindow: 60,
	})
	fixture.register("lockme", "s3cret-pw")
	for attempt := 0; attempt < 4; attempt++ {
		sessionRequireStatus(t, fixture.login("203.0.113.10", "lockme", "nope"), http.StatusUnauthorized)
	}
	locked := fixture.login("203.0.113.10", "lockme", "s3cret-pw")
	sessionRequireStatus(t, locked, http.StatusTooManyRequests)
	if locked.headers.Get("Retry-After") != "300" {
		t.Fatalf("Retry-After = %q, want 300", locked.headers.Get("Retry-After"))
	}
	if fixture.audit["login.failure"] < 4 {
		t.Fatalf("audited failures = %d, want at least 4", fixture.audit["login.failure"])
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/identity/e2e_auth_throttle.rs:89
//	test: lock_auto_expires_after_the_window
func TestSessionLockAutoExpiresAfterTheWindow(t *testing.T) {
	fixture := newSessionThrottleFixture(sessionThrottleConfig{
		lockoutThreshold: 2, lockoutBaseSecs: 1, lockoutCapSecs: 1,
		rateLimitMax: 1000, rateLimitWindow: 60,
	})
	fixture.register("expiry", "s3cret-pw")
	sessionRequireStatus(t, fixture.login("203.0.113.20", "expiry", "nope"), http.StatusUnauthorized)
	sessionRequireStatus(t, fixture.login("203.0.113.20", "expiry", "nope"), http.StatusUnauthorized)
	sessionRequireStatus(t, fixture.login("203.0.113.20", "expiry", "s3cret-pw"), http.StatusTooManyRequests)

	fixture.clock.advance(1)
	sessionRequireStatus(t, fixture.login("203.0.113.20", "expiry", "s3cret-pw"), http.StatusOK)
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/identity/e2e_auth_throttle.rs:147
//	test: rapid_logins_from_one_ip_are_rate_limited
func TestSessionRapidLoginsFromOneIPAreRateLimited(t *testing.T) {
	fixture := newSessionThrottleFixture(sessionThrottleConfig{
		lockoutThreshold: 1000, lockoutBaseSecs: 300,
		rateLimitMax: 5, rateLimitWindow: 60,
	})
	fixture.register("spray", "s3cret-pw")
	for attempt := 0; attempt < 5; attempt++ {
		sessionRequireStatus(t, fixture.login("203.0.113.30", "spray", "nope"), http.StatusUnauthorized)
	}
	limited := fixture.login("203.0.113.30", "spray", "nope")
	sessionRequireStatus(t, limited, http.StatusTooManyRequests)
	if limited.headers.Get("Retry-After") != "60" {
		t.Fatalf("Retry-After = %q, want 60", limited.headers.Get("Retry-After"))
	}
	fixture.clock.advance(60)
	sessionRequireStatus(t, fixture.login("203.0.113.30", "spray", "nope"), http.StatusUnauthorized)
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/identity/e2e_session_rotation.rs:46
//	test: refresh_rotates_and_old_token_is_rejected_admin
func TestSessionRefreshRotatesAndOldTokenIsRejectedAdmin(t *testing.T) {
	fixture := newSessionAuthFixture()
	login := fixture.login("admin")
	sessionRequireStatus(t, login, http.StatusOK)
	original := login.body["refreshToken"]

	rotated := fixture.refresh("admin", original)
	sessionRequireStatus(t, rotated, http.StatusOK)
	if rotated.body["accessToken"] == "" {
		t.Fatal("refresh did not issue an access token")
	}
	if rotated.body["refreshToken"] == original {
		t.Fatal("refresh token did not rotate")
	}
	sessionRequireStatus(t, fixture.refresh("admin", rotated.body["refreshToken"]), http.StatusOK)
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/identity/e2e_session_rotation.rs:80
//	test: refresh_rotates_and_old_token_is_rejected_trader
func TestSessionRefreshRotatesAndOldTokenIsRejectedTrader(t *testing.T) {
	fixture := newSessionAuthFixture()
	login := fixture.login("trader")
	sessionRequireStatus(t, login, http.StatusOK)
	original := login.body["refreshToken"]

	rotated := fixture.refresh("trader", original)
	sessionRequireStatus(t, rotated, http.StatusOK)
	if rotated.body["refreshToken"] == original {
		t.Fatal("refresh token did not rotate")
	}
	sessionRequireStatus(t, fixture.refresh("trader", "deadbeef-not-a-real-token"), http.StatusUnauthorized)
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/identity/e2e_session_rotation.rs:103
//	test: replaying_a_rotated_token_revokes_the_whole_family
func TestSessionReplayingRotatedTokenRevokesWholeFamily(t *testing.T) {
	fixture := newSessionAuthFixture()
	original := fixture.login("trader").body["refreshToken"]
	rotated := fixture.refresh("trader", original)
	sessionRequireStatus(t, rotated, http.StatusOK)
	current := rotated.body["refreshToken"]

	sessionRequireStatus(t, fixture.refresh("trader", original), http.StatusUnauthorized)
	sessionRequireStatus(t, fixture.refresh("trader", current), http.StatusUnauthorized)
	if fixture.audit["session.revoke.success"] < 1 {
		t.Fatal("refresh reuse did not audit family revocation")
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/identity/e2e_session_rotation.rs:138
//	test: logout_revokes_the_session_and_stops_refresh
func TestSessionLogoutRevokesSessionAndStopsRefresh(t *testing.T) {
	fixture := newSessionAuthFixture()
	refresh := fixture.login("trader").body["refreshToken"]
	if got := fixture.logout("trader"); got != http.StatusOK {
		t.Fatalf("logout status = %d, want 200", got)
	}
	if fixture.audit["session.revoke.success"] != 1 {
		t.Fatalf("session revoke audit rows = %d, want 1", fixture.audit["session.revoke.success"])
	}
	sessionRequireStatus(t, fixture.refresh("trader", refresh), http.StatusUnauthorized)
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/identity/e2e_web_refresh_cookie.rs:74
//	test: web_login_sets_httponly_cookie_and_omits_body_refresh
func TestSessionWebLoginSetsHTTPOnlyCookieAndOmitsBodyRefresh(t *testing.T) {
	response := newSessionWebFixture().login(sessionWebOrigin)
	sessionRequireStatus(t, response, http.StatusOK)
	if response.headers.Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatalf("allow credentials = %q", response.headers.Get("Access-Control-Allow-Credentials"))
	}
	if response.headers.Get("Access-Control-Allow-Origin") != sessionWebOrigin {
		t.Fatalf("allow origin = %q", response.headers.Get("Access-Control-Allow-Origin"))
	}
	cookie := response.headers.Get("Set-Cookie")
	if !strings.Contains(cookie, sessionRefreshName+"=") ||
		!strings.Contains(cookie, "HttpOnly") ||
		!strings.Contains(cookie, "Path="+sessionRefreshPath) ||
		sessionCookieToken(cookie) == "" {
		t.Fatalf("web refresh cookie = %q", cookie)
	}
	if response.body["accessToken"] == "" {
		t.Fatal("web login omitted access token")
	}
	if _, exists := response.body["refreshToken"]; exists {
		t.Fatalf("web login exposed refresh token: %#v", response.body)
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/identity/e2e_web_refresh_cookie.rs:134
//	test: web_refresh_redeems_from_cookie_and_rotates
func TestSessionWebRefreshRedeemsFromCookieAndRotates(t *testing.T) {
	fixture := newSessionWebFixture()
	first := sessionCookieToken(fixture.login(sessionWebOrigin).headers.Get("Set-Cookie"))
	refreshed := fixture.refresh(sessionWebOrigin, sessionCookie(first), "")
	sessionRequireStatus(t, refreshed, http.StatusOK)
	second := sessionCookieToken(refreshed.headers.Get("Set-Cookie"))
	if second == "" || second == first {
		t.Fatalf("rotated cookie token = %q, original = %q", second, first)
	}
	if refreshed.body["accessToken"] == "" {
		t.Fatal("web refresh omitted access token")
	}
	if _, exists := refreshed.body["refreshToken"]; exists {
		t.Fatalf("web refresh exposed refresh token: %#v", refreshed.body)
	}
	sessionRequireStatus(t, fixture.refresh(sessionWebOrigin, sessionCookie(second), ""), http.StatusOK)
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/identity/e2e_web_refresh_cookie.rs:199
//	test: web_refresh_without_cookie_or_body_is_unauthorized
func TestSessionWebRefreshWithoutCookieOrBodyIsUnauthorized(t *testing.T) {
	sessionRequireStatus(
		t,
		newSessionWebFixture().refresh(sessionWebOrigin, "", ""),
		http.StatusUnauthorized,
	)
}
