// healthtick periodically ReportHealths so health-monitor stays non-idle on host stacks.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	healthmonitorv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/healthmonitor/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:9202", "health-monitor gRPC address")
	moduleID := flag.String("module", "mvp-healthtick", "module id to report")
	interval := flag.Duration("interval", 30*time.Second, "report interval")
	flag.Parse()

	conn, err := grpc.NewClient(*addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		fmt.Fprintf(os.Stderr, "dial: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = conn.Close() }()
	hm := healthmonitorv1.NewHealthMonitorServiceClient(conn)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	report := func() {
		rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		_, err := hm.ReportHealth(rctx, &healthmonitorv1.ReportHealthRequest{
			Modules: []*healthmonitorv1.ModuleHealth{
				{ModuleId: *moduleID, State: "running"},
			},
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "ReportHealth: %v\n", err)
			return
		}
		fmt.Printf("ReportHealth ok module=%s\n", *moduleID)
	}

	report()
	t := time.NewTicker(*interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			report()
		}
	}
}
