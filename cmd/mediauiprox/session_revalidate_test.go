package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	authv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/auth/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

type sessionValidatorFunc func(context.Context, *authv1.ValidateRequest) (*authv1.ValidateResponse, error)

func (f sessionValidatorFunc) Validate(ctx context.Context, req *authv1.ValidateRequest, _ ...grpc.CallOption) (*authv1.ValidateResponse, error) {
	return f(ctx, req)
}

func validSessionResponse() *authv1.ValidateResponse {
	return &authv1.ValidateResponse{Valid: true, UserId: "u1", Username: "current", TenantId: "home", Roles: []string{"user"}}
}

func boundBFFSession(t *testing.T, s *server) string {
	t.Helper()
	tok, err := s.sessions.CreateWithAuth("u1", "old-name", "home", []string{"admin"}, "provider-bearer")
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func sessionCheckRequest(path, tok, form string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	switch form {
	case "cookie":
		r.AddCookie(&http.Cookie{Name: "session", Value: tok})
	case "bearer":
		r.Header.Set("Authorization", "Bearer "+tok)
	case "header":
		r.Header.Set("X-MuxCore-Session", tok)
	}
	return r
}

func TestSessionRevalidationCurrentClaimsAndMetadata(t *testing.T) {
	for _, form := range []string{"cookie", "bearer", "header"} {
		t.Run(form, func(t *testing.T) {
			s := &server{sessions: newSessionStore(time.Hour)}
			tok := boundBFFSession(t, s)
			before, _ := s.sessions.get(tok)
			calls := 0
			s.auth = sessionValidatorFunc(func(ctx context.Context, req *authv1.ValidateRequest) (*authv1.ValidateResponse, error) {
				calls++
				md, _ := metadata.FromOutgoingContext(ctx)
				if req.GetToken() != "provider-bearer" || !reflect.DeepEqual(md.Get("x-auth-token"), []string{"provider-bearer"}) || !reflect.DeepEqual(md.Get("authorization"), []string{"Bearer provider-bearer"}) {
					t.Error("Validate did not receive exactly the stored credential")
				}
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) > authUpstreamTimeout || time.Until(deadline) < 7*time.Second {
					t.Error("Validate missing eight-second bound")
				}
				return validSessionResponse(), nil
			})
			r := sessionCheckRequest("/api/session", tok, form)
			md := metadata.Pairs("x-auth-token", "old", "x-auth-token", "other", "authorization", "Bearer old", "trace-id", "trace")
			r = r.WithContext(metadata.NewOutgoingContext(r.Context(), md))
			w := httptest.NewRecorder()
			s.withAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if _, ok := r.Context().Deadline(); ok {
					t.Error("validation deadline leaked to handler")
				}
				if r.Context().Err() != nil {
					t.Error("validation cancellation leaked to handler")
				}
				if s.sessionHasPrivilegedRole(r) {
					t.Error("stale admin role survived validation")
				}
				s.handleSessionMe(w, r)
			})).ServeHTTP(w, r)
			if calls != 1 || w.Code != 200 || !strings.Contains(w.Body.String(), `"username":"current"`) {
				t.Fatalf("calls=%d status=%d body=%s", calls, w.Code, w.Body.String())
			}
			after, _ := s.sessions.get(tok)
			if !sameSessionBinding(before, after) || after.username != "current" || !reflect.DeepEqual(after.roles, []string{"user"}) {
				t.Fatal("claims not refreshed or binding changed")
			}
			if !reflect.DeepEqual(md.Get("x-auth-token"), []string{"old", "other"}) {
				t.Fatal("parent metadata mutated")
			}
		})
	}
}

