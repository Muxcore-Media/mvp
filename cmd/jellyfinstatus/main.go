package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	jellyfinv1 "github.com/Muxcore-Media/jellyfin/proto/jellyfinv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:9475", "jellyfin gRPC address")
	requireConfigured := flag.Bool("require-configured", false, "fail unless Status.Configured is true")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := grpc.NewClient(*addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		fmt.Fprintf(os.Stderr, "dial: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	st, err := jellyfinv1.NewJellyfinBridgeClient(conn).Status(ctx, &jellyfinv1.StatusRequest{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Status: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("configured=%v base_url=%q conflict_mode=%q links=%d\n",
		st.GetConfigured(), st.GetBaseUrl(), st.GetConflictMode(), st.GetItemLinks())
	if *requireConfigured && !st.GetConfigured() {
		fmt.Fprintln(os.Stderr, "jellyfin bridge is not configured (set JELLYFIN_BASE_URL + JELLYFIN_API_KEY)")
		os.Exit(1)
	}
}
