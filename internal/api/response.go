package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

type ApiResponse[T any] struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    T      `json:"data,omitempty"`
}

func OK[T any](w http.ResponseWriter, data T) {
	writeJSON(w, http.StatusOK, ApiResponse[T]{Code: 200, Message: "success", Data: data})
}

func OKMsg(w http.ResponseWriter, msg string) {
	writeJSON(w, http.StatusOK, ApiResponse[any]{Code: 200, Message: msg})
}

func OKEmpty(w http.ResponseWriter) {
	writeJSON(w, http.StatusOK, ApiResponse[any]{Code: 200, Message: "success"})
}

func Error(w http.ResponseWriter, status int, code int, message string) {
	writeJSON(w, status, ApiResponse[any]{Code: code, Message: message})
}

func BadRequest(w http.ResponseWriter, message string) {
	Error(w, http.StatusBadRequest, 40000, message)
}

func Unauthorized(w http.ResponseWriter, message string) {
	Error(w, http.StatusUnauthorized, 40100, message)
}

func Forbidden(w http.ResponseWriter) {
	Error(w, http.StatusForbidden, 40300, "forbidden")
}

func NotFound(w http.ResponseWriter, message string) {
	Error(w, http.StatusNotFound, 40400, message)
}

func InternalError(w http.ResponseWriter, message string) {
	Error(w, http.StatusInternalServerError, 50000, message)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("failed to write JSON response", "err", err)
	}
}

func DecodeJSON(r *http.Request, v any) error {
	return json.NewDecoder(r.Body).Decode(v)
}
