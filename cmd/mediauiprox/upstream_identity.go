package main

import (
	"net/http"
	"strings"
)

// End-user identity propagation to modules (ADR-0019, NFR-SEC-007).
//
// The BFF never forwards client-supplied identity or credential headers. It
// sends the signed-in user's auth-local token as a bearer and, for one
// release, X-Caller-Id=<user id> for modules that still read it.

// isClientIdentityHeader reports headers a client must not be able to pass
// through to a module: identity/tenant claims and BFF/auth credentials.
func isClientIdentityHeader(name string) bool {
	k := strings.ToLower(strings.TrimSpace(name))
	if strings.HasPrefix(k, "x-muxcore-") {
		return true
	}
	switch k {
	case "host", "cookie", "authorization", "proxy-authorization",
		"x-caller-id", "x-tenant-id", "x-auth-claims-tenant",
		"x-user-id", "x-auth-token":
		return true
	}
	return false
}

// copyClientHeaders copies request headers from src to dst, dropping identity
// and credential headers.
func copyClientHeaders(dst, src http.Header) {
	for k, vals := range src {
		if isClientIdentityHeader(k) {
			continue
		}
		for _, v := range vals {
			dst.Add(k, v)
		}
	}
}

// upstreamIdentity is the authenticated principal forwarded to modules.
type upstreamIdentity struct {
	userID    string
	username  string
	authToken string // signed-in user's auth-local token
}

// callerID is the user id, falling back to the username for legacy sessions.
func (u upstreamIdentity) callerID() string {
	if id := strings.TrimSpace(u.userID); id != "" {
		return id
	}
	return strings.TrimSpace(u.username)
}

// sessionUpstreamIdentity resolves the request's BFF session to the identity
// forwarded upstream. ok is false without a valid session.
func (s *server) sessionUpstreamIdentity(r *http.Request) (upstreamIdentity, bool) {
	if s.sessions == nil {
		return upstreamIdentity{}, false
	}
	tok := sessionTokenFromRequest(r)
	userID, username, _, _, ok := s.sessions.LookupRoles(tok)
	if !ok {
		return upstreamIdentity{}, false
	}
	return upstreamIdentity{userID: userID, username: username, authToken: s.sessions.LookupAuthToken(tok)}, true
}

// apply sets the bearer and compatibility caller header on an upstream request.
func (u upstreamIdentity) apply(h http.Header) {
	if tok := strings.TrimSpace(u.authToken); tok != "" {
		h.Set("Authorization", "Bearer "+tok)
	}
	if c := u.callerID(); c != "" {
		h.Set("X-Caller-Id", c)
	}
}
