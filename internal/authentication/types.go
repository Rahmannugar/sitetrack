package authentication

import "errors"

var (
	ErrInvalidInput        = errors.New("invalid account details")
	ErrInvalidVerification = errors.New("invalid or expired verification code")
)

type SignupInput struct {
	Name     string `json:"name" binding:"required,min=2,max=100"`
	Email    string `json:"email" binding:"required,email,max=254"`
	Password string `json:"password" binding:"required,min=12,max=128"`
	Role     string `json:"role" binding:"required,oneof=user company_store_manager superadmin"`
}

type VerificationInput struct {
	Email string `json:"email" binding:"required,email,max=254"`
	Code  string `json:"code" binding:"required,len=6,number"`
}

type ResendInput struct {
	Email string `json:"email" binding:"required,email,max=254"`
}
