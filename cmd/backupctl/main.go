// backupctl calls the backup-local BackupService over plaintext gRPC and prints
// the response as protojson (the same JSON shape grpcurl prints). Used by
// scripts/restore-drill.sh in host mode (FR-BAK-004); the registry/compose path
// uses containerized grpcurl (scripts/lib/registry-smoke.sh).
//
//	backupctl [-addr 127.0.0.1:9302] create [-source DIR]...
//	backupctl verify -id ID [-restore-test]
//	backupctl restore -id ID -target DIR
//	backupctl list
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	backupv1 "github.com/Muxcore-Media/backup-local/muxcore/backup/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type multiFlag []string

func (m *multiFlag) String() string     { return fmt.Sprint([]string(*m)) }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

const usage = `usage: backupctl [-addr host:port] [-timeout d] <create|verify|restore|list> [flags]
  create  [-source DIR]...          CreateBackup (sources added to BACKUP_SOURCE_DIRS)
  verify  -id ID [-restore-test]    VerifyBackup
  restore -id ID -target DIR        RestoreBackup into DIR
  list                              ListBackups`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	global := flag.NewFlagSet("backupctl", flag.ContinueOnError)
	global.SetOutput(stderr)
	addr := global.String("addr", envOr("BACKUP_GRPC_CLIENT_ADDR", "127.0.0.1:9302"), "backup-local gRPC address")
	timeout := global.Duration("timeout", 10*time.Minute, "per-call timeout")
	global.Usage = func() { _, _ = fmt.Fprintln(stderr, usage) }
	if err := global.Parse(args); err != nil {
		return 2
	}
	rest := global.Args()
	if len(rest) == 0 {
		global.Usage()
		return 2
	}
	req, method, err := parseCommand(rest[0], rest[1:], stderr)
	if err != nil {
		if !errors.Is(err, flag.ErrHelp) {
			_, _ = fmt.Fprintf(stderr, "backupctl: %v\n%s\n", err, usage)
		}
		return 2
	}

	conn, err := grpc.NewClient(*addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "backupctl: dial %s: %v\n", *addr, err)
		return 1
	}
	defer func() { _ = conn.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	resp, err := call(ctx, backupv1.NewBackupServiceClient(conn), method, req)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "backupctl: %s: %v\n", method, err)
		return 1
	}
	out, err := protojson.Marshal(resp)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "backupctl: encode: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprintln(stdout, string(out))
	return 0
}

// parseCommand validates a subcommand and its flags without dialing.
func parseCommand(cmd string, args []string, stderr io.Writer) (proto.Message, string, error) {
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	var sources multiFlag
	id := fs.String("id", "", "backup id")
	target := fs.String("target", "", "restore target directory")
	restoreTest := fs.Bool("restore-test", false, "extract to a temp dir to validate the archive")
	if cmd == "create" {
		fs.Var(&sources, "source", "extra source directory (repeatable)")
	}
	if err := fs.Parse(args); err != nil {
		return nil, "", err
	}
	if fs.NArg() > 0 {
		return nil, "", fmt.Errorf("%s: unexpected arguments %v", cmd, fs.Args())
	}
	switch cmd {
	case "create":
		return &backupv1.CreateBackupRequest{SourcePaths: sources}, "CreateBackup", nil
	case "verify":
		if *id == "" {
			return nil, "", errors.New("verify: -id is required")
		}
		return &backupv1.VerifyBackupRequest{BackupId: *id, RestoreTest: *restoreTest}, "VerifyBackup", nil
	case "restore":
		if *id == "" || *target == "" {
			return nil, "", errors.New("restore: -id and -target are required")
		}
		return &backupv1.RestoreBackupRequest{BackupId: *id, TargetPath: *target}, "RestoreBackup", nil
	case "list":
		return &backupv1.ListBackupsRequest{}, "ListBackups", nil
	default:
		return nil, "", fmt.Errorf("unknown command %q", cmd)
	}
}

func call(ctx context.Context, c backupv1.BackupServiceClient, method string, req proto.Message) (proto.Message, error) {
	switch r := req.(type) {
	case *backupv1.CreateBackupRequest:
		return c.CreateBackup(ctx, r)
	case *backupv1.VerifyBackupRequest:
		return c.VerifyBackup(ctx, r)
	case *backupv1.RestoreBackupRequest:
		return c.RestoreBackup(ctx, r)
	case *backupv1.ListBackupsRequest:
		return c.ListBackups(ctx, r)
	}
	return nil, fmt.Errorf("unsupported request for %s", method)
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
