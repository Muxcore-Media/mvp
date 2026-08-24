package main

import "net/http"

func writeAPIError(w http.ResponseWriter, status int, message, code string) {
	writeJSONStatus(w, status, map[string]string{"error": message, "code": code})
}

func writeAPIMethodNotAllowed(w http.ResponseWriter) {
	writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed", "api.method_not_allowed")
}

func writeAPIUnauthorized(w http.ResponseWriter) {
	writeAPIError(w, http.StatusUnauthorized, "unauthorized", "api.unauthorized")
}
