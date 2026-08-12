package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ArminDashti/arvaz-api/internal/store"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

var ErrInvalidCredentials = errors.New("invalid username or password")

type Service struct {
	store     *store.Store
	jwtSecret []byte
}

func NewService(st *store.Store, jwtSecret string) *Service {
	return &Service{store: st, jwtSecret: []byte(jwtSecret)}
}

type Claims struct {
	UserID   int64  `json:"userId"`
	Username string `json:"username"`
	jwt.RegisteredClaims
}

type LoginResponse struct {
	Token    string `json:"token"`
	Username string `json:"username"`
}

func (s *Service) EnsureDefaultUser(ctx context.Context, username, password string) error {
	if s.store == nil {
		return fmt.Errorf("database unavailable")
	}
	count, err := s.store.CountUsers(ctx)
	if err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	return s.store.CreateUser(ctx, username, string(hash))
}

func (s *Service) Login(ctx context.Context, username, password string) (*LoginResponse, error) {
	if s.store == nil {
		return nil, fmt.Errorf("database unavailable")
	}
	user, err := s.store.GetUserByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, ErrInvalidCredentials
		}
		return nil, err
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return nil, ErrInvalidCredentials
	}
	token, err := s.createToken(user.ID, user.Username)
	if err != nil {
		return nil, err
	}
	return &LoginResponse{Token: token, Username: user.Username}, nil
}

func (s *Service) ValidateToken(tokenStr string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return s.jwtSecret, nil
	})
	if err != nil {
		return nil, err
	}
	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, errors.New("invalid token")
	}
	return claims, nil
}

func (s *Service) createToken(userID int64, username string) (string, error) {
	claims := Claims{
		UserID:   userID,
		Username: username,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(7 * 24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(s.jwtSecret)
}
