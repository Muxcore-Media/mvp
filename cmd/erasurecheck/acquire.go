package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	authv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/auth/v1"
	discoveryv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/discovery/v1"
	"github.com/Muxcore-Media/core/sdk/go/module/erasure"
	"github.com/Muxcore-Media/core/sdk/go/module/meshtls"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
)

const (
	providerRPCBudget = 30 * time.Second
	maxBearerBytes    = 8 << 10
	ledgerPageSize    = 100
	maxLedgerPages    = 100
	maxLedgerToken    = 4096
)

type providerConfig struct {
	callerID, coreAddr, caFile, certFile, keyFile string
}

func providerConfigFromEnv() (providerConfig, error) {
	get := func(key string) string { return strings.TrimSpace(os.Getenv(key)) }
	c := providerConfig{callerID: get("MUXCORE_MODULE_ID"), coreAddr: get("MUXCORE_GRPC_ADDR"),
		caFile: get(meshtls.EnvTLSCA), certFile: get(meshtls.EnvTLSCert), keyFile: get(meshtls.EnvTLSKey)}
	if meshtls.Insecure() || !moduleIDPattern.MatchString(c.callerID) || c.coreAddr == "" {
		return c, checkFailure("provider_config")
	}
	if (c.certFile == "") != (c.keyFile == "") {
		return c, checkFailure("provider_config")
	}
	// A separate process cannot inherit paths exported inside the daemon by
	// meshid.Ensure. Read its existing directory only; never enroll or write.
	if dir := get("MUXCORE_TLS_DIR"); dir != "" {
		if c.certFile == "" {
			c.certFile, c.keyFile = filepath.Join(dir, "module.crt"), filepath.Join(dir, "module.key")
		}
		if c.caFile == "" {
			c.caFile = filepath.Join(dir, "ca.crt")
		}
	}
	if c.certFile == "" || c.keyFile == "" || c.caFile == "" {
		return c, checkFailure("provider_config")
	}
	return c, nil
}

// The open seam permits deterministic replacement/growth tests. Production
// always uses os.Open. File I/O is separate from the bounded RPC phase.
type bearerInput interface {
	io.Reader
	Stat() (os.FileInfo, error)
	Close() error
}

func openAdminBearer(path string) (bearerInput, error) { return os.Open(path) }

func readAdminBearer(path string, open func(string) (bearerInput, error)) (string, error) {
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Size() > maxBearerBytes {
		return "", checkFailure("admin_input")
	}
	f, err := open(path)
	if err != nil {
		return "", checkFailure("admin_input")
	}
	defer func() { _ = f.Close() }()
	after, err := f.Stat()
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(before, after) || after.Size() > maxBearerBytes {
		return "", checkFailure("admin_input")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxBearerBytes+1))
	if err != nil || len(data) > maxBearerBytes {
		return "", checkFailure("admin_input")
	}
	token := strings.TrimSpace(string(data))
	if token == "" {
		return "", checkFailure("admin_input")
	}
	// gRPC metadata carries the raw printable ASCII session, never a Bearer
	// prefix or multi-token/header value. The provider validates the opaque
	// session; the checker does not infer credential type from a token prefix.
	for _, b := range []byte(token) {
		if b < 33 || b > 126 {
			return "", checkFailure("admin_input")
		}
	}
	return token, nil
}

func runProvider(expected Expected, bearerFile string, stdout, stderr io.Writer) int {
	failure := func(err error) int {
		_, _ = fmt.Fprintln(stderr, "FAIL provider acknowledgment check "+err.Error())
		return 1
	}
	cfg, err := providerConfigFromEnv()
	if err != nil {
		return failure(err)
	}
	bearer, err := readAdminBearer(bearerFile, openAdminBearer)
	if err != nil {
		return failure(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), providerRPCBudget)
	defer cancel()
	if err := acquireProvider(ctx, cfg, expected, bearer); err != nil {
		return failure(err)
	}
	if _, err := fmt.Fprintf(stdout, "OK provider acknowledgment check required=%d\n", len(expected.Required)); err != nil {
		return failure(checkFailure("output_write"))
	}
	return 0
}

