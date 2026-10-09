package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/Muxcore-Media/userdata-local/httpclient"
)

// Checked userdata-local HTTP transport (ADR-0033, slice S9b).
//
// The parental policy read (parental_gate.go) and the userdata blob proxy
// (userdata.go) share one published userdata-local httpclient. In secure
// operation (household/staging, or no insecure flag) it dials an https://
// origin with the BFF's own enrolled mesh certificate, verifies the provider
// against the explicit core CA with the fixed service name and exact CN
// "userdata-local", never follows redirects, ignores proxy environment and is
// bound to that one origin. Only explicit insecure dev
// (MUXCORE_INSECURE_DISABLE_TLS=true) uses plaintext http://, and there is no
// fallback from one mode to the other.

// userdataProviderClient is the subset of *httpclient.Client the BFF uses.
type userdataProviderClient interface {
	Do(ctx context.Context, op httpclient.Operation, headers http.Header, body io.Reader) (*http.Response, error)
}

// userdataProviderOrigin normalizes USERDATA_LOCAL_URL to an origin: space and
// trailing slashes are trimmed (so the historical "http://host:9672/" form
// stays valid). Anything else — a path, query, fragment or credentials — is
// left for httpclient to reject.
func userdataProviderOrigin(raw string) string {
	return strings.TrimRight(strings.TrimSpace(raw), "/")
}

// newUserdataProviderClient builds the BFF's checked provider client for
// USERDATA_LOCAL_URL. It must run after mustEnsureMeshIdentity: meshid exports
// MUXCORE_TLS_CERT/KEY/CA, which httpclient.FromEnv resolves. moduleID is the
// identity the provider admits for this caller; main passes the fixed
// defaultMeshModuleID ("media-ui"), so a different MUXCORE_MODULE_ID override
// (whose certificate the provider would refuse at request time) is a startup
// configuration error here. A nil client and nil error mean the URL is unset;
// an error is a configuration failure and is never retried in plaintext.
func newUserdataProviderClient(rawURL, moduleID string) (userdataProviderClient, error) {
	origin := userdataProviderOrigin(rawURL)
	if origin == "" {
		return nil, nil
	}
	cfg, err := httpclient.FromEnv(origin, moduleID)
	if err != nil {
		return nil, err
	}
	c, err := httpclient.New(cfg)
	if err != nil {
		return nil, err
	}
	return c, nil
}

// mustUserdataProvider is newUserdataProviderClient for main. A configuration
// error is logged and leaves the BFF without a provider: parental policy then
// fails closed (503 parental.policy_unavailable) and userdata blobs use only
// the local store. It never downgrades to plaintext.
func mustUserdataProvider(rawURL, moduleID string) userdataProviderClient {
	c, err := newUserdataProviderClient(rawURL, moduleID)
	if err != nil {
		log.Printf("warn: USERDATA_LOCAL_URL %s: userdata provider transport unusable (ADR-0033): %v; "+
			"parental policy is unavailable and userdata uses the local store only", redactURLForLog(rawURL), err)
		return nil
	}
	if c != nil {
		log.Printf("media-ui: userdata provider %s as %q", redactURLForLog(rawURL), moduleID)
	}
	return c
}

// redactURLForLog renders a configured URL for logs without userinfo, query or
// fragment (an operator typo such as https://user:pass@host must not leak the
// password); an unparseable value is not echoed at all.
func redactURLForLog(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return `"<unparseable URL>"`
	}
	if u.User != nil {
		u.User = url.User("REDACTED")
	}
	if u.RawQuery != "" || u.ForceQuery {
		u.RawQuery, u.ForceQuery = "REDACTED", false
	}
	if u.Fragment != "" || u.RawFragment != "" {
		u.Fragment, u.RawFragment = "REDACTED", ""
	}
	return strconv.Quote(u.String())
}

// startupUserdataProvider is main's provider client: USERDATA_LOCAL_URL bound
// to the BFF's fixed identity. The provider admits the BFF only as media-ui, so
// a MUXCORE_MODULE_ID override is rejected here at startup (logged; parental
// policy then fails closed) instead of surfacing as module_forbidden 503s.
func startupUserdataProvider() userdataProviderClient {
	return mustUserdataProvider(os.Getenv("USERDATA_LOCAL_URL"), defaultMeshModuleID)
}

// describeProviderError renders a provider transport failure for logs. It
// carries the typed reason (module_forbidden, transport, redirect, response)
// and the cause, never request headers or response bodies.
func describeProviderError(err error) string {
	var ue *httpclient.UnavailableError
	if errors.As(err, &ue) {
		if ue.Cause != nil {
			return fmt.Sprintf("provider unavailable (reason=%s): %v", ue.Reason, ue.Cause)
		}
		return fmt.Sprintf("provider unavailable (reason=%s)", ue.Reason)
	}
	return err.Error()
}