func TestSessionRevalidationOutcomes(t *testing.T) {
	for _, outcome := range []string{"invalid", "unauthenticated", "permission", "unavailable", "internal", "unimplemented", "canceled", "deadline", "nil", "missing-client", "blank-user", "whitespace-user", "other-user", "other-tenant", "blank-tenant", "valid"} {
		t.Run(outcome, func(t *testing.T) {
			s := &server{sessions: newSessionStore(time.Hour), publicURL: "https://media.example"}
			tok := boundBFFSession(t, s)
			s.auth = sessionValidatorFunc(func(context.Context, *authv1.ValidateRequest) (*authv1.ValidateResponse, error) {
				resp := validSessionResponse()
				switch outcome {
				case "invalid":
					resp.Valid = false
				case "blank-user":
					resp.UserId = ""
				case "whitespace-user":
					resp.UserId = " \t"
				case "other-user":
					resp.UserId = "u2"
				case "other-tenant":
					resp.TenantId = "other"
				case "blank-tenant":
					resp.TenantId = ""
				case "nil":
					return nil, nil
				default:
					if c, ok := map[string]codes.Code{"unauthenticated": codes.Unauthenticated, "permission": codes.PermissionDenied, "unavailable": codes.Unavailable, "internal": codes.Internal, "unimplemented": codes.Unimplemented, "canceled": codes.Canceled, "deadline": codes.DeadlineExceeded}[outcome]; ok {
						return nil, status.Error(c, "fixture failure")
					}
				}
				return resp, nil
			})
			if outcome == "missing-client" {
				s.auth = nil
			}
			ran := false
			w := httptest.NewRecorder()
			s.withAuth(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { ran = true; w.WriteHeader(204) })).ServeHTTP(w, sessionCheckRequest("/api/me", tok, "cookie"))
			want := 503
			switch outcome {
			case "invalid", "unauthenticated", "blank-user", "whitespace-user", "other-user", "other-tenant", "blank-tenant":
				want = 401
			case "permission":
				want = 403
			case "valid":
				want = 204
			}
			if w.Code != want || ran != (want == 204) || s.sessions.Valid(tok) != (want != 401) {
				t.Fatalf("status=%d want=%d ran=%v retained=%v", w.Code, want, ran, s.sessions.Valid(tok))
			}
			cookies := w.Result().Cookies()
			if want == 401 {
				if len(cookies) != 1 || cookies[0].MaxAge != -1 || !cookies[0].Secure || !cookies[0].HttpOnly {
					t.Fatal("rejected cookie not cleared securely")
				}
			} else if len(cookies) != 0 {
				t.Fatal("retained session cookie changed")
			}
			if want == 503 && (w.Header().Get("Retry-After") == "" || !strings.Contains(w.Body.String(), `"code":"auth.unavailable"`)) {
				t.Fatal("outage is not retryable/distinct")
			}
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("session response cacheable")
			}
		})
	}
}

func TestSessionRevalidationBindingChangesInFlight(t *testing.T) {
	for _, change := range []string{"logout", "expired", "bearer", "user", "tenant", "expiry"} {
		for _, outcome := range []string{"valid", "invalid", "unauthenticated", "permission", "outage", "nil"} {
			t.Run(change+"/"+outcome, func(t *testing.T) {
				s := &server{sessions: newSessionStore(time.Hour)}
				tok := boundBFFSession(t, s)
				started, release := make(chan struct{}), make(chan struct{})
				s.auth = sessionValidatorFunc(func(context.Context, *authv1.ValidateRequest) (*authv1.ValidateResponse, error) {
					close(started)
					<-release
					switch outcome {
					case "invalid":
						return &authv1.ValidateResponse{Valid: false}, nil
					case "unauthenticated":
						return nil, status.Error(codes.Unauthenticated, "revoked")
					case "permission":
						return nil, status.Error(codes.PermissionDenied, "denied")
					case "outage":
						return nil, status.Error(codes.Unavailable, "down")
					case "nil":
						return nil, nil
					}
					return validSessionResponse(), nil
				})
				var ran atomic.Bool
				w := httptest.NewRecorder()
				done := make(chan struct{})
				go func() {
					defer close(done)
					s.withAuth(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { ran.Store(true) })).ServeHTTP(w, sessionCheckRequest("/api/me", tok, "cookie"))
				}()
				<-started
				s.sessions.mu.Lock()
				replacement := s.sessions.byID[sessionID(tok)]
				switch change {
				case "logout":
					delete(s.sessions.byID, sessionID(tok))
				case "expired":
					replacement.expiry = timeNow().Add(-time.Second)
				case "bearer":
					replacement.authToken = "new-provider-bearer"
				case "user":
					replacement.userID = "u2"
				case "tenant":
					replacement.tenantID = "other"
				case "expiry":
					replacement.expiry = replacement.expiry.Add(time.Hour)
				}
				if change != "logout" {
					s.sessions.byID[sessionID(tok)] = replacement
				}
				s.sessions.mu.Unlock()
				close(release)
				<-done
				want := 503
				if change == "logout" || change == "expired" {
					want = 401
				}
				if ran.Load() || w.Code != want {
					t.Fatalf("ran=%v status=%d want=%d", ran.Load(), w.Code, want)
				}
				stored, exists := s.sessions.get(tok)
				if want == 401 && exists {
					t.Fatal("dead session resurrected")
				}
				if want == 503 && (!exists || !reflect.DeepEqual(stored, replacement) || len(w.Result().Cookies()) != 0) {
					t.Fatal("replacement changed or cookie cleared")
				}
			})
		}
	}
}

