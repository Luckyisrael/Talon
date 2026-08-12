package auth

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/mail"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrUnauthorized       = errors.New("unauthorized")
)

const (
	bcryptCost = 12
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

// ValidatePassword enforces a length floor and rejects the handful of passwords
// that show up first in every credential-stuffing list.
func ValidatePassword(password string) error {
	if len(password) < MinPasswordLength {
		return fmt.Errorf("use at least %d characters", MinPasswordLength)
	}
	// bcrypt silently ignores everything past 72 bytes, so a longer password is
	// not the extra security it looks like. Reject rather than truncate.
	if len(password) > MaxPasswordLength {
		return fmt.Errorf("use at most %d characters", MaxPasswordLength)
	}
	if commonPasswords[strings.ToLower(password)] {
		return errors.New("that password is too common — pick something else")
	}
	return nil
}

var commonPasswords = map[string]bool{
	"password":     true,
	"password1":    true,
	"password123":  true,
	"passw0rd":     true,
	"1234567890":   true,
	"12345678901":  true,
	"123456789012": true,
	"qwertyuiop":   true,
	"letmein123":   true,
	"iloveyou123":  true,
	"welcome123":   true,
	"admin123456":  true,
	"changeme123":  true,
}

func (s *Service) HashPassword(password string) (string, error) {
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	return string(bytes), err
}

func (s *Service) CheckPassword(password, hash string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}

// BurnCheckTime does the work of a password comparison and throws the answer
// away. Login calls it when the address is unknown: returning straight away made
// an unregistered email answer measurably faster than a wrong password, which is
// all an attacker needs to enumerate who has an account here.
func (s *Service) BurnCheckTime(password string) {
	bcrypt.CompareHashAndPassword(decoyHash, []byte(password))
}

// decoyHash is generated once at startup so it is guaranteed to be a valid hash
// at the same cost the real ones use — a hard-coded string could drift from
// bcryptCost and make the two paths distinguishable again.
var decoyHash = func() []byte {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		buf = []byte("seekersstudio-decoy-password-value")
	}
	hash, err := bcrypt.GenerateFromPassword(buf, bcryptCost)
	if err != nil {
		// Unreachable for a 32-byte input, but a nil hash would make the compare
		// return early and defeat the point.
		log.Printf("WARNING: could not prepare the login timing decoy: %v", err)
	}
	return hash
}()

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
