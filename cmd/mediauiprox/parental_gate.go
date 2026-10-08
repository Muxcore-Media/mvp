package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Muxcore-Media/userdata-local/parental"
)

// Server-side parental enforcement (ADR-0031, FR-PLAY-007).
//
// The BFF is the single enforcement point. parentalGate resolves the signed-in
// principal's policy from the authoritative userdata-local resource
// (ADR-0030), never from request fields, the self-editable userdata blob or a
// local file. Every failure to establish the policy fails closed (§3): there is
// no fallback and no switch that turns enforcement off.

const (
	parentalPolicyPath = "/api/parental-policy"
	// parentalPolicyTTL bounds how long a validated policy is reused (ADR-0031
	// §4, matching the ADR-0019 identity cache bound).
	parentalPolicyTTL     = 30 * time.Second
	parentalPolicyTimeout = 5 * time.Second
	// The provider caps the policy at parental.MaxBodyBytes; the envelope adds
	// a few short fields.
	parentalPolicyMaxBody  = parental.MaxBodyBytes + 4<<10
	parentalPolicyCacheMax = 4096
)

// Error codes (BFF-API.md "Parental enforcement").
const (
	parentalCodeBlocked                   = "parental.blocked"
	parentalCodeRestrictedRoute           = "parental.restricted_route"
	parentalCodeUnconfigured              = "parental.policy_unconfigured"
	parentalCodeUnverifiable              = "parental.policy_unverifiable"
	parentalCodeSessionInvalid            = "parental.session_invalid"
	parentalCodeUnavailable               = "parental.policy_unavailable"
	parentalCodeClassificationUnavailable = "parental.classification_unavailable"
	// playbackCodeParentalBlocked is the legacy resolve code, kept as an alias
	// of parental.blocked on GET /api/playback/resolve.
	playbackCodeParentalBlocked = "playback.parental_blocked"
)

// parentalError is a gate decision that ends the request.
type parentalError struct {
	status  int
	code    string
	message string
}

func (e *parentalError) Error() string { return e.code + ": " + e.message }

var (
	errParentalBlocked = &parentalError{http.StatusForbidden, parentalCodeBlocked, "blocked by parental controls"}
	errParentalRoute   = &parentalError{http.StatusForbidden, parentalCodeRestrictedRoute, "not available with parental controls"}
	errParentalUnconf  = &parentalError{http.StatusForbidden, parentalCodeUnconfigured, "parental policy not configured for this account"}
	errParentalUnverif = &parentalError{http.StatusForbidden, parentalCodeUnverifiable, "parental policy cannot be verified for this session"}
	errParentalSession = &parentalError{http.StatusUnauthorized, parentalCodeSessionInvalid, "session is no longer valid"}
	errParentalUnavail = &parentalError{http.StatusServiceUnavailable, parentalCodeUnavailable, "parental policy unavailable"}
	errParentalClassif = &parentalError{http.StatusServiceUnavailable, parentalCodeClassificationUnavailable, "content classification unavailable"}
)

// writeParentalError writes a gate decision. The body carries only the code
// and a generic message, never item metadata. blockedAlias, when set, replaces
// parental.blocked in "code" and moves the canonical code to "parental_code".
func writeParentalError(w http.ResponseWriter, e *parentalError, blockedAlias string) {
	w.Header().Set("Cache-Control", "no-store")
	body := map[string]string{"error": e.message, "code": e.code}
	if blockedAlias != "" && e.code == parentalCodeBlocked {
		body["code"] = blockedAlias
		body["parental_code"] = e.code
	}
	writeJSONStatus(w, e.status, body)
}

// parentalPrincipal is the session identity the policy is requested for.
type parentalPrincipal struct {
	sessionID string // sessionID(raw BFF session token); binds HLS keys
	userID    string
	tenantID  string // session tenant as issued by auth; "" is the household scope
	bearer    string // signed-in user's auth-local token
}

