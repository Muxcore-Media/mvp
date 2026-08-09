package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	healthmonitorv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/healthmonitor/v1"
	"github.com/Muxcore-Media/core/sdk/go/client"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:9202", "health-monitor gRPC address")
	statusURL := flag.String("status-url", "http://127.0.0.1:9203/status", "HTTP /status URL")
	moduleID := flag.String("module", "mvp-smoke", "module id to report")
	mesh := flag.String("mesh", "127.0.0.1:9090", "core mesh addr (subscribe module.degraded)")
	skipMesh := flag.Bool("skip-mesh", false, "skip mesh fan-out assertion")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	conn, err := grpc.NewClient(*addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		fmt.Fprintf(os.Stderr, "dial: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()
	hm := healthmonitorv1.NewHealthMonitorServiceClient(conn)

	_, err = hm.ReportHealth(ctx, &healthmonitorv1.ReportHealthRequest{
		Modules: []*healthmonitorv1.ModuleHealth{
			{ModuleId: *moduleID, State: "running"},
			{ModuleId: "api-rest", State: "running"},
		},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "ReportHealth: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("ReportHealth ok module=%s (+api-rest)\n", *moduleID)

	_, err = hm.PublishEvent(ctx, &healthmonitorv1.PublishEventRequest{
		EventType: "health.smoke",
		ModuleId:  *moduleID,
		Message:   "health-monitor smoke",
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "PublishEvent: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("PublishEvent ok")

	if !*skipMesh && *mesh != "" {
		if err := assertMeshDegraded(ctx, *mesh, *moduleID, hm); err != nil {
			fmt.Fprintf(os.Stderr, "mesh fan-out: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("OK mesh fan-out module.degraded")
	}

	resp, err := http.Get(*statusURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "GET /status: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		fmt.Fprintf(os.Stderr, "GET /status HTTP %d: %s\n", resp.StatusCode, body)
		os.Exit(1)
	}

	var st struct {
		Status        string `json:"status"`
		ModuleCount   int    `json:"module_count"`
		EventsPublish int64  `json:"events_published"`
		MeshPublished int64  `json:"mesh_published"`
		Modules       []struct {
			ModuleID string `json:"module_id"`
			State    string `json:"state"`
		} `json:"modules"`
	}
	if err := json.Unmarshal(body, &st); err != nil {
		fmt.Fprintf(os.Stderr, "decode /status: %v\n%s\n", err, body)
		os.Exit(1)
	}
	if st.ModuleCount < 2 {
		fmt.Fprintf(os.Stderr, "expected >=2 modules in /status, got %d\n%s\n", st.ModuleCount, body)
		os.Exit(1)
	}
	if st.EventsPublish < 1 {
		fmt.Fprintf(os.Stderr, "expected events_published >= 1\n%s\n", body)
		os.Exit(1)
	}
	if !*skipMesh && st.MeshPublished < 1 {
		fmt.Fprintf(os.Stderr, "expected mesh_published >= 1\n%s\n", body)
		os.Exit(1)
	}
	found := false
	for _, mod := range st.Modules {
		if mod.ModuleID == *moduleID {
			found = true
			break
		}
	}
	if !found {
		fmt.Fprintf(os.Stderr, "module %q missing in /status\n%s\n", *moduleID, body)
		os.Exit(1)
	}
	fmt.Printf("OK /status status=%s modules=%d events=%d mesh=%d\n",
		st.Status, st.ModuleCount, st.EventsPublish, st.MeshPublished)
}

func assertMeshDegraded(ctx context.Context, mesh, moduleID string, hm healthmonitorv1.HealthMonitorServiceClient) error {
	c, err := client.Dial(mesh, client.WithInsecure())
	if err != nil {
		return fmt.Errorf("dial mesh: %w", err)
	}
	defer c.Close()

	ch, cancel, err := c.Events.Subscribe(ctx, "module.degraded")
	if err != nil {
		return fmt.Errorf("subscribe: %w", err)
	}
	defer cancel()

	// Give subscription a moment to attach before publishing.
	time.Sleep(200 * time.Millisecond)

	_, err = hm.ReportHealth(ctx, &healthmonitorv1.ReportHealthRequest{
		Modules: []*healthmonitorv1.ModuleHealth{
			{ModuleId: moduleID, State: "degraded", Error: "mvp smoke degraded"},
		},
	})
	if err != nil {
		return fmt.Errorf("ReportHealth degraded: %w", err)
	}

	deadline := time.After(8 * time.Second)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline:
			return fmt.Errorf("timeout waiting for module.degraded on mesh")
		case ev, ok := <-ch:
			if !ok {
				return fmt.Errorf("subscription closed")
			}
			if ev.GetType() != "module.degraded" {
				continue
			}
			var payload struct {
				ModuleID string `json:"module_id"`
			}
			_ = json.Unmarshal(ev.GetPayload(), &payload)
			if payload.ModuleID == moduleID || ev.GetSource() == "health-monitor" {
				fmt.Printf("mesh event type=%s source=%s module=%s\n",
					ev.GetType(), ev.GetSource(), payload.ModuleID)
				return nil
			}
		}
	}
}
