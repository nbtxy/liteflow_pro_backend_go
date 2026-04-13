package api

import (
	"encoding/json"
	"net/http"

	"github.com/liteflow/backend/internal/auth"
	"github.com/liteflow/backend/internal/user"
)

type UserHandler struct {
	userSvc *user.Service
}

func NewUserHandler(userSvc *user.Service) *UserHandler {
	return &UserHandler{userSvc: userSvc}
}

func (h *UserHandler) GetProfile(w http.ResponseWriter, r *http.Request) {
	userID, err := auth.GetUserID(r.Context())
	if err != nil {
		Unauthorized(w, "unauthorized")
		return
	}

	u, err := h.userSvc.GetByID(r.Context(), userID)
	if err != nil {
		InternalError(w, "failed to get user")
		return
	}

	OK(w, u)
}

func (h *UserHandler) UpdateName(w http.ResponseWriter, r *http.Request) {
	userID, err := auth.GetUserID(r.Context())
	if err != nil {
		Unauthorized(w, "unauthorized")
		return
	}

	var body struct {
		Name string `json:"name"`
	}
	if err := DecodeJSON(r, &body); err != nil || body.Name == "" {
		BadRequest(w, "name is required")
		return
	}

	if err := h.userSvc.UpdateName(r.Context(), userID, body.Name); err != nil {
		InternalError(w, "failed to update name")
		return
	}

	OKEmpty(w)
}

func (h *UserHandler) GetSettings(w http.ResponseWriter, r *http.Request) {
	userID, err := auth.GetUserID(r.Context())
	if err != nil {
		Unauthorized(w, "unauthorized")
		return
	}

	settings, err := h.userSvc.GetSettings(r.Context(), userID)
	if err != nil {
		InternalError(w, "failed to get settings")
		return
	}

	OK(w, settings)
}

func (h *UserHandler) UpdateSettings(w http.ResponseWriter, r *http.Request) {
	userID, err := auth.GetUserID(r.Context())
	if err != nil {
		Unauthorized(w, "unauthorized")
		return
	}

	var settings json.RawMessage
	if err := DecodeJSON(r, &settings); err != nil {
		BadRequest(w, "invalid settings JSON")
		return
	}

	if err := h.userSvc.UpdateSettings(r.Context(), userID, settings); err != nil {
		InternalError(w, "failed to update settings")
		return
	}

	OKEmpty(w)
}
