package api

import (
	"encoding/json"
	"net/http"
)

// Error codes; must stay in sync with the Error.code enum in openapi.yaml.
const (
	CodeInvalidRequest   = "invalid_request"
	CodeUnauthenticated  = "unauthenticated"
	CodeBackupNotFound   = "backup_not_found"
	CodeActivityNotFound = "activity_not_found"
	CodeLogNotFound      = "log_not_found"
	CodeBackupRunning    = "backup_running"
	CodeInternal         = "internal_error"
	CodeNotImplemented   = "not_implemented"
	CodeNotFound         = "not_found"          // add to spec
	CodeMethodNotAllowed = "method_not_allowed" // add to spec
	CodeUnavailable      = "unavailable"        // add to spec
)

type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type errorBody struct {
	Error errorDetail `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, errorBody{Error: errorDetail{Code: code, Message: msg}})
}