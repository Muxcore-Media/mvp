package main

import (
	"context"
	"net/http"
	"strings"

	authv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/auth/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type sessionValidator interface {
	Validate(context.Context, *authv1.ValidateRequest, ...grpc.CallOption) (*authv1.ValidateResponse, error)
}

type checkedSessionKey struct{}

// requestSession pins identity, claims and the provider bearer together for a
// request. Direct handler tests and the public Quick Connect flow may not yet
// have a checked snapshot; read one atomic store copy in that case.
func requestSession(r *http.Request, sessions *sessionStore) (sessionEntry, bool) {
	if e, ok := r.Context().Value(checkedSessionKey{}).(sessionEntry); ok {
		e.roles = append([]string(nil), e.roles...)
		return e, true
	}
	return sessions.get(sessionTokenFromRequest(r))
}

func (s *server) withAuth(next http.Handler) http.Handler {
	return s.withSessionValidation(next, true)
}

// Anonymous development requests remain supported, but an attached session is
// always checked. Recovery/public routes do not depend on a provider being up.
func (s *server) withSessionValidation(next http.Handler, required bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz", "/login", "/auth/callback", "/logout", "/api/quickconnect", "/api/tv/login", "/api/tv/login/totp",
			"/api/mobile/auth/login", "/api/mobile/auth/done", "/api/mobile/session",
			"/api/invite/peek", "/api/invite/redeem":
			next.ServeHTTP(w, r)
			return
		}
		if (r.URL.Path == "/api/password-reset" && r.Method == http.MethodPost) ||
			strings.HasPrefix(r.URL.Path, "/invite/") || strings.HasPrefix(r.URL.Path, "/images/") {
			next.ServeHTTP(w, r)
			return
		}
		if !required && sessionTokenFromRequest(r) == "" {
			next.ServeHTTP(w, r)
			return
		}
		if checked, ok := s.validateRequestSession(w, r); ok {
			next.ServeHTTP(w, checked)
		}
	})
}

// Validate only the exact bearer stored with the local session. Replacing both
// metadata fields avoids inheriting a different principal through the context.
func sessionValidationContext(ctx context.Context, bearer string) context.Context {
	md, _ := metadata.FromOutgoingContext(ctx)
	md = md.Copy()
	md.Set("x-auth-token", bearer)
	md.Set("authorization", "Bearer "+bearer)
	return metadata.NewOutgoingContext(ctx, md)
}

func (s *server) validateRequestSession(w http.ResponseWriter, r *http.Request) (*http.Request, bool) {
	w.Header().Set("Cache-Control", "no-store")
	tok := sessionTokenFromRequest(r)
	snap, ok := s.sessions.get(tok)
	if !ok {
		s.rejectSessionRequest(w, r)
		return r, false
	}
	if snap.authToken == "" {
		// Quick Connect/legacy sessions have no provider grant to validate.
		return r.WithContext(context.WithValue(r.Context(), checkedSessionKey{}, snap)), true
	}
	if s.auth == nil {
		writeSessionUnavailable(w)
		return r, false
	}
	ctx, cancel := context.WithTimeout(sessionValidationContext(r.Context(), snap.authToken), authUpstreamTimeout)
	defer cancel()
	resp, err := s.auth.Validate(ctx, &authv1.ValidateRequest{Token: snap.authToken})
	// Check on every outcome, including outages: an in-flight call cannot
	// resurrect a logout/expiry or reject a replacement provider binding.
	current, exists := s.sessions.get(tok)
	if !exists {
		s.rejectSessionRequest(w, r)
		return r, false
	}
	if !sameSessionBinding(current, snap) {
		writeSessionUnavailable(w)
		return r, false
	}
	if ctx.Err() != nil {
		writeSessionUnavailable(w)
		return r, false
	}
	if err != nil {
		switch status.Code(err) {
		case codes.Unauthenticated:
			s.rejectBoundSession(w, r, tok, snap)
		case codes.PermissionDenied:
			writeAPIError(w, http.StatusForbidden, "session access denied", "auth.forbidden")
		default:
			writeSessionUnavailable(w)
		}
		return r, false
	}
	if resp == nil {
		writeSessionUnavailable(w)
		return r, false
	}
	// Never move a local cookie to another user or tenant. Public claims are
	// canonical Validate fields; token claims are not an alternative identity.
	if !resp.GetValid() || strings.TrimSpace(resp.GetUserId()) == "" ||
		resp.GetUserId() != snap.userID || resp.GetTenantId() != snap.tenantID {
		s.rejectBoundSession(w, r, tok, snap)
		return r, false
	}
	updated, applied := s.sessions.commitValidated(tok, snap, resp.GetUsername(), resp.GetRoles())
	if !applied {
		if _, exists := s.sessions.get(tok); exists {
			writeSessionUnavailable(w)
		} else {
			s.rejectSessionRequest(w, r)
		}
		return r, false
	}
	return r.WithContext(context.WithValue(r.Context(), checkedSessionKey{}, updated)), true
}

func (s *server) rejectBoundSession(w http.ResponseWriter, r *http.Request, tok string, snap sessionEntry) {
	if !s.sessions.deleteIfBound(tok, snap) {
		if _, exists := s.sessions.get(tok); exists {
			writeSessionUnavailable(w)
			return
		}
	}
	s.rejectSessionRequest(w, r)
}

func (s *server) rejectSessionRequest(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie("session"); err == nil && c.Value != "" {
		http.SetCookie(w, &http.Cookie{Name: "session", Path: "/", MaxAge: -1,
			HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: strings.HasPrefix(s.publicOrigin(r), "https://")})
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		writeAPIUnauthorized(w)
	} else if wantsJSON(r) || strings.HasPrefix(r.URL.Path, "/stream/") {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	} else {
		s.redirectLogin(w, r)
	}
}

func writeSessionUnavailable(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "1")
	writeAPIError(w, http.StatusServiceUnavailable, "Identity service unavailable. Your sign-in was kept; try again.", "auth.unavailable")
}