// acquireProvider returns only local fixed errors or Check's fixed failures.
// Never expose SDK, filesystem, gRPC or peer-supplied diagnostics to the CLI.
func acquireProvider(ctx context.Context, cfg providerConfig, expected Expected, bearer string) error {
	d := &erasure.ProviderDialer{CAFile: cfg.caFile, CertFile: cfg.certFile, KeyFile: cfg.keyFile}
	tlsConfig, err := d.TLSConfig("muxcored", cfg.callerID)
	if err != nil {
		return checkFailure("provider_tls")
	}
	core, err := grpc.NewClient(cfg.coreAddr, grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(maxSnapshotBytes)))
	if err != nil {
		return checkFailure("provider_discovery")
	}
	defer func() { _ = core.Close() }()
	d.Discovery = discoveryv1.NewDiscoveryServiceClient(core)
	// Replace any inherited metadata; bearer credentials never reach discovery
	// or ledger List, whose admission explicitly forbids end-user tokens.
	readCtx := metadata.NewOutgoingContext(ctx, metadata.MD{})
	provider, err := d.Dial(readCtx, cfg.callerID)
	if err != nil {
		return checkFailure("provider_discovery")
	}
	defer func() { _ = provider.Close() }()
	tombstone, err := acquireTombstone(readCtx, provider.Client, expected)
	if err != nil {
		return err
	}
	adminCtx := metadata.NewOutgoingContext(ctx, metadata.Pairs("x-auth-token", bearer))
	response, err := provider.Client.GetUserErasureStatus(adminCtx,
		&authv1.GetUserErasureStatusRequest{ErasureId: expected.ErasureID}, grpc.MaxCallRecvMsgSize(maxSnapshotBytes))
	if err != nil {
		return checkFailure("provider_status")
	}
	return Check(expected, tombstone, response)
}

func acquireTombstone(ctx context.Context, client authv1.AuthServiceClient, expected Expected) (*authv1.UserErasure, error) {
	token := ""
	seenTokens := map[string]bool{"": true}
	seenIDs := make(map[string]bool)
	for range maxLedgerPages {
		response, err := client.ListUserErasures(ctx, &authv1.ListUserErasuresRequest{PageSize: ledgerPageSize, PageToken: token},
			grpc.MaxCallRecvMsgSize(maxSnapshotBytes))
		if err != nil {
			return nil, checkFailure("provider_ledger")
		}
		if response == nil || len(response.Erasures) > ledgerPageSize {
			return nil, checkFailure("provider_page")
		}
		next := response.NextPageToken
		if len(next) > maxLedgerToken || (next != "" && (seenTokens[next] || len(response.Erasures) == 0)) {
			return nil, checkFailure("provider_page")
		}
		var found *authv1.UserErasure
		for _, stone := range response.Erasures {
			if stone == nil || !erasureIDPattern.MatchString(stone.GetErasureId()) ||
				!validIdentityField(stone.GetUserId(), true) || !validIdentityField(stone.GetTenantId(), false) || seenIDs[stone.GetErasureId()] {
				return nil, checkFailure("provider_page")
			}
			if _, err := parseUTC(stone.GetDeletedAt()); err != nil {
				return nil, checkFailure("provider_page")
			}
			seenIDs[stone.ErasureId] = true
			if stone.ErasureId == expected.ErasureID {
				if stone.UserId != expected.UserID || stone.TenantId != expected.TenantID {
					return nil, errBinding
				}
				found = stone
			}
		}
		// Validate the entire page and continuation first. The provider contract
		// makes E unique and immutable; unvisited pages are not scanned.
		if found != nil {
			return found, nil
		}
		if next == "" {
			return nil, checkFailure("provider_target")
		}
		seenTokens[next], token = true, next
	}
	return nil, checkFailure("provider_limit")
}
