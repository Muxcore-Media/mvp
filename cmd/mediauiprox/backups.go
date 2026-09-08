package main

import (
	"context"
	"net/http"
	"strings"
	"time"

	backupv1 "github.com/Muxcore-Media/backup-local/muxcore/backup/v1"
)

func publicBackup(info *backupv1.BackupInfo) map[string]any {
	if info == nil {
		return map[string]any{}
	}
	modules := info.GetModuleIds()
	if modules == nil {
		modules = []string{}
	}
	out := map[string]any{
		"id":         info.GetId(),
		"size_bytes": info.GetSizeBytes(),
		"modules":    modules,
		"checksum":   info.GetChecksumSha256(),
	}
	if ts := info.GetTimestampUnix(); ts > 0 {
		out["created_at"] = time.Unix(ts, 0).UTC().Format(time.RFC3339)
	}
	return out
}

func (s *server) handleListBackups(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "backups.forbidden"})
		return
	}
	if s.backup == nil {
		writeJSON(w, map[string]any{"available": false, "restore_dir": s.backupRestoreDir, "backups": []any{}})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	resp, err := s.backup.ListBackups(ctx, &backupv1.ListBackupsRequest{})
	if err != nil {
		writeJSON(w, map[string]any{"available": false, "restore_dir": s.backupRestoreDir, "backups": []any{}, "error": err.Error()})
		return
	}
	out := make([]map[string]any, 0, len(resp.GetBackups()))
	for _, row := range resp.GetBackups() {
		pub := publicBackup(row)
		if pub["id"] == nil || pub["id"] == "" {
			continue
		}
		out = append(out, pub)
	}
	writeJSON(w, map[string]any{"available": true, "restore_dir": s.backupRestoreDir, "backups": out})
}

func (s *server) handleCreateBackup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "backups.forbidden"})
		return
	}
	if s.backup == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{
			"error": "Backups are unavailable — start backup-local.",
			"code":  "backups.unavailable",
		})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	resp, err := s.backup.CreateBackup(ctx, &backupv1.CreateBackupRequest{})
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "backups.create_failed"})
		return
	}
	writeJSON(w, map[string]any{"ok": true, "backup": publicBackup(resp.GetBackup())})
}

func (s *server) handleDeleteBackup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "backups.forbidden"})
		return
	}
	if s.backup == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{
			"error": "Backups are unavailable — start backup-local.",
			"code":  "backups.unavailable",
		})
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "backup id required", "code": "backups.id_required"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	_, err := s.backup.DeleteBackup(ctx, &backupv1.DeleteBackupRequest{BackupId: id})
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "backups.delete_failed"})
		return
	}
	writeJSON(w, map[string]any{"removed": true, "id": id})
}

func (s *server) handleRestoreBackup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIMethodNotAllowed(w)
		return
	}
	if !s.sessionHasPrivilegedRole(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "admin or manager role required", "code": "backups.forbidden"})
		return
	}
	if s.backup == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{
			"error": "Backups are unavailable — start backup-local.",
			"code":  "backups.unavailable",
		})
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "backup id required", "code": "backups.id_required"})
		return
	}
	target := strings.TrimSpace(s.backupRestoreDir)
	if target == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{
			"error": "BACKUP_RESTORE_DIR is not configured — restore is disabled until the household restore directory is set.",
			"code":  "backups.restore_dir_required",
		})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	resp, err := s.backup.RestoreBackup(ctx, &backupv1.RestoreBackupRequest{BackupId: id, TargetPath: target})
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "backups.restore_failed"})
		return
	}
	writeJSON(w, map[string]any{
		"ok":             true,
		"id":             id,
		"files_restored": resp.GetFilesRestored(),
		"restore_dir":    target,
	})
}