func TestSessionRevalidationCanceledSuccessFailsClosed(t *testing.T) {
	for _, during := range []bool{false, true} {
		t.Run(map[bool]string{false: "before", true: "during"}[during], func(t *testing.T) {
			s := &server{sessions: newSessionStore(time.Hour)}
			tok := boundBFFSession(t, s)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if !during {
				cancel()
			}
			s.auth = sessionValidatorFunc(func(context.Context, *authv1.ValidateRequest) (*authv1.ValidateResponse, error) {
				cancel()
				return validSessionResponse(), nil
			})
			w := httptest.NewRecorder()
			ran := false
			s.withAuth(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { ran = true })).ServeHTTP(w, sessionCheckRequest("/api/me", tok, "cookie").WithContext(ctx))
			if ran || w.Code != 503 || !s.sessions.Valid(tok) {
				t.Fatal("canceled success admitted or session lost")
			}
		})
	}
}

func TestSessionRevalidationAtomicStoreGuardsAndPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	key := make([]byte, 32)
	st, err := newPersistentSessionStore(path, time.Hour, key)
	if err != nil {
		t.Fatal(err)
	}
	s := &server{sessions: st}
	tok := boundBFFSession(t, s)
	old, _ := st.get(tok)
	copyOfOld := old
	copyOfOld.roles[0] = "mutated"
	if _, _, _, roles, _ := st.LookupRoles(tok); roles[0] != "admin" {
		t.Fatal("snapshot aliases roles")
	}
	old, _ = st.get(tok)
	updated, ok := st.commitValidated(tok, old, "current", nil)
	if !ok || updated.username != "current" || len(updated.roles) != 0 || !sameSessionBinding(old, updated) {
		t.Fatal("canonical empty claims not applied")
	}
	raw, _ := os.ReadFile(path)
	if _, ok := st.commitValidated(tok, updated, "current", nil); !ok {
		t.Fatal("unchanged commit failed")
	}
	unchanged, _ := os.ReadFile(path)
	if string(raw) != string(unchanged) {
		t.Fatal("unchanged claims rewrote encrypted session file")
	}
	if strings.Contains(string(raw), "provider-bearer") {
		t.Fatal("provider credential persisted in plaintext")
	}
	reloaded, err := newPersistentSessionStore(path, time.Hour, key)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := reloaded.get(tok)
	if !ok || got.username != "current" || len(got.roles) != 0 || got.authToken != "provider-bearer" {
		t.Fatal("claims/bearer lost across restart")
	}
	st.mu.Lock()
	replacement := st.byID[sessionID(tok)]
	replacement.authToken = "replacement"
	st.byID[sessionID(tok)] = replacement
	st.mu.Unlock()
	if st.deleteIfBound(tok, old) {
		t.Fatal("late rejection deleted replacement")
	}
	if _, ok := st.commitValidated(tok, old, "stale", []string{"admin"}); ok {
		t.Fatal("stale claims committed")
	}
	if !st.deleteIfBound(tok, replacement) {
		t.Fatal("matching deletion failed")
	}
	reloaded, err = newPersistentSessionStore(path, time.Hour, key)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Valid(tok) {
		t.Fatal("deleted session returned after restart")
	}
	if _, ok := st.commitValidated(tok, replacement, "stale", nil); ok {
		t.Fatal("commit resurrected deleted session")
	}
}