// parentalPolicy is a provider document that passed envelope validation.
type parentalPolicy struct {
	configured bool
	revision   int64
	policy     parental.Policy // normalized; meaningful only when configured
}

func (p parentalPolicy) unrestricted() bool {
	return p.configured && p.policy.Mode == "unrestricted"
}

// parentalItem identifies the catalogue item behind a C-PLAY request. An empty
// ID means the item could not be identified, which classifies as unavailable.
type parentalItem struct {
	Kind string // "movie" | "series" | "episode" | "" (unknown)
	ID   string
}

// parentalClassifier supplies the trusted classification of one item. It must
// read the owning media module (ADR-0031 §2), never request or user data. An
// error is a lookup failure (503 parental.classification_unavailable).
type parentalClassifier interface {
	Classify(ctx context.Context, item parentalItem) (parental.Classification, error)
}

// unavailableClassifier is the fail-closed default for an unwired classifier.
// Production installs catalogClassifier after constructing the server.
type unavailableClassifier struct{}

func (unavailableClassifier) Classify(context.Context, parentalItem) (parental.Classification, error) {
	return parental.Classification{State: parental.Unavailable}, nil
}

type parentalCacheEntry struct {
	policy  parentalPolicy
	expires time.Time
}

type parentalGate struct {
	policyURL  string // "" when USERDATA_LOCAL_URL is unset or invalid
	client     *http.Client
	now        func() time.Time
	ttl        time.Duration
	classifier parentalClassifier
	hls        *hlsKeyBindings

	mu    sync.Mutex
	cache map[string]parentalCacheEntry
}

// newParentalGate builds the production gate for USERDATA_LOCAL_URL. An empty
// or unusable base leaves the gate without a provider; every gated request
// from a session then fails with 503 parental.policy_unavailable.
func newParentalGate(userdataBase string) *parentalGate {
	g := newParentalGateWith(userdataBase, newParentalPolicyClient(parentalPolicyTimeout), time.Now)
	if strings.TrimSpace(userdataBase) != "" && g.policyURL == "" {
		log.Printf("warn: USERDATA_LOCAL_URL %q is not an http(s) base URL; parental policy unavailable", userdataBase)
	}
	return g
}

// newParentalPolicyClient never follows redirects: a redirect is not a policy,
// and following it would resend the bearer.
func newParentalPolicyClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout:       timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// newParentalGateWith injects the HTTP client and clock (tests).
func newParentalGateWith(userdataBase string, client *http.Client, now func() time.Time) *parentalGate {
	return &parentalGate{
		policyURL:  parentalPolicyURL(userdataBase),
		client:     client,
		now:        now,
		ttl:        parentalPolicyTTL,
		classifier: unavailableClassifier{},
		hls:        newHLSKeyBindings(),
		cache:      map[string]parentalCacheEntry{},
	}
}

// parentalPolicyURL derives the fixed policy resource URL. The request never
// carries a query string or any client-supplied selector.
func parentalPolicyURL(base string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	u, err := url.Parse(base)
	if base == "" || err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" ||
		u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return ""
	}
	return base + parentalPolicyPath
}

func (g *parentalGate) hasProvider() bool { return g != nil && g.policyURL != "" }

// parentalCacheKey is SHA-256(bearer) plus the session user and tenant, so a
// cached document is only reused for the exact principal it was checked for.
func parentalCacheKey(p parentalPrincipal) string {
	sum := sha256.Sum256([]byte(p.bearer))
	return hex.EncodeToString(sum[:]) + "\x00" + p.userID + "\x00" + p.tenantID
}

