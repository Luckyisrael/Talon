package api

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/seekersstudio/backend/internal/auth"
	"github.com/seekersstudio/backend/internal/store"
)

// AuthHandler doesn't need GetUserID — it's called before auth middleware.

type AuthHandler struct {
	store *store.Store
	auth  *auth.Service
}

func NewAuthHandler(s *store.Store, a *auth.Service) *AuthHandler {
	return &AuthHandler{store: s, auth: a}
}

type registerRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type authResponse struct {
	Token string      `json:"token"`
	User  *store.User `json:"user"`
}

func (h *AuthHandler) Register(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "could not read the request body")
		return
	}

	// Registration used to accept any non-empty strings, so "x" / "x" was a valid
	// account and a typo in an address could never be recovered from.
	email, err := auth.ValidateEmail(req.Email)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := auth.ValidatePassword(req.Password); err != nil {
		writeError(w, http.StatusBadRequest, "Choose a stronger password: "+err.Error()+".")
		return
	}

	hash, err := h.auth.HashPassword(req.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create your account")
		return
	}

	user, err := h.store.CreateUser(email, hash)
	if err != nil {
		if errors.Is(err, store.ErrDuplicateEmail) {
			writeError(w, http.StatusConflict, "an account already exists for that email")
			return
		}
		log.Printf("register: could not create %s: %v", email, err)
		writeError(w, http.StatusInternalServerError, "could not create your account")
		return
	}

	token, err := h.auth.GenerateToken(user.ID, user.Email)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "your account was created but signing in failed — try signing in")
		return
	}

	writeJSON(w, http.StatusCreated, authResponse{Token: token, User: user})
}

func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "could not read the request body")
		return
	}

	// Addresses are stored lower-cased, so normalise before looking one up.
	// Without this, anyone who typed a capital letter could not sign back in.
	email := strings.ToLower(strings.TrimSpace(req.Email))

	user, err := h.store.GetUserByEmail(email)
	if err != nil {
		h.auth.BurnCheckTime(req.Password)
		writeError(w, http.StatusUnauthorized, "that email and password do not match")
		return
	}

	if !h.auth.CheckPassword(req.Password, user.PasswordHash) {
		writeError(w, http.StatusUnauthorized, "that email and password do not match")
		return
	}

	token, err := h.auth.GenerateToken(user.ID, user.Email)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not sign you in")
		return
	}

	writeJSON(w, http.StatusOK, authResponse{Token: token, User: user})
}
