package main

import (
	"net/http"
	"strings"
	"time"
)

func (s *sessionStore) LookupRoles(tok string) (userID, username, tenantID string, roles []string, ok bool) {
	if tok == "" {
		return "", "", "", nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.byID[tok]
	if !ok || timeNow().After(e.expiry) {
		return "", "", "", nil, false
	}
	return e.userID, e.username, e.tenantID, append([]string(nil), e.roles...), true
}

func sessionTokenFromRequest(r *http.Request) string {
	if c, err := r.Cookie("session"); err == nil && c.Value != "" {
		return c.Value
	}
	return bearerSessionToken(r)
}

func (s *server) sessionIdentity(r *http.Request) (username string, roles []string, ok bool) {
	if s.sessions == nil {
		return "", nil, false
	}
	tok := sessionTokenFromRequest(r)
	if tok == "" {
		return "", nil, false
	}
	_, username, _, roles, ok = s.sessions.LookupRoles(tok)
	return username, roles, ok
}

func (s *server) sessionHasPrivilegedRole(r *http.Request) bool {
	_, roles, ok := s.sessionIdentity(r)
	if !ok {
		return false
	}
	for _, role := range roles {
		switch strings.ToLower(strings.TrimSpace(role)) {
		case "admin", "manager":
			return true
		}
	}
	return false
}

func rolesFromClaims(claims map[string]any) []string {
	if claims == nil {
		return nil
	}
	switch v := claims["roles"].(type) {
	case []string:
		return append([]string(nil), v...)
	case []any:
		out := make([]string, 0, len(v))
		for _, x := range v {
			if s, ok := x.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		return out
	}
	return nil
}

// timeNow is overridden in tests.
var timeNow = func() time.Time { return time.Now() }
