package identity

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"
)

func mfaRequireStatus(t *testing.T, response mfaResponse, want int) {
	t.Helper()
	if response.Status != want {
		t.Fatalf("status=%d code=%q, want %d", response.Status, response.Code, want)
	}
}

func mfaRegisterAndLogin(t *testing.T, fixture *mfaFixture, login string) string {
	t.Helper()
	fixture.register(login, "admin-pw")
	response := fixture.login(login, "admin-pw", "")
	mfaRequireStatus(t, response, mfaStatusOK)
	return response.AccessToken
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/identity/e2e_admin_mfa.rs:15
//	test: enrol_confirm_then_required_at_login
func TestEnrolConfirmThenRequiredAtLogin(t *testing.T) {
	fixture := newMFAFixture()
	session := mfaRegisterAndLogin(t, fixture, "root")
	enroll := fixture.enroll(session)
	mfaRequireStatus(t, enroll, mfaStatusOK)
	if enroll.Secret == "" || !strings.HasPrefix(enroll.OTPAuthURI, "otpauth://") {
		t.Fatalf("enroll=%#v", enroll)
	}
	mfaRequireStatus(t, fixture.confirm(session, fixture.currentCode(enroll.Secret)), mfaStatusOK)
	mfaRequireStatus(t, fixture.login("root", "admin-pw", ""), mfaStatusUnauthorized)
	mfaRequireStatus(t, fixture.login("root", "admin-pw", fixture.currentCode(enroll.Secret)), mfaStatusOK)
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/identity/e2e_admin_mfa.rs:80
//	test: step_up_gates_broker_key_create_and_revoke
func TestStepUpGatesBrokerKeyCreateAndRevoke(t *testing.T) {
	fixture := newMFAFixture()
	session := mfaRegisterAndLogin(t, fixture, "root")
	fixture.grantSuper("root")
	blocked := fixture.createAPIKey(session, "lp-bridge")
	mfaRequireStatus(t, blocked, mfaStatusForbidden)
	if blocked.Code != "step_up_required" {
		t.Fatalf("code=%q", blocked.Code)
	}
	enroll := fixture.enroll(session)
	mfaRequireStatus(t, fixture.confirm(session, fixture.currentCode(enroll.Secret)), mfaStatusOK)
	stepped := fixture.login("root", "admin-pw", fixture.currentCode(enroll.Secret))
	mfaRequireStatus(t, stepped, mfaStatusOK)
	created := fixture.createAPIKey(stepped.AccessToken, "lp-bridge")
	mfaRequireStatus(t, created, mfaStatusCreated)
	if !strings.HasPrefix(created.KeyID, "urn:xb:apikey:") {
		t.Fatalf("key id=%q", created.KeyID)
	}
	revoked := fixture.revokeAPIKey(stepped.AccessToken, created.KeyID)
	mfaRequireStatus(t, revoked, mfaStatusOK)
	if !revoked.Revoked {
		t.Fatal("expected revoked=true")
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/identity/e2e_admin_mfa.rs:195
//	test: totp_code_is_single_use_no_replay
func TestTOTPCodeIsSingleUseNoReplay(t *testing.T) {
	fixture := newMFAFixture()
	session := mfaRegisterAndLogin(t, fixture, "root")
	enroll := fixture.enroll(session)
	code := fixture.currentCode(enroll.Secret)
	mfaRequireStatus(t, fixture.confirm(session, code), mfaStatusOK)
	mfaRequireStatus(t, fixture.login("root", "admin-pw", code), mfaStatusOK)
	mfaRequireStatus(t, fixture.login("root", "admin-pw", code), mfaStatusUnauthorized)
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/identity/e2e_admin_mfa.rs:259
//	test: reenroll_over_confirmed_factor_requires_step_up
func TestReenrollOverConfirmedFactorRequiresStepUp(t *testing.T) {
	fixture := newMFAFixture()
	session := mfaRegisterAndLogin(t, fixture, "root")
	enroll := fixture.enroll(session)
	code := fixture.currentCode(enroll.Secret)
	mfaRequireStatus(t, fixture.confirm(session, code), mfaStatusOK)
	blocked := fixture.enroll(session)
	mfaRequireStatus(t, blocked, mfaStatusForbidden)
	if blocked.Code != "step_up_required" {
		t.Fatalf("code=%q", blocked.Code)
	}
	stepped := fixture.login("root", "admin-pw", code)
	mfaRequireStatus(t, stepped, mfaStatusOK)
	mfaRequireStatus(t, fixture.enroll(stepped.AccessToken), mfaStatusOK)
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/identity/e2e_admin_mfa.rs:338
//	test: step_up_endpoint_elevates_without_relogin
func TestStepUpEndpointElevatesWithoutRelogin(t *testing.T) {
	fixture := newMFAFixture()
	session := mfaRegisterAndLogin(t, fixture, "stepper")
	fixture.grantSuper("stepper")
	enroll := fixture.enroll(session)
	code := fixture.currentCode(enroll.Secret)
	mfaRequireStatus(t, fixture.confirm(session, code), mfaStatusOK)
	blocked := fixture.createAPIKey(session, "blocked")
	mfaRequireStatus(t, blocked, mfaStatusForbidden)
	if blocked.Code != "step_up_required" {
		t.Fatalf("code=%q", blocked.Code)
	}
	mfaRequireStatus(t, fixture.stepUp(session, "000000"), mfaStatusUnauthorized)
	stepped := fixture.stepUp(session, code)
	mfaRequireStatus(t, stepped, mfaStatusOK)
	login, elevated, err := mfaParseSessionPayload(stepped.AccessToken)
	if err != nil || login != "stepper" || !elevated {
		t.Fatalf("payload login=%q elevated=%t err=%v", login, elevated, err)
	}
	mfaRequireStatus(t, fixture.stepUp(session, code), mfaStatusUnauthorized)
	mfaRequireStatus(t, fixture.createAPIKey(stepped.AccessToken, "unblocked"), mfaStatusCreated)
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/identity/e2e_mfa_secret_at_rest.rs:38
//	test: enroll_stores_ciphertext_not_raw_seed
func TestEnrollStoresCiphertextNotRawSeed(t *testing.T) {
	fixture := newMFAFixture()
	session := mfaRegisterAndLogin(t, fixture, "root")
	enroll := fixture.enroll(session)
	rawSeed, err := hex.DecodeString(enroll.Secret)
	if err != nil {
		t.Fatal(err)
	}
	if len(rawSeed) != 20 {
		t.Fatalf("seed length=%d, want 20", len(rawSeed))
	}
	stored := fixture.admins["root"].factor.secretEnc
	if bytes.Equal(stored, rawSeed) {
		t.Fatal("secret_enc must not be the plaintext seed")
	}
	if !mfaIsSealed(stored) || stored[0] != mfaEnvelopeV1 || len(stored) <= len(rawSeed) {
		t.Fatalf("stored envelope length=%d version=%d", len(stored), stored[0])
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/identity/e2e_mfa_secret_at_rest.rs:99
//	test: enroll_confirm_login_round_trips_under_encryption
func TestEnrollConfirmLoginRoundTripsUnderEncryption(t *testing.T) {
	fixture := newMFAFixture()
	session := mfaRegisterAndLogin(t, fixture, "root")
	enroll := fixture.enroll(session)
	code := fixture.currentCode(enroll.Secret)
	mfaRequireStatus(t, fixture.confirm(session, code), mfaStatusOK)
	mfaRequireStatus(t, fixture.login("root", "admin-pw", code), mfaStatusOK)
	mfaRequireStatus(t, fixture.login("root", "admin-pw", ""), mfaStatusUnauthorized)
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/identity/e2e_mfa_secret_at_rest.rs:162
//	test: legacy_plaintext_seed_remains_verifiable_and_backfills
func TestLegacyPlaintextSeedRemainsVerifiableAndBackfills(t *testing.T) {
	fixture := newMFAFixture()
	fixture.register("root", "admin-pw")
	rawSeed := []byte("01234567890123456789")
	secretHex := hex.EncodeToString(rawSeed)
	fixture.seedLegacyFactor("root", rawSeed)
	if !bytes.Equal(fixture.admins["root"].factor.secretEnc, rawSeed) {
		t.Fatal("factor was not seeded as bare plaintext")
	}
	mfaRequireStatus(t, fixture.login("root", "admin-pw", fixture.currentCode(secretHex)), mfaStatusOK)
	if count := fixture.backfillLegacyFactors(); count != 1 {
		t.Fatalf("resealed=%d, want 1", count)
	}
	stored := fixture.admins["root"].factor.secretEnc
	opened, err := fixture.mfaOpen(stored)
	if err != nil || !mfaIsSealed(stored) || !bytes.Equal(opened, rawSeed) {
		t.Fatalf("sealed=%t opened=%x err=%v", mfaIsSealed(stored), opened, err)
	}
	fixture.advanceTOTPWindow()
	mfaRequireStatus(t, fixture.login("root", "admin-pw", fixture.currentCode(secretHex)), mfaStatusOK)
	if count := fixture.backfillLegacyFactors(); count != 0 {
		t.Fatalf("idempotent reseal count=%d, want 0", count)
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/identity/e2e_signing_key_at_rest.rs:11
//	test: signing_key_is_sealed_at_rest_and_unseals_to_sign
func TestSigningKeyIsSealedAtRestAndUnsealsToSign(t *testing.T) {
	fixture := newMFAFixture()
	stored := fixture.signingKeyEnc
	if !mfaIsSealed(stored) || stored[0] != mfaEnvelopeV1 {
		t.Fatalf("signing key is not a v1 sealed envelope")
	}
	if strings.Contains(string(stored), "PRIVATE KEY") {
		t.Fatal("stored bytes contain a plaintext PEM")
	}
	privatePEM, err := fixture.mfaOpen(stored)
	if err != nil || !strings.Contains(string(privatePEM), "BEGIN PRIVATE KEY") {
		t.Fatalf("unsealed PEM invalid: err=%v", err)
	}
	fixture.register("root", "admin-pw")
	login := fixture.login("root", "admin-pw", "")
	mfaRequireStatus(t, login, mfaStatusOK)
	if !fixture.verifyAccessToken(login.AccessToken) {
		t.Fatal("unsealed signing key did not produce a verifiable access token")
	}
}
