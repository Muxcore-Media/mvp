// Command smokeprotoset writes the protobuf descriptor set that the registry
// smoke (scripts/lib/registry-smoke.sh) hands to containerized grpcurl.
//
// Core and the modules do not register gRPC server reflection, so grpcurl needs
// the service descriptors up front. They are taken from the same generated Go
// packages the modules serve, so the set cannot drift from the wire contract;
// scripts/check-smoke-protoset_test.sh regenerates it and fails on a diff.
//
//	go run ./cmd/smokeprotoset -o proto/smoke.protoset
package main

import (
	"flag"
	"fmt"
	"os"
	"sort"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"

	_ "github.com/Muxcore-Media/contracts-automation/muxcore/automation/v1"
	_ "github.com/Muxcore-Media/contracts-scanner/muxcore/scanner/v1"
	_ "github.com/Muxcore-Media/core/proto/gen/muxcore/auth/v1"
	_ "github.com/Muxcore-Media/core/proto/gen/muxcore/discovery/v1"
	_ "github.com/Muxcore-Media/core/proto/gen/muxcore/healthmonitor/v1"
	_ "github.com/Muxcore-Media/jellyfin/proto/jellyfinv1"
	_ "github.com/Muxcore-Media/media-movies/proto/mgmntv1"
	_ "github.com/Muxcore-Media/media-root-folders/proto/rootsv1"
	_ "github.com/Muxcore-Media/media-tvshows/proto/tvmgmtv1"
)

// Services the registry smoke calls (registry-smoke.sh, acquisition-smoke.sh).
var services = []protoreflect.FullName{
	"muxcore.discovery.v1.DiscoveryService",
	"muxcore.auth.v1.AuthService",
	"muxcore.healthmonitor.v1.HealthMonitorService",
	"muxcore.media.movies.v1.MovieManagementService",
	"muxcore.media.tv.v1.TvManagementService",
	"muxcore.roots.v1.RootFolderService",
	"muxcore.scanner.v1.ScannerService",
	"muxcore.automation.v1.AutomationService",
	"muxcore.playback.jellyfin.v1.JellyfinBridge",
}

func main() {
	out := flag.String("o", "proto/smoke.protoset", "output descriptor set")
	flag.Parse()
	b, err := build()
	if err != nil {
		fmt.Fprintln(os.Stderr, "smokeprotoset:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(*out, b, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "smokeprotoset:", err)
		os.Exit(1)
	}
}

func build() ([]byte, error) {
	seen := map[string]bool{}
	var files []*descriptorpb.FileDescriptorProto
	var add func(fd protoreflect.FileDescriptor)
	add = func(fd protoreflect.FileDescriptor) {
		if seen[fd.Path()] {
			return
		}
		seen[fd.Path()] = true
		imps := fd.Imports()
		paths := make([]protoreflect.FileImport, 0, imps.Len())
		for i := 0; i < imps.Len(); i++ {
			paths = append(paths, imps.Get(i))
		}
		sort.Slice(paths, func(i, j int) bool { return paths[i].Path() < paths[j].Path() })
		for _, imp := range paths {
			add(imp.FileDescriptor)
		}
		files = append(files, protodesc.ToFileDescriptorProto(fd)) // dependencies first
	}
	for _, name := range services {
		d, err := protoregistry.GlobalFiles.FindDescriptorByName(name)
		if err != nil {
			return nil, fmt.Errorf("service %s: %w", name, err)
		}
		if _, ok := d.(protoreflect.ServiceDescriptor); !ok {
			return nil, fmt.Errorf("%s is not a service", name)
		}
		add(d.ParentFile())
	}
	return proto.MarshalOptions{Deterministic: true}.Marshal(&descriptorpb.FileDescriptorSet{File: files})
}
