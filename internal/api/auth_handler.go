package api

import (
	"net/http"

	"github.com/liteflow/backend/internal/auth"
)

type AuthHandler struct {
	authSvc *auth.AuthService
}

func NewAuthHandler(authSvc *auth.AuthService) *AuthHandler {
	return &AuthHandler{authSvc: authSvc}
}

func (h *AuthHandler) SendCode(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Phone string `json:"phone"`
	}
	if err := DecodeJSON(r, &body); err != nil || body.Phone == "" {
		BadRequest(w, "phone is required")
		return
	}

	if err := h.authSvc.SendCode(r.Context(), body.Phone); err != nil {
		switch err {
		case auth.ErrTooFrequent:
			Error(w, http.StatusTooManyRequests, 42900, "发送过于频繁，请60秒后重试")
		case auth.ErrDailyLimit:
			Error(w, http.StatusTooManyRequests, 42901, "今日验证码发送次数已达上限")
		default:
			InternalError(w, "发送验证码失败")
		}
		return
	}

	OKMsg(w, "验证码已发送")
}

func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Phone string `json:"phone"`
		Code  string `json:"code"`
	}
	if err := DecodeJSON(r, &body); err != nil || body.Phone == "" || body.Code == "" {
		BadRequest(w, "phone and code are required")
		return
	}

	result, err := h.authSvc.Login(r.Context(), body.Phone, body.Code)
	if err != nil {
		if err == auth.ErrInvalidCode {
			Error(w, http.StatusUnauthorized, 40101, "验证码无效或已过期")
			return
		}
		InternalError(w, "登录失败")
		return
	}

	OK(w, result)
}

func (h *AuthHandler) Refresh(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RefreshToken string `json:"refreshToken"`
	}
	if err := DecodeJSON(r, &body); err != nil || body.RefreshToken == "" {
		BadRequest(w, "refreshToken is required")
		return
	}

	result, err := h.authSvc.Refresh(r.Context(), body.RefreshToken)
	if err != nil {
		Unauthorized(w, "无效的刷新令牌")
		return
	}

	OK(w, result)
}

func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RefreshToken string `json:"refreshToken"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		BadRequest(w, "refreshToken is required")
		return
	}

	_ = h.authSvc.Logout(r.Context(), body.RefreshToken)
	OKEmpty(w)
}
