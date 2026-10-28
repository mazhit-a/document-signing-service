package http

import (
	"encoding/json"
	"net/http"
)

type errorBody struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Fields  map[string]string `json:"fields"`
}

type errorEnvelope struct {
	Error errorBody `json:"error"`
}

func writeErrorEnvelope(w http.ResponseWriter, status int, code, message string) {
	writeErrorWithFields(w, status, code, message, nil)
}

func writeErrorWithFields(w http.ResponseWriter, status int, code, message string, fields map[string]string) {
	if fields == nil {
		fields = map[string]string{}
	}
	WriteJSON(w, status, errorEnvelope{Error: errorBody{Code: code, Message: message, Fields: fields}})
}

func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
