package auth

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// sessionTTL is the absolute lifetime of a freshly-issued session token.
// renewAfter is the threshold at which a request triggers re-issuance —
// i.e. tokens slide forward by 24-hour increments inside a 7-day window.
const (
	sessionTTL = 7 * 24 * time.Hour
	renewAfter = 24 * time.Hour
)

// Claims is the parsed form of a session token. We carry only what the
// middleware needs — subject, username, and the timestamps that drive
// renewal decisions.
type Claims struct {
	Subject  string
	Username string
	IssuedAt time.Time
	Expires  time.Time
}

// SessionSigner issues and verifies HS256 JWTs. It owns no state besides
// the secret; callers should construct exactly one and reuse it across
// the request lifecycle.
type SessionSigner struct {
	secret []byte
}

// NewSessionSigner constructs a SessionSigner. The secret should be at
// least 32 bytes; loadConfig validates length at startup.
func NewSessionSigner(secret []byte) *SessionSigner {
	return &SessionSigner{secret: secret}
}

// Issue mints a JWT for (userID, username) anchored at `now`. The token
// expires at now + sessionTTL.
func (s *SessionSigner) Issue(userID, username string, now time.Time) (string, error) {
	claims := jwt.MapClaims{
		"sub": userID,
		"usr": username,
		"iat": now.Unix(),
		"exp": now.Add(sessionTTL).Unix(),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return tok.SignedString(s.secret)
}

// Verify parses a token and validates signature + expiry against `now`.
// Returns the parsed claims on success.
//
// Verify uses jwt.WithTimeFunc so callers can supply a deterministic
// clock in tests; the real middleware passes time.Now().
func (s *SessionSigner) Verify(token string, now time.Time) (*Claims, error) {
	parsed, err := jwt.Parse(token, func(t *jwt.Token) (any, error) {
		// Reject anything that isn't HS256 — e.g. a forged token claiming
		// "alg":"none" or asking us to verify with an asymmetric algorithm.
		if t.Method != jwt.SigningMethodHS256 {
			return nil, errors.New("unexpected signing method")
		}
		return s.secret, nil
	}, jwt.WithTimeFunc(func() time.Time { return now }))
	if err != nil {
		return nil, err
	}
	mc, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		return nil, errors.New("unexpected claims type")
	}
	sub, _ := mc["sub"].(string)
	usr, _ := mc["usr"].(string)
	iat, _ := mc["iat"].(float64)
	exp, _ := mc["exp"].(float64)
	return &Claims{
		Subject:  sub,
		Username: usr,
		IssuedAt: time.Unix(int64(iat), 0).UTC(),
		Expires:  time.Unix(int64(exp), 0).UTC(),
	}, nil
}

// ShouldRenew reports whether the middleware should re-issue the cookie.
// True when the token's iat is more than renewAfter (24h) ago. The
// effect is a sliding 7-day window: any visit within 7 days of the last
// activity refreshes the cookie.
func (s *SessionSigner) ShouldRenew(issuedAt, now time.Time) bool {
	return now.Sub(issuedAt) > renewAfter
}
