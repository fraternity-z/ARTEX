package db

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"
)

const SettingCredentialAgent = "credential_agent_enabled"
const SettingCredentialTraffic = "credential_traffic_binding"
const CredentialAgentKey = "credential_processor"

type CredentialAccount struct {
	Label    string `json:"label,omitempty"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	Token    string `json:"token,omitempty"`
	Cookie   string `json:"cookie,omitempty"`
	Endpoint string `json:"endpoint,omitempty"`
	Protocol string `json:"protocol,omitempty"`
}
type CredentialTraffic struct {
	ID       string `json:"id"`
	URL      string `json:"url"`
	Method   string `json:"method"`
	Status   int    `json:"status"`
	Side     string `json:"side"`
	Excerpt  string `json:"excerpt"`
	Accounts []int  `json:"accounts"`
}
type CredentialAnalysis struct {
	ForceExtract bool                `json:"force_extract,omitempty"`
	TrafficNote  string              `json:"traffic_note,omitempty"`
	State        string              `json:"state"`
	Lease        string              `json:"lease,omitempty"`
	LeaseUntil   time.Time           `json:"lease_until,omitempty"`
	SourceHash   string              `json:"source_hash,omitempty"`
	Method       string              `json:"method,omitempty"`
	Accounts     []CredentialAccount `json:"accounts"`
	Traffic      []CredentialTraffic `json:"traffic"`
	TrafficState string              `json:"traffic_state"`
	Note         string              `json:"note,omitempty"`
	Error        string              `json:"error,omitempty"`
	Model        string              `json:"model,omitempty"`
	InputTokens  int                 `json:"input_tokens"`
	OutputTokens int                 `json:"output_tokens"`
	ProcessedAt  *time.Time          `json:"processed_at,omitempty"`
}

func CredentialSourceHash(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

func (d *DB) SetCredentialAgentSettings(ctx context.Context, enabled, binding *bool) error {
	return d.SetIntelligenceSettings(ctx, nil, enabled, binding)
}

// Serialize all three switches so turning a parent off clears its children.
func (d *DB) SetIntelligenceSettings(ctx context.Context, recording, enabled, binding *bool) error {
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(7337741006)"); err != nil {
		return err
	}
	values := map[string]bool{}
	for _, key := range []string{SettingTaskIntelligence, SettingCredentialAgent, SettingCredentialTraffic} {
		var value string
		err = tx.QueryRowContext(ctx, "SELECT value FROM settings WHERE key=$1", key).Scan(&value)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		values[key] = value == "true" || value == "1"
	}
	if recording != nil {
		values[SettingTaskIntelligence] = *recording
	}
	if enabled != nil {
		values[SettingCredentialAgent] = *enabled
	}
	if binding != nil {
		values[SettingCredentialTraffic] = *binding
	}
	if !values[SettingTaskIntelligence] {
		if enabled != nil && *enabled || binding != nil && *binding {
			return fmt.Errorf("%w: 请先开启 Agent 自动记录", ErrIntelligenceInvalid)
		}
		values[SettingCredentialAgent] = false
		values[SettingCredentialTraffic] = false
	}
	if !values[SettingCredentialAgent] {
		if binding != nil && *binding {
			return fmt.Errorf("%w: 请先开启 Agent 开关，再启用绑定流量", ErrIntelligenceInvalid)
		}
		values[SettingCredentialTraffic] = false
	}
	for key, val := range values {
		if _, err = tx.ExecContext(ctx, "INSERT INTO settings(key,value) VALUES($1,$2) ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value,updated_at=now()", key, strconv.FormatBool(val)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// No task lock is held during model I/O; expired leases recover interrupted jobs.
func (d *DB) ClaimCredentialJob(ctx context.Context, bind bool, taskID int64) (*Intelligence, error) {
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var id, claimedTaskID int64
	err = tx.QueryRowContext(ctx, `SELECT i.id,i.task_id FROM task_intelligence i JOIN tasks t ON t.id=i.task_id
 WHERE i.kind='credential' AND EXISTS(SELECT 1 FROM settings WHERE key='credential_agent_enabled' AND value IN ('true','1')) AND EXISTS(SELECT 1 FROM settings WHERE key='task_intelligence_enabled' AND value IN ('true','1')) AND ($2::bigint=0 OR i.task_id=$2) AND t.deleted_at IS NULL AND t.archived_at IS NULL
 AND NOT EXISTS(SELECT 1 FROM task_archives a WHERE a.task_id=t.id AND a.state IN ('archive_queued','archiving','restore_queued','restoring','delete_queued','deleting'))
 AND (i.credential_analysis IS NULL OR i.credential_analysis->>'state'='queued'
 OR (i.credential_analysis->>'state'='processing' AND (i.credential_analysis->>'lease_until')::timestamptz < now())
 OR ($1 AND i.credential_analysis->>'state'='ready' AND i.credential_analysis->>'traffic_state'='off'))
 ORDER BY i.id FOR UPDATE OF t SKIP LOCKED LIMIT 1`, bind, taskID).Scan(&id, &claimedTaskID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err = LockTaskEvidenceTx(tx, claimedTaskID); err != nil {
		return nil, err
	}
	token := make([]byte, 16)
	if _, err = rand.Read(token); err != nil {
		return nil, err
	}
	patch, _ := json.Marshal(map[string]any{"state": "processing", "lease": hex.EncodeToString(token), "lease_until": time.Now().UTC().Add(3 * time.Minute)})
	if _, err = tx.ExecContext(ctx, "UPDATE task_intelligence SET credential_analysis=COALESCE(credential_analysis,'{}'::jsonb)||$2::jsonb WHERE id=$1", id, string(patch)); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return d.GetIntelligence(ctx, id)
}

func (d *DB) FinishCredentialJob(ctx context.Context, item *Intelligence, result CredentialAnalysis) error {
	if item == nil || item.Credentials == nil || item.Credentials.Lease == "" {
		return fmt.Errorf("%w: 缺少整理租约", ErrIntelligenceInvalid)
	}
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = LockTaskEvidenceTx(tx, item.TaskID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(7337741006)"); err != nil {
		return err
	}
	var enabled, binding bool
	if err = tx.QueryRowContext(ctx, `SELECT (EXISTS(SELECT 1 FROM settings WHERE key=$1 AND value IN ('true','1')) AND EXISTS(SELECT 1 FROM settings WHERE key='task_intelligence_enabled' AND value IN ('true','1'))),EXISTS(SELECT 1 FROM settings WHERE key=$2 AND value IN ('true','1'))`, SettingCredentialAgent, SettingCredentialTraffic).Scan(&enabled, &binding); err != nil {
		return err
	}
	if !enabled {
		_, err = tx.ExecContext(ctx, `UPDATE task_intelligence SET credential_analysis=credential_analysis||'{"state":"queued"}'::jsonb WHERE id=$1 AND credential_analysis->>'lease'=$2`, item.ID, item.Credentials.Lease)
		if err != nil {
			return err
		}
		return tx.Commit()
	}
	if !binding {
		result.Traffic = item.Credentials.Traffic
		result.TrafficState = "off"
		result.TrafficNote = ""
	}
	result.Lease = ""
	result.LeaseUntil = time.Time{}
	now := time.Now().UTC()
	result.ProcessedAt = &now
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE task_intelligence i SET credential_analysis=$3::jsonb
 WHERE i.id=$1 AND i.credential_analysis->>'lease'=$2
 AND EXISTS(SELECT 1 FROM tasks t WHERE t.id=i.task_id AND t.deleted_at IS NULL AND t.archived_at IS NULL)
 AND NOT EXISTS(SELECT 1 FROM task_archives a WHERE a.task_id=i.task_id AND a.state IN ('archive_queued','archiving','restore_queued','restoring','delete_queued','deleting'))`, item.ID, item.Credentials.Lease, string(raw))
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (d *DB) RetryCredentialJob(ctx context.Context, taskID, id int64) error {
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = LockTaskEvidenceTx(tx, taskID); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE task_intelligence i SET credential_analysis=COALESCE(credential_analysis,'{}'::jsonb)||'{"state":"queued","error":"","force_extract":true}'::jsonb
 WHERE id=$1 AND task_id=$2 AND kind='credential'
 AND COALESCE(credential_analysis->>'state','')<>'processing'
 AND EXISTS(SELECT 1 FROM tasks t WHERE t.id=i.task_id AND t.deleted_at IS NULL AND t.archived_at IS NULL)`, id, taskID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrIntelligenceNotFound
	}
	return tx.Commit()
}