// policy returns the principal's validated policy, from the cache when fresh.
// Only validated configured/unconfigured documents are cached; any error evicts
// the entry and is never cached (ADR-0031 §4).
func (g *parentalGate) policy(ctx context.Context, p parentalPrincipal) (parentalPolicy, *parentalError) {
	if !g.hasProvider() {
		return parentalPolicy{}, errParentalUnavail
	}
	key := parentalCacheKey(p)
	now := g.now()
	g.mu.Lock()
	if e, ok := g.cache[key]; ok && now.Before(e.expires) {
		g.mu.Unlock()
		return e.policy, nil
	}
	g.mu.Unlock()

	pol, perr := g.fetch(ctx, p)
	g.mu.Lock()
	defer g.mu.Unlock()
	if perr != nil {
		delete(g.cache, key)
		return parentalPolicy{}, perr
	}
	g.storeLocked(key, parentalCacheEntry{policy: pol, expires: now.Add(g.ttl)}, now)
	return pol, nil
}

func (g *parentalGate) storeLocked(key string, e parentalCacheEntry, now time.Time) {
	if len(g.cache) >= parentalPolicyCacheMax {
		for k, old := range g.cache {
			if !now.Before(old.expires) {
				delete(g.cache, k)
			}
		}
		if len(g.cache) >= parentalPolicyCacheMax {
			return // still full of live entries: skip caching, never fail open
		}
	}
	g.cache[key] = e
}

// fetch performs GET {USERDATA_LOCAL_URL}/api/parental-policy with exactly one
// bearer and the session user as the target selector.
func (g *parentalGate) fetch(ctx context.Context, p parentalPrincipal) (parentalPolicy, *parentalError) {
	ctx, cancel := context.WithTimeout(ctx, parentalPolicyTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.policyURL, nil)
	if err != nil {
		return parentalPolicy{}, errParentalUnavail
	}
	req.Header.Set("Authorization", "Bearer "+p.bearer)
	req.Header.Set(muxcoreUserIDHeader, p.userID)
	req.Header.Set("Accept", "application/json")
	resp, err := g.client.Do(req)
	if err != nil {
		log.Printf("parental: policy request for user %q failed: %v", p.userID, err)
		return parentalPolicy{}, errParentalUnavail
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, parentalPolicyMaxBody))
		_ = resp.Body.Close()
	}()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized:
		return parentalPolicy{}, errParentalSession
	default:
		log.Printf("parental: policy provider returned HTTP %d for user %q", resp.StatusCode, p.userID)
		return parentalPolicy{}, errParentalUnavail
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, parentalPolicyMaxBody+1))
	if err != nil || len(raw) > parentalPolicyMaxBody {
		log.Printf("parental: policy response for user %q unreadable or too large", p.userID)
		return parentalPolicy{}, errParentalUnavail
	}
	pol, err := decodeParentalDocument(raw, p)
	if err != nil {
		log.Printf("parental: policy response for user %q rejected: %v", p.userID, err)
		return parentalPolicy{}, errParentalUnavail
	}
	mode := "unconfigured"
	if pol.configured {
		mode = pol.policy.Mode
	}
	log.Printf("parental: policy user=%q revision=%d mode=%s", p.userID, pol.revision, mode)
	return pol, nil
}

