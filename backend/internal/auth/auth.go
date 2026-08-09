package auth

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrUnauthorized       = errors.New("unauthorized")
)

const (
	bcryptCost       = 12
	jwtExpiry        = 24 * time.Hour
	issuer           = "seekersstudio"
)

type Claims struct {
	UserID int64  `json:"user_id"`
	Email  string `json:"email"`
	jwt.RegisteredClaims
}

type Service struct {
	secret []byte
}

func New() *Service {
	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		// Generate a random secret at startup and log a warning.
		// In production this means tokens are invalidated on restart,
		// which is acceptable — the real fix is setting JWT_SECRET.
		buf := make([]byte, 32)
		if _, err := rand.Read(buf); err != nil {
			log.Fatal("failed to generate random JWT secret: ", err)
		}
		secret = hex.EncodeToString(buf)
		log.Printf("WARNING: JWT_SECRET not set — generated ephemeral secret. Tokens will be invalid after restart. Set JWT_SECRET in your environment.")
	}
	return &Service{secret: []byte(secret)}
}

func (s *Service) HashPassword(password string) (string, error) {
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	return string(bytes), err
}

func (s *Service) CheckPassword(password, hash string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
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

func (s *Service) ValidateToken(tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		return s.secret, nil
	})
	if err != nil {
		return nil, ErrUnauthorized
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, ErrUnauthorized
	}

	return claims, nil
}
