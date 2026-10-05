package main

import (
	"bytes"
	"context"
	"net"
	"strings"
	"testing"

	backupv1 "github.com/Muxcore-Media/backup-local/muxcore/backup/v1"
	"google.golang.org/grpc"
)

func TestParseCommand(t *testing.T) {
	cases := []struct {
		cmd    string
		args   []string
		method string
		ok     bool
	}{
		{"create", nil, "CreateBackup", true},
		{"create", []string{"-source", "/a", "-source", "/b"}, "CreateBackup", true},
		{"verify", []string{"-id", "backup_1", "-restore-test"}, "VerifyBackup", true},
		{"verify", nil, "", false},
		{"restore", []string{"-id", "backup_1"}, "", false},
		{"restore", []string{"-id", "backup_1", "-target", "/t"}, "RestoreBackup", true},
		{"list", nil, "ListBackups", true},
		{"list", []string{"extra"}, "", false},
		{"verify", []string{"-source", "/a"}, "", false},
		{"nope", nil, "", false},
	}
	for _, tc := range cases {
		var stderr bytes.Buffer
		req, method, err := parseCommand(tc.cmd, tc.args, &stderr)
		if (err == nil) != tc.ok {
			t.Fatalf("%s %v: err=%v want ok=%v", tc.cmd, tc.args, err, tc.ok)
		}
		if tc.ok && (method != tc.method || req == nil) {
			t.Fatalf("%s %v: method=%q req=%v", tc.cmd, tc.args, method, req)
		}
	}
	req, _, _ := parseCommand("create", []string{"-source", "/a", "-source", "/b"}, &bytes.Buffer{})
	if got := req.(*backupv1.CreateBackupRequest).GetSourcePaths(); len(got) != 2 || got[1] != "/b" {
		t.Fatalf("sources=%v", got)
	}
}

type fakeBackup struct {
	backupv1.UnimplementedBackupServiceServer
	gotVerify *backupv1.VerifyBackupRequest
}

func (f *fakeBackup) CreateBackup(_ context.Context, _ *backupv1.CreateBackupRequest) (*backupv1.CreateBackupResponse, error) {
	return &backupv1.CreateBackupResponse{Backup: &backupv1.BackupInfo{Id: "backup_42", SizeBytes: 7}}, nil
}

func (f *fakeBackup) VerifyBackup(_ context.Context, r *backupv1.VerifyBackupRequest) (*backupv1.VerifyBackupResponse, error) {
	f.gotVerify = r
	return &backupv1.VerifyBackupResponse{Valid: true, RestoreTestOk: r.GetRestoreTest(), FilesInArchive: 3}, nil
}

func TestRunAgainstServer(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	fake := &fakeBackup{}
	backupv1.RegisterBackupServiceServer(srv, fake)
	go func() { _ = srv.Serve(lis) }()
	defer srv.Stop()

	var out, errb bytes.Buffer
	if rc := run([]string{"-addr", lis.Addr().String(), "create"}, &out, &errb); rc != 0 {
		t.Fatalf("create rc=%d stderr=%s", rc, errb.String())
	}
	if !strings.Contains(out.String(), `"id":"backup_42"`) {
		t.Fatalf("create output %q", out.String())
	}
	out.Reset()
	if rc := run([]string{"-addr", lis.Addr().String(), "verify", "-id", "backup_42", "-restore-test"}, &out, &errb); rc != 0 {
		t.Fatalf("verify rc=%d stderr=%s", rc, errb.String())
	}
	if !fake.gotVerify.GetRestoreTest() || !strings.Contains(out.String(), `"restoreTestOk":true`) {
		t.Fatalf("verify output %q", out.String())
	}
	if rc := run([]string{"-addr", lis.Addr().String(), "list"}, &out, &errb); rc != 1 {
		t.Fatalf("unimplemented list rc=%d, want 1", rc)
	}
	if rc := run([]string{"bogus"}, &out, &errb); rc != 2 {
		t.Fatalf("bogus rc=%d, want 2", rc)
	}
}