func TestSessionRevalidationRequestSnapshot(t *testing.T) {
	t.Setenv("TENANT_MODE", "1")
	s := &server{sessions: newSessionStore(time.Hour)}
	tok := boundBFFSession(t, s)
	s.auth = sessionValidatorFunc(func(context.Context, *authv1.ValidateRequest) (*authv1.ValidateResponse, error) {
		return validSessionResponse(), nil
	})
	w := httptest.NewRecorder()
	s.withAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.sessions.mu.Lock()
		changed := s.sessions.byID[sessionID(tok)]
		changed.userID = "u2"
		changed.tenantID = "other"
		changed.authToken = "replacement"
		changed.roles = []string{"admin"}
		s.sessions.byID[sessionID(tok)] = changed
		s.sessions.mu.Unlock()
		uid, name, tenant, roles, ok := s.sessionPrincipal(r)
		if !ok || uid != "u1" || name != "current" || tenant != "home" || !reflect.DeepEqual(roles, []string{"user"}) {
			t.Fatal("principal drifted from checked snapshot")
		}
		up, ok := s.sessionUpstreamIdentity(r)
		if !ok || up.userID != "u1" || up.authToken != "provider-bearer" || s.sessionAuthToken(r) != "provider-bearer" {
			t.Fatal("forwarded identity drifted")
		}
		pr, ok := s.sessionParentalPrincipal(r)
		if !ok || pr.userID != "u1" || pr.tenantID != "home" || pr.bearer != "provider-bearer" || pr.sessionID != sessionID(tok) {
			t.Fatal("parental identity drifted")
		}
		scope := (&serverUserdata{}).scopeFromRequest(r, s.sessions, false)
		if scope.UserID != "u1" || scope.TenantID != "home" {
			t.Fatal("userdata scope drifted")
		}
		if !s.requireLinkedAuthSession(w, r) {
			t.Fatal("TOTP lost checked session")
		}
		w.WriteHeader(204)
	})).ServeHTTP(w, sessionCheckRequest("/api/me", tok, "cookie"))
	if w.Code != 204 {
		t.Fatalf("status=%d", w.Code)
	}
}

func TestSessionRevalidationCredentialPrecedenceAndLocalOnly(t *testing.T) {
	for _, form := range []string{"cookie", "bearer", "header"} {
		t.Run(form, func(t *testing.T) {
			s := &server{sessions: newSessionStore(time.Hour)}
			tok, err := s.sessions.CreateWithRoles("local", "local", "", []string{"user"})
			if err != nil {
				t.Fatal(err)
			}
			s.auth = sessionValidatorFunc(func(context.Context, *authv1.ValidateRequest) (*authv1.ValidateResponse, error) {
				t.Fatal("local-only session called provider")
				return nil, nil
			})
			w := httptest.NewRecorder()
			s.withAuth(http.HandlerFunc(s.handleSessionMe)).ServeHTTP(w, sessionCheckRequest("/api/me", tok, form))
			if w.Code != 200 {
				t.Fatal(w.Code)
			}
			r := sessionCheckRequest("/api/me", tok, form)
			if form != "cookie" {
				r.AddCookie(&http.Cookie{Name: "session", Value: "stale"})
			} else {
				r.Header.Set("Authorization", "Bearer stale")
			}
			w = httptest.NewRecorder()
			s.withAuth(http.HandlerFunc(s.handleSessionMe)).ServeHTTP(w, r)
			want := 401
			if form == "cookie" {
				want = 200
			}
			if w.Code != want {
				t.Fatalf("credential precedence: %d want %d", w.Code, want)
			}
		})
	}
}

