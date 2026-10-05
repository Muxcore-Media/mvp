package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/Muxcore-Media/core/sdk/go/module/meshid"
)

// defaultMeshModuleID is the BFF's mesh identity (certificate CN) when
// MUXCORE_MODULE_ID is unset; compose and run-host use the same ID.
const defaultMeshModuleID = "media-ui"

// ensureFunc is meshid.Ensure (swapped in tests).
type ensureFunc func(context.Context, meshid.Config) (meshid.Paths, error)

// ensureMeshIdentity gives the BFF its mesh identity before any gRPC dial
// (ADR-0017): it reuses MUXCORE_TLS_CERT/KEY or the stored certificate in
// MUXCORE_TLS_DIR (default <MUXCORE_DATA_DIR>/mesh-id), or enrolls with core
// using MUXCORE_BOOTSTRAP_TOKEN. meshid exports MUXCORE_TLS_CERT/KEY/CA, which
// meshGRPCDialOptions reads. In the insecure dev profile it does nothing; with
// the insecure flag in the household profile it fails (ADR-0016).
func ensureMeshIdentity(ctx context.Context, ensure ensureFunc, getenv func(string) string) (meshid.Paths, error) {
	moduleID := strings.TrimSpace(getenv(meshid.EnvModuleID))
	if moduleID == "" {
		moduleID = defaultMeshModuleID
	}
	paths, err := ensure(ctx, meshid.Config{
		Getenv:   getenv,
		ModuleID: moduleID,
		Insecure: meshInsecureAllowedFrom(getenv),
	})
	if err != nil {
		return meshid.Paths{}, fmt.Errorf("mesh identity for %q: %w", moduleID, err)
	}
	return paths, nil
}

// mustEnsureMeshIdentity is ensureMeshIdentity for main: failure is fatal, so
// the container restarts and retries (as SDK modules do).
func mustEnsureMeshIdentity() {
	paths, err := ensureMeshIdentity(context.Background(), meshid.Ensure, os.Getenv)
	if err != nil {
		log.Fatalf("media-ui: %v", err)
	}
	switch {
	case paths.Enrolled:
		log.Printf("media-ui: enrolled with core; mesh identity in %s", paths.Cert)
	case paths.Cert != "":
		log.Printf("media-ui: mesh identity %s", paths.Cert)
	}
}
