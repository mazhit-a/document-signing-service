package http

import (
	"encoding/json"
	"net/http"
)

// writeErrorEnvelope writes the standard error JSON. It is deliberately
// minimal; the domain-error mapper lands in errors.go in a later increment
// and will reuse it.
func writeErrorEnvelope(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{"code": code, "message": message, "fields": map[string]string{}},
	})
}