func TestSessionRevalidationDevAndPublicRecovery(t *testing.T) {
	s := &server{sessions: newSessionStore(time.Hour)}
	tok := boundBFFSession(t, s)
	s.auth = sessionValidatorFunc(func(context.Context, *authv1.ValidateRequest) (*authv1.ValidateResponse, error) {
		return nil, status.Error(codes.Unavailable, "down")
	})
	for _, path := range []string{"/healthz", "/login", "/auth/callback", "/logout", "/api/mobile/auth/login", "/api/mobile/auth/done", "/api/mobile/session", "/api/tv/login", "/api/tv/login/totp", "/api/invite/peek", "/api/invite/redeem", "/invite/token", "/images/poster"} {
		w := httptest.NewRecorder()
		s.withAuth(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })).ServeHTTP(w, sessionCheckRequest(path, tok, "cookie"))
		if w.Code != 204 {
			t.Fatalf("public %s unavailable: %d", path, w.Code)
		}
	}
	for _, credential := range []string{"", tok, "stale"} {
		w := httptest.NewRecorder()
		s.withSessionValidation(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }), false).ServeHTTP(w, sessionCheckRequest("/api/me", credential, "cookie"))
		want := 204
		if credential == tok {
			want = 503
		}
		if credential == "stale" {
			want = 401
		}
		if w.Code != want {
			t.Fatalf("dev credential %q: %d want %d", credential, w.Code, want)
		}
	}
}

func TestSessionRevalidationQuickConnectApproval(t *testing.T) {
	for _, outcome := range []string{"revoked", "outage", "valid"} {
		t.Run(outcome, func(t *testing.T) {
			s := &server{sessions: newSessionStore(time.Hour), quickconnect: newQuickConnectStore(t.TempDir())}
			tok := boundBFFSession(t, s)
			s.auth = sessionValidatorFunc(func(context.Context, *authv1.ValidateRequest) (*authv1.ValidateResponse, error) {
				switch outcome {
				case "revoked":
					return &authv1.ValidateResponse{}, nil
				case "outage":
					return nil, status.Error(codes.Unavailable, "down")
				}
				return validSessionResponse(), nil
			})
			h := s.withAuth(http.HandlerFunc(s.handleQuickConnect))
			r := httptest.NewRequest(http.MethodPost, "/api/quickconnect", strings.NewReader(`{"action":"register","code":"123456"}`))
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != 200 {
				t.Fatalf("register: %d %s", w.Code, w.Body.String())
			}
			r = httptest.NewRequest(http.MethodPost, "/api/quickconnect", strings.NewReader(`{"action":"approve","code":"123456"}`))
			r.AddCookie(&http.Cookie{Name: "session", Value: tok})
			w = httptest.NewRecorder()
			h.ServeHTTP(w, r)
			want := 200
			if outcome == "revoked" {
				want = 401
			}
			if outcome == "outage" {
				want = 503
			}
			if w.Code != want {
				t.Fatalf("approve: %d want %d", w.Code, want)
			}
			if s.quickconnect.load()["123456"].Approved != (outcome == "valid") {
				t.Fatal("invalid approver approved device")
			}
			w = httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/quickconnect?code=123456", nil))
			if w.Code != 200 {
				t.Fatalf("poll: %d", w.Code)
			}
			if outcome == "valid" {
				s.sessions.mu.Lock()
				defer s.sessions.mu.Unlock()
				for id, e := range s.sessions.byID {
					if id != sessionID(tok) && (e.authToken != "" || len(e.roles) != 0) {
						t.Fatal("Quick Connect unexpectedly minted provider grant/roles")
					}
				}
			}
		})
	}
}

func TestSessionRevalidationConcurrentRequests(t *testing.T) {
	s := &server{sessions: newSessionStore(time.Hour)}
	tok := boundBFFSession(t, s)
	s.auth = sessionValidatorFunc(func(context.Context, *authv1.ValidateRequest) (*authv1.ValidateResponse, error) {
		return validSessionResponse(), nil
	})
	h := s.withAuth(http.HandlerFunc(s.handleSessionMe))
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, sessionCheckRequest("/api/me", tok, "cookie"))
			if w.Code != 200 {
				t.Errorf("concurrent status %d", w.Code)
			}
		})
	}
	wg.Wait()
}

type sessionAuthRPC struct {
	authv1.UnimplementedAuthServiceServer
	validate func(context.Context, *authv1.ValidateRequest) (*authv1.ValidateResponse, error)
}

func (a *sessionAuthRPC) Validate(ctx context.Context, req *authv1.ValidateRequest) (*authv1.ValidateResponse, error) {
	return a.validate(ctx, req)
}

