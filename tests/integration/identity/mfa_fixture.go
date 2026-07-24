package identity

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"strconv"
	"strings"
)

const (
	mfaStatusOK           = 200
	mfaStatusCreated      = 201
	mfaStatusUnauthorized = 401
	mfaStatusForbidden    = 403
	mfaEnvelopeV1         = byte(1)
)

type mfaResponse struct {
	Status      int
	Code        string
	Secret      string
	OTPAuthURI  string
	AccessToken string
	KeyID       string
	Revoked     bool
}

type mfaFactor struct {
	secretEnc       []byte
	confirmed       bool
	lastUsedCounter uint64
	hasUsedCode     bool
}

type mfaAdmin struct {
	password string
	super    bool
	factor   *mfaFactor
}

type mfaSession struct {
	login  string
	stepUp bool
}

type mfaFixture struct {
	admins         map[string]*mfaAdmin
	sessions       map[string]mfaSession
	apiKeys        map[string]bool
	dek            []byte
	counter        uint64
	envelopeSerial uint64
	sessionSerial  uint64
	keySerial      uint64
	signingKeyEnc  []byte
	signingPublic  ed25519.PublicKey
}

func newMFAFixture() *mfaFixture {
	dekDigest := sha256.Sum256([]byte("platformgo identity fixture factor DEK"))
	signingSeed := sha256.Sum256([]byte("platformgo identity fixture signing key"))
	signingPrivate := ed25519.NewKeyFromSeed(signingSeed[:])
	privateDER, err := x509.MarshalPKCS8PrivateKey(signingPrivate)
	if err != nil {
		panic(err)
	}
	privatePEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER})
	fixture := &mfaFixture{
		admins:        make(map[string]*mfaAdmin),
		sessions:      make(map[string]mfaSession),
		apiKeys:       make(map[string]bool),
		dek:           append([]byte(nil), dekDigest[:]...),
		counter:       1_000_000,
		signingPublic: append(ed25519.PublicKey(nil), signingPrivate.Public().(ed25519.PublicKey)...),
	}
	fixture.signingKeyEnc = fixture.mfaSeal(privatePEM)
	return fixture
}

func (fixture *mfaFixture) register(login, password string) {
	fixture.admins[login] = &mfaAdmin{password: password}
}

func (fixture *mfaFixture) grantSuper(login string) {
	fixture.admins[login].super = true
}

