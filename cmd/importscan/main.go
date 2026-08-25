package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	scannerv1 "github.com/Muxcore-Media/contracts-scanner/muxcore/scanner/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:9470", "media-scanner gRPC address")
	watch := flag.String("watch", "", "download/watch directory (required)")
	library := flag.String("library", "", "organized library root (required)")
	name := flag.String("name", "Fight.Club.1999.1080p.BluRay.mkv", "fixture release filename")
	flag.Parse()
	if *watch == "" || *library == "" {
		fmt.Fprintln(os.Stderr, "-watch and -library are required")
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := grpc.NewClient(*addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		fmt.Fprintf(os.Stderr, "dial: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = conn.Close() }()
	client := scannerv1.NewScannerServiceClient(conn)

	if err := os.MkdirAll(*watch, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "mkdir watch: %v\n", err)
		os.Exit(1)
	}
	if err := os.MkdirAll(*library, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "mkdir library: %v\n", err)
		os.Exit(1)
	}

	// Ensure watch dir registered (idempotent enough for smoke).
	list, err := client.ListWatchDirs(ctx, &scannerv1.ListWatchDirsRequest{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "ListWatchDirs: %v\n", err)
		os.Exit(1)
	}
	found := false
	watchAbs, _ := filepath.Abs(*watch)
	libAbs := mustAbs(*library)
	for _, d := range list.GetDirs() {
		if samePath(d.GetPath(), watchAbs) {
			found = true
			if strings.TrimSpace(d.GetLibraryPath()) == "" {
				// Replace empty auto-registered entry with a proper library path.
				if _, err := client.RemoveWatchDir(ctx, &scannerv1.RemoveWatchDirRequest{Id: d.GetId()}); err != nil {
					fmt.Fprintf(os.Stderr, "RemoveWatchDir: %v\n", err)
					os.Exit(1)
				}
				found = false
			}
			break
		}
	}
	if !found {
		if _, err := client.AddWatchDir(ctx, &scannerv1.AddWatchDirRequest{
			Path:        watchAbs,
			LibraryPath: libAbs,
			MediaType:   "movie",
		}); err != nil {
			fmt.Fprintf(os.Stderr, "AddWatchDir: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("registered watch=%s library=%s\n", watchAbs, libAbs)
	}

	releaseDir := filepath.Join(watchAbs, strings.TrimSuffix(*name, filepath.Ext(*name)))
	if err := os.MkdirAll(releaseDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "mkdir release: %v\n", err)
		os.Exit(1)
	}
	src := filepath.Join(releaseDir, *name)
	// ~6 KiB placeholder is fine when SCANNER_MIN_VIDEO_BYTES=0
	payload := make([]byte, 8*1024)
	for i := range payload {
		payload[i] = byte(i % 251)
	}
	if err := os.WriteFile(src, payload, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "write fixture: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("fixture %s (%d bytes)\n", src, len(payload))

	imp, err := client.ImportPath(ctx, &scannerv1.ImportPathRequest{Path: releaseDir})
	if err != nil {
		fmt.Fprintf(os.Stderr, "ImportPath: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("ImportPath found=%d imported=%d skipped=%d\n",
		imp.GetFilesFound(), imp.GetFilesImported(), imp.GetFilesSkipped())

	listed, err := client.ListImported(ctx, &scannerv1.ListImportedRequest{PageSize: 50, MediaType: "movie"})
	if err != nil {
		fmt.Fprintf(os.Stderr, "ListImported: %v\n", err)
		os.Exit(1)
	}
	ok := false
	for _, f := range listed.GetFiles() {
		if strings.Contains(strings.ToLower(f.GetTitle()), "fight club") ||
			strings.Contains(strings.ToLower(f.GetFileName()), "fight.club") {
			ok = true
			fmt.Printf("listed title=%q dest=%s status=%s\n", f.GetTitle(), f.GetDestinationPath(), f.GetStatus())
			break
		}
	}

	// Destination should exist under library Movies/
	var matches []string
	_ = filepath.Walk(mustAbs(*library), func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		if strings.Contains(strings.ToLower(info.Name()), "fight") {
			matches = append(matches, path)
		}
		return nil
	})

	if imp.GetFilesImported() < 1 {
		// Idempotent smoke: prior run may have already imported; accept skip if
		// ListImported + library file still prove the path works.
		if ok && len(matches) > 0 {
			fmt.Printf("ImportPath skipped (already imported); library file %s\n", matches[0])
			return
		}
		fmt.Fprintln(os.Stderr, "expected at least one imported file")
		os.Exit(1)
	}
	if !ok {
		fmt.Fprintln(os.Stderr, "Fight Club not found in ListImported")
		os.Exit(1)
	}
	if len(matches) == 0 {
		fmt.Fprintln(os.Stderr, "imported library file not found under Movies/")
		os.Exit(1)
	}
	fmt.Printf("library file %s\n", matches[0])
}

func mustAbs(p string) string {
	a, err := filepath.Abs(p)
	if err != nil {
		fmt.Fprintf(os.Stderr, "abs: %v\n", err)
		os.Exit(1)
	}
	return a
}

func samePath(a, b string) bool {
	aa, _ := filepath.Abs(a)
	bb, _ := filepath.Abs(b)
	return filepath.Clean(aa) == filepath.Clean(bb)
}