func TestSessionRevalidationProviderRPCAndImmediateRevocation(t *testing.T) {
	listener := bufconn.Listen(1 << 20)
	g := grpc.NewServer()
	var revoked atomic.Bool
	var calls atomic.Int32
	authv1.RegisterAuthServiceServer(g, &sessionAuthRPC{validate: func(ctx context.Context, req *authv1.ValidateRequest) (*authv1.ValidateResponse, error) {
		calls.Add(1)
		md, _ := metadata.FromIncomingContext(ctx)
		if req.GetToken() != "provider-bearer" || !reflect.DeepEqual(md.Get("x-auth-token"), []string{"provider-bearer"}) || !reflect.DeepEqual(md.Get("authorization"), []string{"Bearer provider-bearer"}) {
			t.Error("wrong provider RPC bearer metadata")
		}
		resp := validSessionResponse()
		resp.Valid = !revoked.Load()
		return resp, nil
	}})
	go func() { _ = g.Serve(listener) }()
	t.Cleanup(g.Stop)
	conn, err := grpc.NewClient("passthrough:///auth-fixture", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	s := &server{sessions: newSessionStore(time.Hour), auth: authv1.NewAuthServiceClient(conn), requireAuth: true}
	tok := boundBFFSession(t, s)
	mux := http.NewServeMux()
	s.registerRoutes(mux)
	h := s.withAuth(mux)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, sessionCheckRequest("/api/me", tok, "cookie"))
	if w.Code != 200 {
		t.Fatalf("first call: %d %s", w.Code, w.Body.String())
	}
	// The current user role must reach the registered operator gate even when
	// login saved admin. No parental/module fixture is necessary for rejection.
	r := sessionCheckRequest("/api/movies/id1", tok, "cookie")
	r.Method = http.MethodDelete
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 || !strings.Contains(w.Body.String(), "operator.forbidden") {
		t.Fatalf("demoted operator reached handler: %d %s", w.Code, w.Body.String())
	}
	revoked.Store(true)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, sessionCheckRequest("/api/me", tok, "cookie"))
	if w.Code != 401 || s.sessions.Valid(tok) || calls.Load() != 3 {
		t.Fatalf("next-request revocation: status=%d calls=%d", w.Code, calls.Load())
	}
}

func TestSessionRevalidationProviderRecoveryIsRetried(t *testing.T) {
	for _, first := range []codes.Code{codes.Unavailable, codes.PermissionDenied, codes.Unimplemented} {
		t.Run(first.String(), func(t *testing.T) {
			s := &server{sessions: newSessionStore(time.Hour)}
			tok := boundBFFSession(t, s)
			calls := 0
			s.auth = sessionValidatorFunc(func(context.Context, *authv1.ValidateRequest) (*authv1.ValidateResponse, error) {
				calls++
				if calls == 1 {
					return nil, status.Error(first, "fixture")
				}
				return validSessionResponse(), nil
			})
			h := s.withAuth(http.HandlerFunc(s.handleSessionMe))
			w := httptest.NewRecorder()
			h.ServeHTTP(w, sessionCheckRequest("/api/me", tok, "cookie"))
			if w.Code == 200 || !s.sessions.Valid(tok) {
				t.Fatal("first failure admitted/lost session")
			}
			w = httptest.NewRecorder()
			h.ServeHTTP(w, sessionCheckRequest("/api/me", tok, "cookie"))
			if w.Code != 200 || calls != 2 {
				t.Fatal("provider recovery not retried")
			}
		})
	}
}

