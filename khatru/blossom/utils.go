package blossom

import (
	"net/http"
)

func blossomError(w http.ResponseWriter, msg string, code int) {
	w.Header().Add("X-Reason", msg)
	w.WriteHeader(code)
}
