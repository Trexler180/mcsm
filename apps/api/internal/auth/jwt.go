package auth

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Access tokens name who issued them, who they are for, and what they are, so a
// future token type signed with the same key can never be mistaken for one.
const (
	tokenIssuer     = "mcsm-api"
	tokenAudience   = "mcsm-panel"
	tokenTypeAccess = "access"

	// AccessTokenTTL is how long an access token is valid.
	AccessTokenTTL = 15 * time.Minute
)

type Claims struct {
	UserID string `json:"sub"`
	Email  string `json:"email"`
	Role   string `json:"role"`
	// SessionID is the refresh-token session this access token belongs to.
	// Ending the session (logout, revocation, a password reset) ends the token
	// with it on the next request, rather than up to 15 minutes later.
	SessionID string `json:"sid,omitempty"`
	Type      string `json:"typ"`
	jwt.RegisteredClaims
}

var (
	ErrInvalidToken = errors.New("invalid token")
	ErrExpiredToken = errors.New("token expired")
)

// IssueSessionToken mints an access token bound to a session.
func IssueSessionToken(secret, userID, email, role, sessionID string) (string, error) {
	now := time.Now()
	claims := Claims{
		UserID:    userID,
		Email:     email,
		Role:      role,
		SessionID: sessionID,
		Type:      tokenTypeAccess,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    tokenIssuer,
			Audience:  jwt.ClaimStrings{tokenAudience},
			ExpiresAt: jwt.NewNumericDate(now.Add(AccessTokenTTL)),
			IssuedAt:  jwt.NewNumericDate(now),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
}

// IssueAccessToken mints an access token with no session. Production sign-in
// always uses IssueSessionToken; a router configured WithSessionCheck refuses
// session-less tokens. This form exists for tests and tools that exercise a
// router without sessions.
func IssueAccessToken(secret, userID, email, role string) (string, error) {
	return IssueSessionToken(secret, userID, email, role, "")
}

func ParseAccessToken(secret, tokenStr string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, ErrInvalidToken
		}
		return []byte(secret), nil
	},
		jwt.WithIssuer(tokenIssuer),
		jwt.WithAudience(tokenAudience),
		jwt.WithExpirationRequired(),
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
	)
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrExpiredToken
		}
		return nil, ErrInvalidToken
	}
	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid || claims.Type != tokenTypeAccess || claims.UserID == "" {
		return nil, ErrInvalidToken
	}
	return claims, nil
}
