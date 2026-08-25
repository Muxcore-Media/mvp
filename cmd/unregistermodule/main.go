// Command unregistermodule removes a stale mesh registration by module ID.
// Use when a sidecar died without calling Unregister (e.g. kill -9) and
// restart fails with "already registered".
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	modulev1 "github.com/Muxcore-Media/core/proto/gen/muxcore/module/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:9090", "muxcored mesh gRPC address")
	insec := flag.Bool("insecure", true, "use insecure credentials (dev host default)")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintf(os.Stderr, "usage: %s [-addr host:port] [-insecure] <module-id>\n", os.Args[0])
		os.Exit(2)
	}
	id := flag.Arg(0)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var opts []grpc.DialOption
	if *insec {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	} else {
		fmt.Fprintln(os.Stderr, "tls dial not wired in this helper; use -insecure on the default host")
		os.Exit(2)
	}

	conn, err := grpc.NewClient(*addr, opts...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dial: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = conn.Close() }()

	_, err = modulev1.NewModuleRegistrationClient(conn).Unregister(ctx, &modulev1.UnregisterRequest{ModuleId: id})
	if err != nil {
		fmt.Fprintf(os.Stderr, "unregister %s: %v\n", id, err)
		os.Exit(1)
	}
	fmt.Println("unregistered", id)
}
