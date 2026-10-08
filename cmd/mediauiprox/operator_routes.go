package main

import (
	"bytes"
	"io"
	"net/http"
)

// Operator role gate (T-M5-12, C-30). Routes whose parentalRouteClasses row
// sets requirePrivileged are rejected here, before the parental gate of their
// class and before the handler, unless the BFF session has the admin or
// manager role (FRD §1, RULE-AUTH-3). The role comes only from the BFF session
// store (sessionHasPrivilegedRole / sessionHasAdminRole); client headers,
// query parameters and body fields are never consulted for it.

const (
	operatorCodeForbidden     = "operator.forbidden"
	operatorCodeAdminRequired = "operator.admin_required"
	operatorCodeBodyTooLarge  = "operator.body_too_large"
	operatorCodeBodyUnread    = "operator.body_unreadable"
	// operatorRootPathMaxBody bounds the PATCH body read to look for
	// root_folder_path. Library PATCH bodies are a few small fields.
	operatorRootPathMaxBody = 1 << 20
)

// operatorRoleGate wraps next with the route's role requirement. Routes
// without requirePrivileged are returned unchanged.
func (s *server) operatorRoleGate(route parentalRoute, next http.Handler) http.Handler {
	if !route.requirePrivileged {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.sessionHasPrivilegedRole(r) {
			writeAPIError(w, http.StatusForbidden, "admin or manager role required", operatorCodeForbidden)
			return
		}
		if route.rootPathAdmin && !s.sessionHasAdminRole(r) {
			names, status, code := requestNamesRootFolderPath(r)
			if code != "" {
				writeAPIError(w, status, "request body could not be checked", code)
				return
			}
			if names {
				writeAPIError(w, http.StatusForbidden, "admin role required to change root_folder_path", operatorCodeAdminRequired)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// requestNamesRootFolderPath reports whether the JSON body sets
// root_folder_path, decoding it exactly as the library PATCH handlers do
// (decodeLibraryPatch), and restores the body for the handler. Any presence
// counts, including an empty string: some modules apply an empty path.
func requestNamesRootFolderPath(r *http.Request) (names bool, status int, code string) {
	if r.Body == nil || r.Body == http.NoBody {
		return false, 0, ""
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, operatorRootPathMaxBody+1))
	_ = r.Body.Close()
	if err != nil {
		return false, http.StatusBadRequest, operatorCodeBodyUnread
	}
	if len(raw) > operatorRootPathMaxBody {
		return false, http.StatusRequestEntityTooLarge, operatorCodeBodyTooLarge
	}
	r.Body = io.NopCloser(bytes.NewReader(raw))
	return decodeLibraryPatch(bytes.NewReader(raw)).RootFolderPath != nil, 0, ""
}
