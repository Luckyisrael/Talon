package auth

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log"
	"net/mail"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrUnauthorized       = errors.New("unauthorized")
)

const (
	jwtExpiry  = 24 * time.Hour
	issuer     = "seekersstudio"

	// minSecretLen is the shortest JWT_SECRET worth accepting. HS256 keys shorter
	// than this are brute-forceable offline from a single captured token.
	minSecretLen = 32

	MinPasswordLength = 10
	MaxPasswordLength = 200
)

type Claims struct {
	UserID int64  `json:"user_id"`
	Email  string `json:"email"`
	jwt.RegisteredClaims
}

type Service struct {
	secret []byte
	// ephemeral is true when no JWT_SECRET was configured, so sessions do not
	// survive a restart.
	ephemeral bool
}

// New builds the auth service.
//
// A missing JWT_SECRET used to be papered over with a random one and a warning.
// That fails open in the worst way: a restart silently signs everyone out, and
// two instances behind a load balancer reject each other's tokens with no
// indication why. In production it is now fatal; in development it stays a
// warning so nobody has to configure anything to run the thing locally.
func New() *Service {
	secret := strings.TrimSpace(os.Getenv("JWT_SECRET"))
	production := isProduction()

	if secret == "" {
		if production {
			log.Fatal("JWT_SECRET is not set. Generate one with `openssl rand -hex 32` and set it in the environment.")
		}
		buf := make([]byte, 32)
		if _, err := rand.Read(buf); err != nil {
			log.Fatal("could not generate a development JWT secret: ", err)
		}
		log.Print("WARNING: JWT_SECRET is not set — using a secret that lasts until this process exits. " +
			"Everyone will be signed out on restart. Set JWT_SECRET to fix this.")
		return &Service{secret: []byte(hex.EncodeToString(buf)), ephemeral: true}
	}

	if len(secret) < minSecretLen {
		if production {
			log.Fatalf("JWT_SECRET is only %d characters; use at least %d (`openssl rand -hex 32`).",
				len(secret), minSecretLen)
		}
		log.Printf("WARNING: JWT_SECRET is only %d characters — use at least %d in production.",
			len(secret), minSecretLen)
	}

	return &Service{secret: []byte(secret)}
}

func isProduction() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("ENV"))) {
	case "production", "prod", "staging":
		return true
	}
	return strings.EqualFold(strings.TrimSpace(os.Getenv("APP_ENV")), "production")
}

// Ephemeral reports whether tokens will be invalidated by a restart.
func (s *Service) Ephemeral() bool { return s.ephemeral }

// ValidateEmail checks that an address is one we can plausibly deliver to.
// Registration previously accepted anything at all, including the empty string.
func ValidateEmail(email string) (string, error) {
	email = strings.TrimSpace(email)
	if email == "" {
		return "", errors.New("enter your email address")
	}
	if len(email) > 254 {
		return "", errors.New("that email address is too long")
	}
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email {
		return "", errors.New("that does not look like an email address")
	}
	at := strings.LastIndexByte(email, '@')
	if !strings.Contains(email[at+1:], ".") {
		return "", errors.New("that does not look like an email address")
	}
	// Addresses are compared case-insensitively, so store them one way.
	return strings.ToLower(email), nil
}

func (s *Service) GenerateToken(userID int64, email string) (string, error) {
	claims := &Claims{
		UserID: userID,
		Email:  email,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    issuer,
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(jwtExpiry)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			NotBefore: jwt.NewNumericDate(time.Now()),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(s.secret)
}

// ValidateToken parses and verifies a token.
//
// The signing method is pinned. Without that, jwt/v5's keyfunc would be handed
// whatever algorithm the token's own header claimed, and this service would
// accept a token signed with a different method than it issues.
func (s *Service) ValidateToken(tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{},
		func(token *jwt.Token) (interface{}, error) {
			return s.secret, nil
		},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(issuer),
		jwt.WithExpirationRequired(),
	)
	if err != nil {
		return nil, ErrUnauthorized
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid || claims.UserID <= 0 {
		return nil, ErrUnauthorized
	}

	return claims, nil
}