func (fixture *mfaFixture) mfaSeal(plaintext []byte) []byte {
	block, err := aes.NewCipher(fixture.dek)
	if err != nil {
		panic(err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		panic(err)
	}
	fixture.envelopeSerial++
	nonce := make([]byte, aead.NonceSize())
	binary.BigEndian.PutUint64(nonce[len(nonce)-8:], fixture.envelopeSerial)
	sealed := aead.Seal(nil, nonce, plaintext, []byte{mfaEnvelopeV1})
	envelope := make([]byte, 1+len(nonce)+len(sealed))
	envelope[0] = mfaEnvelopeV1
	copy(envelope[1:], nonce)
	copy(envelope[1+len(nonce):], sealed)
	return envelope
}

func (fixture *mfaFixture) mfaOpen(envelope []byte) ([]byte, error) {
	block, err := aes.NewCipher(fixture.dek)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if !mfaIsSealed(envelope) || len(envelope) < 1+aead.NonceSize()+aead.Overhead() {
		return nil, fmt.Errorf("invalid sealed envelope")
	}
	nonce := envelope[1 : 1+aead.NonceSize()]
	return aead.Open(nil, nonce, envelope[1+aead.NonceSize():], []byte{mfaEnvelopeV1})
}

func mfaIsSealed(value []byte) bool {
	return len(value) > 1 && value[0] == mfaEnvelopeV1
}

func (fixture *mfaFixture) enroll(sessionToken string) mfaResponse {
	session, admin, ok := fixture.mfaAuthenticated(sessionToken)
	if !ok {
		return mfaResponse{Status: mfaStatusUnauthorized}
	}
	if admin.factor != nil && admin.factor.confirmed && !session.stepUp {
		return mfaResponse{Status: mfaStatusForbidden, Code: "step_up_required"}
	}
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s:totp:%d", session.login, fixture.envelopeSerial)))
	secret := append([]byte(nil), digest[:20]...)
	admin.factor = &mfaFactor{secretEnc: fixture.mfaSeal(secret)}
	secretHex := hex.EncodeToString(secret)
	return mfaResponse{
		Status:     mfaStatusOK,
		Secret:     secretHex,
		OTPAuthURI: "otpauth://totp/platform:" + session.login + "?secret=" + base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(secret),
	}
}

func (fixture *mfaFixture) confirm(sessionToken, code string) mfaResponse {
	_, admin, ok := fixture.mfaAuthenticated(sessionToken)
	if !ok || admin.factor == nil || !fixture.mfaCodeValid(admin.factor, code) {
		return mfaResponse{Status: mfaStatusUnauthorized}
	}
	admin.factor.confirmed = true
	return mfaResponse{Status: mfaStatusOK}
}

func (fixture *mfaFixture) login(login, password, otp string) mfaResponse {
	admin, ok := fixture.admins[login]
	if !ok || admin.password != password {
		return mfaResponse{Status: mfaStatusUnauthorized}
	}
	stepped := false
	if admin.factor != nil && admin.factor.confirmed {
		if otp == "" || !fixture.mfaConsumeCode(admin.factor, otp) {
			return mfaResponse{Status: mfaStatusUnauthorized}
		}
		stepped = true
	}
	return fixture.mfaNewSession(login, stepped)
}

func (fixture *mfaFixture) stepUp(sessionToken, otp string) mfaResponse {
	session, admin, ok := fixture.mfaAuthenticated(sessionToken)
	if !ok || admin.factor == nil || !admin.factor.confirmed || !fixture.mfaConsumeCode(admin.factor, otp) {
		return mfaResponse{Status: mfaStatusUnauthorized}
	}
	return fixture.mfaNewSession(session.login, true)
}

func (fixture *mfaFixture) createAPIKey(sessionToken, name string) mfaResponse {
	session, admin, ok := fixture.mfaAuthenticated(sessionToken)
	if !ok {
		return mfaResponse{Status: mfaStatusUnauthorized}
	}
	if !admin.super {
		return mfaResponse{Status: mfaStatusForbidden}
	}
	if !session.stepUp {
		return mfaResponse{Status: mfaStatusForbidden, Code: "step_up_required"}
	}
	fixture.keySerial++
	id := fmt.Sprintf("urn:xb:apikey:%d", fixture.keySerial)
	fixture.apiKeys[id] = false
	_ = name
	return mfaResponse{Status: mfaStatusCreated, KeyID: id}
}

func (fixture *mfaFixture) revokeAPIKey(sessionToken, id string) mfaResponse {
	session, admin, ok := fixture.mfaAuthenticated(sessionToken)
	if !ok {
		return mfaResponse{Status: mfaStatusUnauthorized}
	}
	if !admin.super || !session.stepUp {
		return mfaResponse{Status: mfaStatusForbidden, Code: "step_up_required"}
	}
	if _, exists := fixture.apiKeys[id]; !exists {
		return mfaResponse{Status: 404}
	}
	fixture.apiKeys[id] = true
	return mfaResponse{Status: mfaStatusOK, Revoked: true}
}

func (fixture *mfaFixture) mfaNewSession(login string, stepped bool) mfaResponse {
	fixture.sessionSerial++
	payload := fmt.Sprintf("%s:%t:%d", login, stepped, fixture.sessionSerial)
	privateKey, err := fixture.mfaSigningPrivateKey()
	if err != nil {
		panic(err)
	}
	signature := ed25519.Sign(privateKey, []byte(payload))
	token := base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." +
		base64.RawURLEncoding.EncodeToString(signature)
	fixture.sessions[token] = mfaSession{login: login, stepUp: stepped}
	return mfaResponse{Status: mfaStatusOK, AccessToken: token}
}

func (fixture *mfaFixture) mfaAuthenticated(token string) (mfaSession, *mfaAdmin, bool) {
	session, ok := fixture.sessions[token]
	if !ok || !fixture.verifyAccessToken(token) {
		return mfaSession{}, nil, false
	}
	admin := fixture.admins[session.login]
	return session, admin, admin != nil
}

func (fixture *mfaFixture) verifyAccessToken(token string) bool {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return false
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	return err == nil && ed25519.Verify(fixture.signingPublic, payload, signature)
}

func (fixture *mfaFixture) mfaSigningPrivateKey() (ed25519.PrivateKey, error) {
	privatePEM, err := fixture.mfaOpen(fixture.signingKeyEnc)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(privatePEM)
	if block == nil || block.Type != "PRIVATE KEY" {
		return nil, fmt.Errorf("invalid PKCS#8 PEM")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	privateKey, ok := key.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("not an Ed25519 private key")
	}
	return privateKey, nil
}

func (fixture *mfaFixture) currentCode(secretHex string) string {
	secret, err := hex.DecodeString(secretHex)
	if err != nil {
		return ""
	}
	return mfaTOTPCode(secret, fixture.counter)
}

func mfaTOTPCode(secret []byte, counter uint64) string {
	var message [8]byte
	binary.BigEndian.PutUint64(message[:], counter)
	mac := hmac.New(sha1.New, secret)
	_, _ = mac.Write(message[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	value := (binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff) % 1_000_000
	return fmt.Sprintf("%06d", value)
}

func (fixture *mfaFixture) mfaCodeValid(factor *mfaFactor, code string) bool {
	secret, err := fixture.mfaFactorSecret(factor)
	return err == nil && hmac.Equal([]byte(mfaTOTPCode(secret, fixture.counter)), []byte(code))
}

func (fixture *mfaFixture) mfaConsumeCode(factor *mfaFactor, code string) bool {
	if factor.hasUsedCode && factor.lastUsedCounter == fixture.counter {
		return false
	}
	if !fixture.mfaCodeValid(factor, code) {
		return false
	}
	factor.lastUsedCounter = fixture.counter
	factor.hasUsedCode = true
	return true
}

func (fixture *mfaFixture) mfaFactorSecret(factor *mfaFactor) ([]byte, error) {
	if mfaIsSealed(factor.secretEnc) {
		return fixture.mfaOpen(factor.secretEnc)
	}
	return append([]byte(nil), factor.secretEnc...), nil
}

func (fixture *mfaFixture) seedLegacyFactor(login string, rawSeed []byte) {
	fixture.admins[login].factor = &mfaFactor{
		secretEnc: append([]byte(nil), rawSeed...),
		confirmed: true,
	}
}

func (fixture *mfaFixture) backfillLegacyFactors() int {
	count := 0
	for _, admin := range fixture.admins {
		if admin.factor != nil && !mfaIsSealed(admin.factor.secretEnc) {
			admin.factor.secretEnc = fixture.mfaSeal(admin.factor.secretEnc)
			count++
		}
	}
	return count
}

func (fixture *mfaFixture) advanceTOTPWindow() {
	fixture.counter++
}

func mfaParseSessionPayload(token string) (string, bool, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return "", false, fmt.Errorf("invalid token")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", false, err
	}
	fields := strings.Split(string(decoded), ":")
	if len(fields) != 3 {
		return "", false, fmt.Errorf("invalid token payload")
	}
	stepped, err := strconv.ParseBool(fields[1])
	return fields[0], stepped, err
}
