package authentication

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	ErrInvalidInput        = errors.New("invalid account details")
	ErrInvalidVerification = errors.New("invalid or expired verification code")
	ErrInvalidCredentials  = errors.New("invalid email or password")
	ErrUnauthenticated     = errors.New("authentication required")
	ErrEmailRegistered     = errors.New("email already registered")
	ErrEmailUnverified     = errors.New("verify your email before logging in")
	ErrInvalidReset        = errors.New("invalid or expired password reset code")
)

type SignupInput struct {
	Name     string `json:"name" binding:"required,min=2,max=100"`
	Email    string `json:"email" binding:"required,email,max=254"`
	Password string `json:"password" binding:"required,min=12,max=128"`
	Role     string `json:"role" binding:"required,oneof=user company_store_manager superadmin"`
}

type LoginInput struct {
	Email    string `json:"email" binding:"required,email,max=254"`
	Password string `json:"password" binding:"required,max=128"`
}

type RefreshInput struct {
	RefreshToken string `json:"refresh_token" binding:"required,len=43"`
}

type Profile struct {
	ID            uuid.UUID `json:"id"`
	Name          string    `json:"name"`
	Email         string    `json:"email"`
	Role          string    `json:"role"`
	EmailVerified bool      `json:"email_verified"`
}

type TokenPair struct {
	AccessToken           string    `json:"access_token"`
	AccessTokenExpiresAt  time.Time `json:"access_token_expires_at"`
	RefreshToken          string    `json:"refresh_token"`
	RefreshTokenExpiresAt time.Time `json:"refresh_token_expires_at"`
	TokenType             string    `json:"token_type"`
	User                  Profile   `json:"user"`
}

type Principal struct {
	SessionID uuid.UUID
	User      Profile
}

type VerificationInput struct {
	Email string `json:"email" binding:"required,email,max=254"`
	Code  string `json:"code" binding:"required,len=6,number"`
}

type ResendInput struct {
	Email string `json:"email" binding:"required,email,max=254"`
}

type ResetPasswordInput struct {
	Email    string `json:"email" binding:"required,email,max=254"`
	Code     string `json:"code" binding:"required,len=6,number"`
	Password string `json:"password" binding:"required,min=12,max=128"`
}
