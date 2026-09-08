package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	backupv1 "github.com/Muxcore-Media/backup-local/muxcore/backup/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type fixtureBackup struct {
	backupv1.UnimplementedBackupServiceServer
	listed   []*backupv1.BackupInfo
	created  *backupv1.BackupInfo
	deleted  string
	restored string
	target   string
}

func (f *fixtureBackup) ListBackups(_ context.Context, _ *backupv1.ListBackupsRequest) (*backupv1.ListBackupsResponse, error) {
	return &backupv1.ListBackupsResponse{Backups: f.listed}, nil
}

func (f *fixtureBackup) CreateBackup(_ context.Context, _ *backupv1.CreateBackupRequest) (*backupv1.CreateBackupResponse, error) {
	return &backupv1.CreateBackupResponse{Backup: f.created}, nil
}

func (f *fixtureBackup) DeleteBackup(_ context.Context, req *backupv1.DeleteBackupRequest) (*backupv1.DeleteBackupResponse, error) {
	f.deleted = req.GetBackupId()
	return &backupv1.DeleteBackupResponse{Status: "ok"}, nil
}

func (f *fixtureBackup) RestoreBackup(_ context.Context, req *backupv1.RestoreBackupRequest) (*backupv1.RestoreBackupResponse, error) {
	f.restored = req.GetBackupId()
	f.target = req.GetTargetPath()
	return &backupv1.RestoreBackupResponse{Status: "ok", FilesRestored: 4}, nil
}

func privilegedBackupRequest(t *testing.T, method, path string) (*server, *http.Request, *fixtureBackup) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	fake := &fixtureBackup{
		listed: []*backupv1.BackupInfo{{
			Id:             "backup_1",
			TimestampUnix:  1_700_000_000,
			SizeBytes:      2048,
			ModuleIds:      []string{"auth-local"},
			ChecksumSha256: "abc",
		}},
		created: &backupv1.BackupInfo{Id: "backup_new", SizeBytes: 512, ModuleIds: []string{}},
	}
	srv := grpc.NewServer()
	backupv1.RegisterBackupServiceServer(srv, fake)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithRoles("admin", "admin", "", []string{"admin"})
	if err != nil {
		t.Fatal(err)
	}
	s := &server{
		backup:          backupv1.NewBackupServiceClient(conn),
		sessions:        sessions,
		backupRestoreDir: "/data/restore",
	}
	req := httptest.NewRequest(method, path, nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	return s, req, fake
}

func TestHandleListBackupsForbidden(t *testing.T) {
	s := &server{sessions: newSessionStore(time.Hour)}
	w := httptest.NewRecorder()
	s.handleListBackups(w, httptest.NewRequest(http.MethodGet, "/api/backups", nil))
	if w.Code != http.StatusForbidden {
		t.Fatalf("status %d", w.Code)
	}
}

func TestHandleListBackupsUnavailable(t *testing.T) {
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithRoles("admin", "admin", "", []string{"admin"})
	if err != nil {
		t.Fatal(err)
	}
	s := &server{sessions: sessions, backupRestoreDir: "/data/restore"}
	req := httptest.NewRequest(http.MethodGet, "/api/backups", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w := httptest.NewRecorder()
	s.handleListBackups(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var body struct {
		Available  bool  `json:"available"`
		RestoreDir string `json:"restore_dir"`
		Backups    []any `json:"backups"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Available || body.Backups == nil || body.RestoreDir != "/data/restore" {
		t.Fatalf("%#v", body)
	}
}

func TestHandleListBackupsLive(t *testing.T) {
	s, req, _ := privilegedBackupRequest(t, http.MethodGet, "/api/backups")
	w := httptest.NewRecorder()
	s.handleListBackups(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Available bool `json:"available"`
		Backups   []struct {
			ID        string `json:"id"`
			CreatedAt string `json:"created_at"`
			SizeBytes int64  `json:"size_bytes"`
		} `json:"backups"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Available || len(body.Backups) != 1 || body.Backups[0].ID != "backup_1" || body.Backups[0].CreatedAt == "" {
		t.Fatalf("%#v", body)
	}
}

func TestHandleCreateBackup(t *testing.T) {
	s, req, _ := privilegedBackupRequest(t, http.MethodPost, "/api/backups")
	w := httptest.NewRecorder()
	s.handleCreateBackup(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	var body struct {
		OK     bool `json:"ok"`
		Backup struct {
			ID string `json:"id"`
		} `json:"backup"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.OK || body.Backup.ID != "backup_new" {
		t.Fatalf("%#v", body)
	}
}

func TestHandleDeleteBackup(t *testing.T) {
	s, req, fake := privilegedBackupRequest(t, http.MethodDelete, "/api/backups/backup_1")
	req.SetPathValue("id", "backup_1")
	w := httptest.NewRecorder()
	s.handleDeleteBackup(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if fake.deleted != "backup_1" {
		t.Fatalf("deleted %q", fake.deleted)
	}
}

func TestHandleRestoreBackupUsesConfiguredDir(t *testing.T) {
	s, req, fake := privilegedBackupRequest(t, http.MethodPost, "/api/backups/backup_1/restore")
	req.SetPathValue("id", "backup_1")
	w := httptest.NewRecorder()
	s.handleRestoreBackup(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if fake.restored != "backup_1" || fake.target != "/data/restore" {
		t.Fatalf("restore %q target %q", fake.restored, fake.target)
	}
	var body struct {
		OK            bool   `json:"ok"`
		FilesRestored int64  `json:"files_restored"`
		RestoreDir    string `json:"restore_dir"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.OK || body.FilesRestored != 4 || body.RestoreDir != "/data/restore" {
		t.Fatalf("%#v", body)
	}
}

func TestHandleRestoreBackupRequiresDir(t *testing.T) {
	s, req, fake := privilegedBackupRequest(t, http.MethodPost, "/api/backups/backup_1/restore")
	s.backupRestoreDir = ""
	req.SetPathValue("id", "backup_1")
	w := httptest.NewRecorder()
	s.handleRestoreBackup(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d", w.Code)
	}
	if fake.restored != "" {
		t.Fatalf("must not restore without configured dir")
	}
}

func TestHandleCreateBackupUnavailable(t *testing.T) {
	sessions := newSessionStore(time.Hour)
	tok, err := sessions.CreateWithRoles("admin", "admin", "", []string{"admin"})
	if err != nil {
		t.Fatal(err)
	}
	s := &server{sessions: sessions}
	req := httptest.NewRequest(http.MethodPost, "/api/backups", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w := httptest.NewRecorder()
	s.handleCreateBackup(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d", w.Code)
	}
}
