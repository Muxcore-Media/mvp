package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Muxcore-Media/core/sdk/go/client"
)

type catalogFile struct {
	Modules []struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"modules"`
}

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

	wantVer := map[string]string{}
	if path := os.Getenv("SMOKE_CATALOG"); path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "catalog: %v\n", err)
			os.Exit(1)
		}
		var cat catalogFile
		if err := json.Unmarshal(raw, &cat); err != nil {
			fmt.Fprintf(os.Stderr, "catalog json: %v\n", err)
			os.Exit(1)
		}
		for _, m := range cat.Modules {
			wantVer[m.Name] = strings.TrimPrefix(m.Version, "v")
		}
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
	mismatch := 0
	for _, id := range need {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		info, err := c.Discovery.Resolve(ctx, id)
		if err != nil || info == nil || info.GetId() == "" {
			fmt.Fprintf(os.Stderr, "MISSING %s (%v)\n", id, err)
			missing++
			continue
		}
		live := strings.TrimPrefix(info.GetVersion(), "v")
		if live == "" {
			fmt.Printf("OK %s\n", info.GetId())
			continue
		}
		if pin, ok := wantVer[id]; ok && pin != "" && live != pin {
			fmt.Fprintf(os.Stderr, "VERSION %s live=%s catalog=%s\n", id, live, pin)
			mismatch++
			continue
		}
		fmt.Printf("OK %s %s\n", info.GetId(), live)
	}
	if missing != 0 || mismatch != 0 {
		os.Exit(1)
	}
}
