package api

// The email/password era of sign-in is gone (PLAN_CORE_PRODUCT.md D):
// identity comes from GitHub and Google through internal/api/auth_oauth.go.
// This file keeps only the shared response shape the OAuth exchange uses.

import (
	"github.com/seekersstudio/backend/internal/store"
)

type authResponse struct {
	Token string      `json:"token"`
	User  *store.User `json:"user"`
}
