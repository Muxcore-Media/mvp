package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	scannerv1 "github.com/Muxcore-Media/contracts-scanner/muxcore/scanner/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:9470", "media-scanner gRPC address")
	timeout := flag.Duration("timeout", 10*time.Minute, "scan timeout")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	conn, err := grpc.NewClient(*addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		fmt.Fprintf(os.Stderr, "dial: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = conn.Close() }()
	client := scannerv1.NewScannerServiceClient(conn)

	resp, err := client.ScanLibraryRoots(ctx, &scannerv1.ScanLibraryRootsRequest{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "ScanLibraryRoots: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("ScanLibraryRoots ok found=%d imported=%d skipped=%d\n",
		resp.GetFilesFound(), resp.GetFilesImported(), resp.GetFilesSkipped())
}
