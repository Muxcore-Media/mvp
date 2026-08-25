package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	authv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/auth/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:9403", "auth-local gRPC address")
	user := flag.String("user", "admin", "username")
	pass := flag.String("password", "", "password")
	out := flag.String("out", "", "optional path to write token")
	flag.Parse()
	if *pass == "" {
		fmt.Fprintln(os.Stderr, "-password is required")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := grpc.NewClient(*addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		fmt.Fprintf(os.Stderr, "dial: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = conn.Close() }()
	cred, _ := json.Marshal(map[string]string{"username": *user, "password": *pass})
	resp, err := authv1.NewAuthServiceClient(conn).Authenticate(ctx, &authv1.AuthenticateRequest{
		CredentialType: "password",
		CredentialData: cred,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "authenticate: %v\n", err)
		os.Exit(1)
	}
	if !resp.GetAuthenticated() || resp.GetSessionToken() == "" {
		fmt.Fprintf(os.Stderr, "auth failed: %s\n", resp.GetError())
		os.Exit(1)
	}
	token := resp.GetSessionToken()
	if *out != "" {
		if err := os.WriteFile(*out, []byte(token+"\n"), 0o600); err != nil {
			fmt.Fprintf(os.Stderr, "write token: %v\n", err)
			os.Exit(1)
		}
	}
	fmt.Println(token)
}