// decodeParentalDocument validates the provider envelope strictly (ADR-0031
// §3): no unknown or duplicate fields, the scope must be exactly the session's
// user and tenant (an empty tenant is never rebound to "default"), and the
// state must be configured (revision > 0, valid policy) or unconfigured
// (revision 0, null policy).
func decodeParentalDocument(raw []byte, want parentalPrincipal) (parentalPolicy, error) {
	obj, err := decodeStrictJSONObject(raw)
	if err != nil {
		return parentalPolicy{}, err
	}
	for k := range obj {
		switch k {
		case "user_id", "tenant_id", "state", "revision", "policy", "updated_at":
		default:
			return parentalPolicy{}, fmt.Errorf("unknown field %q", k)
		}
	}
	for _, k := range []string{"user_id", "tenant_id", "state", "revision", "policy"} {
		if _, ok := obj[k]; !ok {
			return parentalPolicy{}, fmt.Errorf("missing field %q", k)
		}
	}
	userID, err := jsonString(obj["user_id"])
	if err != nil {
		return parentalPolicy{}, fmt.Errorf("user_id: %w", err)
	}
	tenantID, err := jsonString(obj["tenant_id"])
	if err != nil {
		return parentalPolicy{}, fmt.Errorf("tenant_id: %w", err)
	}
	if userID != want.userID || tenantID != want.tenantID {
		return parentalPolicy{}, errors.New("scope does not match the session")
	}
	state, err := jsonString(obj["state"])
	if err != nil {
		return parentalPolicy{}, fmt.Errorf("state: %w", err)
	}
	revision, err := jsonInt64(obj["revision"])
	if err != nil {
		return parentalPolicy{}, fmt.Errorf("revision: %w", err)
	}
	updatedAt := ""
	if v, ok := obj["updated_at"]; ok {
		if updatedAt, err = jsonString(v); err != nil {
			return parentalPolicy{}, fmt.Errorf("updated_at: %w", err)
		}
	}
	policyNull := bytes.Equal(bytes.TrimSpace(obj["policy"]), []byte("null"))
	switch state {
	case "unconfigured":
		if revision != 0 || !policyNull || updatedAt != "" {
			return parentalPolicy{}, errors.New("unconfigured document must have revision 0 and a null policy")
		}
		return parentalPolicy{}, nil
	case "configured":
		if revision <= 0 || policyNull {
			return parentalPolicy{}, errors.New("configured document must have a positive revision and a policy")
		}
		pol, err := parental.DecodePolicy(obj["policy"])
		if err != nil {
			return parentalPolicy{}, fmt.Errorf("policy: %w", err)
		}
		return parentalPolicy{configured: true, revision: revision, policy: pol}, nil
	default:
		return parentalPolicy{}, fmt.Errorf("unknown state %q", state)
	}
}

// decodeStrictJSONObject decodes one JSON object member by member so a
// duplicate key cannot silently override an earlier one, and rejects trailing
// documents.
func decodeStrictJSONObject(raw []byte) (map[string]json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, errors.New("object required")
	}
	obj := map[string]json.RawMessage{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, errors.New("invalid JSON")
		}
		key, ok := tok.(string)
		if !ok {
			return nil, errors.New("invalid field name")
		}
		if _, dup := obj[key]; dup {
			return nil, fmt.Errorf("duplicate field %q", key)
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, errors.New("invalid JSON")
		}
		obj[key] = v
	}
	if tok, err := dec.Token(); err != nil || tok != json.Delim('}') {
		return nil, errors.New("invalid JSON")
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("unexpected data after object")
	}
	return obj, nil
}

// jsonString accepts only a JSON string (json.Unmarshal would turn null into "").
func jsonString(raw json.RawMessage) (string, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '"' {
		return "", errors.New("string required")
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", errors.New("string required")
	}
	return s, nil
}

// jsonInt64 accepts only an integral JSON number.
func jsonInt64(raw json.RawMessage) (int64, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return 0, errors.New("integer required")
	}
	n, ok := v.(json.Number)
	if !ok {
		return 0, errors.New("integer required")
	}
	i, err := n.Int64()
	if err != nil {
		return 0, errors.New("integer required")
	}
	return i, nil
}

// itemClassifier returns the configured classifier, failing closed when unset.
func (g *parentalGate) itemClassifier() parentalClassifier {
	if g == nil || g.classifier == nil {
		return unavailableClassifier{}
	}
	return g.classifier
}

// authorizeItem evaluates a restricted policy against the item's trusted
// classification with the shared evaluator (userdata-local parental.Evaluate).
func (g *parentalGate) authorizeItem(ctx context.Context, pol parentalPolicy, item parentalItem) *parentalError {
	c := parental.Classification{State: parental.Unavailable}
	if item.ID != "" {
		var err error
		if c, err = g.itemClassifier().Classify(ctx, item); err != nil {
			log.Printf("parental: classification lookup for %s %q failed: %v", item.Kind, item.ID, err)
			return errParentalClassif
		}
	}
	if !parental.Evaluate(pol.policy, c).Allowed {
		return errParentalBlocked
	}
	return nil
}
