package livepeerheader

import (
	"encoding/json"
	"net/http"
)

// WriteError writes a Livepeer-shaped error response: sets Livepeer-Error,
// Content-Type, status, and a structured JSON body.
func WriteError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set(Error, code)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error":   code,
		"message": message,
	})
}

// WriteBadRequest writes a 400 with no Livepeer-Error code (for cases the
// spec does not enumerate, e.g. "missing required header").
func WriteBadRequest(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"message": message,
	})
}