func TestSessionRevalidationExchangeAndTenantCompatibility(t *testing.T) {
	// Auth-local login.go exchange/device/TOTP emit these canonical public
	// fields from GetUser; Validate emits the same fields from ValidateSession.
	// Both reads use COALESCE(users.tenant_id, ''), including household scope.
	for _, tenant := range []string{"", "household"} {
		t.Run("tenant="+tenant, func(t *testing.T) {
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				writeJSON(w, map[string]any{"token": "provider-bearer", "user_id": "u1", "username": "old", "tenant_id": tenant, "roles": []string{"admin"}, "claims": map[string]any{"tenant_id": tenant}})
			}))
			defer provider.Close()
			s := &server{sessions: newSessionStore(time.Hour), authInternal: provider.URL}
			tok, _, err := s.createSessionFromAuthCode("fixture-code")
			if err != nil {
				t.Fatal(err)
			}
			s.auth = sessionValidatorFunc(func(context.Context, *authv1.ValidateRequest) (*authv1.ValidateResponse, error) {
				resp := validSessionResponse()
				resp.TenantId = tenant
				resp.Username = ""
				resp.Roles = nil
				return resp, nil
			})
			w := httptest.NewRecorder()
			s.withAuth(http.HandlerFunc(s.handleSessionMe)).ServeHTTP(w, sessionCheckRequest("/api/me", tok, "cookie"))
			if w.Code != 200 {
				t.Fatalf("canonical exchange/Validate mismatch: %d %s", w.Code, w.Body.String())
			}
			var body map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body["username"] != "" || len(body["roles"].([]any)) != 0 {
				t.Fatal("empty canonical claims not authoritative")
			}
		})
	}
	// A claim-only tenant is accepted at login for existing provider
	// compatibility, but must not silently rebind when Validate omits it.
	s := &server{sessions: newSessionStore(time.Hour)}
	tok := boundBFFSession(t, s)
	s.auth = sessionValidatorFunc(func(context.Context, *authv1.ValidateRequest) (*authv1.ValidateResponse, error) {
		resp := validSessionResponse()
		resp.TenantId = ""
		return resp, nil
	})
	w := httptest.NewRecorder()
	s.withAuth(http.HandlerFunc(s.handleSessionMe)).ServeHTTP(w, sessionCheckRequest("/api/me", tok, "cookie"))
	if w.Code != 401 {
		t.Fatal("omitted tenant silently rebound local principal")
	}
}

func TestSessionRevalidationExpiryAndResponseShapes(t *testing.T) {
	for _, path := range []string{"/api/me", "/stream/movies/1", "/"} {
		t.Run(path, func(t *testing.T) {
			s := &server{sessions: newSessionStore(time.Hour), authHTTP: "https://auth.example", publicURL: "https://media.example"}
			tok := boundBFFSession(t, s)
			s.auth = sessionValidatorFunc(func(context.Context, *authv1.ValidateRequest) (*authv1.ValidateResponse, error) {
				return &authv1.ValidateResponse{}, nil
			})
			w := httptest.NewRecorder()
			s.withAuth(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("rejected session admitted") })).ServeHTTP(w, sessionCheckRequest(path, tok, "cookie"))
			want := 401
			if path == "/" {
				want = 303
				if !strings.HasPrefix(w.Header().Get("Location"), "https://auth.example/login?") {
					t.Fatal("browser not redirected to login")
				}
			}
			if w.Code != want || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("response: %d %v", w.Code, w.Header())
			}
		})
	}
	oldNow := timeNow
	now := time.Now()
	timeNow = func() time.Time { return now }
	defer func() { timeNow = oldNow }()
	s := newSessionStore(time.Hour)
	tok, err := s.Create("user", "user")
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	entry := s.byID[sessionID(tok)]
	entry.expiry = now
	s.byID[sessionID(tok)] = entry
	s.mu.Unlock()
	if _, ok := s.commitValidated(tok, entry, "current", nil); ok {
		t.Fatal("commit accepted exact expiry")
	}
	if s.Valid(tok) {
		t.Fatal("lookup accepted exact expiry")
	}
}

func TestSessionRevalidationShortParentDeadline(t *testing.T) {
	s := &server{sessions: newSessionStore(time.Hour)}
	tok := boundBFFSession(t, s)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	s.auth = sessionValidatorFunc(func(ctx context.Context, _ *authv1.ValidateRequest) (*authv1.ValidateResponse, error) {
		<-ctx.Done()
		return validSessionResponse(), nil
	})
	w := httptest.NewRecorder()
	s.withAuth(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("deadline-expired success admitted") })).ServeHTTP(w, sessionCheckRequest("/api/me", tok, "cookie").WithContext(ctx))
	if w.Code != 503 || !s.sessions.Valid(tok) {
		t.Fatal("deadline lost session or admitted request")
	}
}
