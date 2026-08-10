package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Muxcore-Media/core/sdk/go/client"
)

func main() {
	addr := "127.0.0.1:9090"
	if v := os.Getenv("MUXCORE_GRPC_ADDR"); v != "" {
		addr = v
	}
	need := []string{
		"api-rest", "auth-local", "database-sqlite", "secrets-file", "encryption-aesgcm",
		"call-policy-default", "publish-policy-default", "metadata-tmdb", "media-movies",
		"media-tvshows", "media-automation", "media-scanner", "media-root-folders", "jellyfin",
		"request-media", "notification-default",
	}
	if v := os.Getenv("SMOKE_MODULES"); v != "" {
		need = strings.Split(v, ",")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	c, err := client.Dial(addr, client.WithInsecure())
	if err != nil {
		fmt.Fprintf(os.Stderr, "dial: %v\n", err)
		os.Exit(1)
	}
	defer c.Close()

	missing := 0
	for _, id := range need {
		info, err := c.Discovery.Resolve(ctx, id)
		if err != nil || info == nil || info.GetId() == "" {
			fmt.Fprintf(os.Stderr, "MISSING %s (%v)\n", id, err)
			missing++
			continue
		}
		fmt.Printf("OK %s\n", info.GetId())
	}
	if missing != 0 {
		os.Exit(1)
	}
}
