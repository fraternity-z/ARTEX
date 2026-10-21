package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

// A bounded SQL driver keeps lease/settings checks independent of a live server.
type credentialSQL struct {
	query     func(string, []driver.NamedValue) (driver.Rows, error)
	exec      func(string, []driver.NamedValue) (driver.Result, error)
	committed bool
}

func (c *credentialSQL) Connect(context.Context) (driver.Conn, error) { return c, nil }
func (c *credentialSQL) Driver() driver.Driver                        { return c }
func (c *credentialSQL) Open(string) (driver.Conn, error)             { return c, nil }
func (c *credentialSQL) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (c *credentialSQL) Close() error                                                 { return nil }
func (c *credentialSQL) Begin() (driver.Tx, error)                                    { return c, nil }
func (c *credentialSQL) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) { return c, nil }
func (c *credentialSQL) Commit() error                                                { c.committed = true; return nil }
func (c *credentialSQL) Rollback() error                                              { return nil }
func (c *credentialSQL) QueryContext(_ context.Context, q string, a []driver.NamedValue) (driver.Rows, error) {
	return c.query(q, a)
}
func (c *credentialSQL) ExecContext(_ context.Context, q string, a []driver.NamedValue) (driver.Result, error) {
	if c.exec == nil {
		return driver.RowsAffected(1), nil
	}
	return c.exec(q, a)
}

type credentialRows struct{ values [][]driver.Value }

func (r *credentialRows) Columns() []string {
	if len(r.values) == 0 {
		return []string{"empty"}
	}
	out := make([]string, len(r.values[0]))
	for i := range out {
		out[i] = "column"
	}
	return out
}
func (r *credentialRows) Close() error { return nil }
func (r *credentialRows) Next(dest []driver.Value) error {
	if len(r.values) == 0 {
		return io.EOF
	}
	copy(dest, r.values[0])
	r.values = r.values[1:]
	return nil
}
func credentialRow(v ...driver.Value) driver.Rows {
	return &credentialRows{values: [][]driver.Value{v}}
}
func credentialTestDB(t *testing.T, c *credentialSQL) *DB {
	t.Helper()
	d := &DB{sql.OpenDB(c)}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestCredentialSettingsParentsAndAtomicFailure(t *testing.T) {
	for _, test := range []struct {
		record, on, bind                bool
		recordPatch, onPatch, bindPatch *bool
		invalid                         bool
	}{
		{record: true, on: true, bind: true, recordPatch: new(false)},
		{record: true, on: true, bind: true, onPatch: new(false)},
		{onPatch: new(true), invalid: true},
		{record: true, bindPatch: new(true), invalid: true},
		{recordPatch: new(true), onPatch: new(true), bindPatch: new(true)},
	} {
		writes := map[string]bool{}
		initial := map[string]bool{SettingTaskIntelligence: test.record, SettingCredentialAgent: test.on, SettingCredentialTraffic: test.bind}
		c := &credentialSQL{}
		c.query = func(_ string, a []driver.NamedValue) (driver.Rows, error) {
			if initial[a[0].Value.(string)] {
				return credentialRow("true"), nil
			}
			return credentialRow("false"), nil
		}
		c.exec = func(q string, a []driver.NamedValue) (driver.Result, error) {
			if strings.HasPrefix(q, "INSERT") {
				writes[a[0].Value.(string)] = a[1].Value == "true"
			}
			return driver.RowsAffected(1), nil
		}
		err := credentialTestDB(t, c).SetIntelligenceSettings(t.Context(), test.recordPatch, test.onPatch, test.bindPatch)
		if test.invalid {
			if !errors.Is(err, ErrIntelligenceInvalid) || len(writes) > 0 || c.committed {
				t.Fatal("invalid child switch partially committed")
			}
			continue
		}
		if err != nil || !c.committed || len(writes) != 3 {
			t.Fatal("valid settings not atomic", err)
		}
		if !writes[SettingTaskIntelligence] && (writes[SettingCredentialAgent] || writes[SettingCredentialTraffic]) || !writes[SettingCredentialAgent] && writes[SettingCredentialTraffic] {
			t.Fatal("dependent switch remained on")
		}
	}
}

func TestCredentialFinishLeaseAndDisabledSettings(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		for _, binding := range []bool{false, true} {
			c := &credentialSQL{}
			c.query = func(q string, _ []driver.NamedValue) (driver.Rows, error) {
				if strings.Contains(q, "SELECT deleted_at") {
					return credentialRow(nil), nil
				}
				if strings.Contains(q, "SELECT state FROM task_archives") {
					return &credentialRows{}, nil
				}
				return credentialRow(enabled, binding), nil
			}
			wrote := false
			c.exec = func(q string, a []driver.NamedValue) (driver.Result, error) {
				if !strings.HasPrefix(q, "UPDATE") {
					return driver.RowsAffected(1), nil
				}
				wrote = true
				if !strings.Contains(q, "credential_analysis->>'lease'=$2") || a[1].Value != "owned-lease" {
					t.Fatal("finish ignored lease")
				}
				if !enabled {
					if len(a) != 2 || !strings.Contains(q, "queued") {
						t.Fatal("disabled job published result")
					}
				} else {
					var result CredentialAnalysis
					if json.Unmarshal([]byte(a[2].Value.(string)), &result) != nil || result.Lease != "" || result.ProcessedAt == nil {
						t.Fatal("lease not cleared")
					}
					if !binding && (len(result.Traffic) != 1 || result.Traffic[0].ID != "old" || result.TrafficState != "off") {
						t.Fatal("disabled binding published new flow")
					}
				}
				return driver.RowsAffected(0), nil // A stale lease never overwrites a newer job.
			}
			d := credentialTestDB(t, c)
			item := &Intelligence{ID: 1, TaskID: 2, Credentials: &CredentialAnalysis{Lease: "owned-lease", Traffic: []CredentialTraffic{{ID: "old"}}}}
			if err := d.FinishCredentialJob(t.Context(), item, CredentialAnalysis{State: "ready", Traffic: []CredentialTraffic{{ID: "new"}}}); err != nil || !wrote || !c.committed {
				t.Fatal("finish failed", err)
			}
		}
	}
	if err := (&DB{}).FinishCredentialJob(t.Context(), nil, CredentialAnalysis{}); !errors.Is(err, ErrIntelligenceInvalid) {
		t.Fatal("missing lease accepted")
	}
}

func TestCredentialMutationsRespectArchiveGate(t *testing.T) {
	for _, operation := range []string{"finish", "retry"} {
		c := &credentialSQL{query: func(q string, _ []driver.NamedValue) (driver.Rows, error) {
			if strings.Contains(q, "deleted_at") {
				return credentialRow(nil), nil
			}
			return credentialRow(ArchiveQueued), nil
		}}
		c.exec = func(string, []driver.NamedValue) (driver.Result, error) {
			t.Fatal("archived task mutated")
			return nil, nil
		}
		d := credentialTestDB(t, c)
		var err error
		if operation == "finish" {
			err = d.FinishCredentialJob(t.Context(), &Intelligence{ID: 1, TaskID: 2, Credentials: &CredentialAnalysis{Lease: "lease"}}, CredentialAnalysis{})
		} else {
			err = d.RetryCredentialJob(t.Context(), 2, 1)
		}
		if !errors.Is(err, ErrTaskArchiveState) || c.committed {
			t.Fatal("archive gate bypassed", err)
		}
	}
}

func TestCredentialRetryOwnershipAndProcessingGate(t *testing.T) {
	c := &credentialSQL{query: func(q string, _ []driver.NamedValue) (driver.Rows, error) {
		if strings.Contains(q, "deleted_at") {
			return credentialRow(nil), nil
		}
		return &credentialRows{}, nil
	}}
	c.exec = func(q string, a []driver.NamedValue) (driver.Result, error) {
		if !strings.Contains(q, "task_id=$2") || !strings.Contains(q, "kind='credential'") || !strings.Contains(q, "<>'processing'") || !strings.Contains(q, "force_extract") || a[0].Value != int64(9) || a[1].Value != int64(2) {
			t.Fatal("retry lost ownership or processing gate")
		}
		return driver.RowsAffected(0), nil
	}
	if err := credentialTestDB(t, c).RetryCredentialJob(t.Context(), 2, 9); !errors.Is(err, ErrIntelligenceNotFound) || c.committed {
		t.Fatal("unowned retry succeeded", err)
	}
}

func TestCredentialSourceHashAndOldArchive(t *testing.T) {
	if CredentialSourceHash("fixture") == CredentialSourceHash("different") || len(CredentialSourceHash("fixture")) != 64 {
		t.Fatal("invalid source identity")
	}
	// Missing nullable analysis in an older archive decodes without a fabricated job.
	var item Intelligence
	if err := json.Unmarshal([]byte(`{"kind":"credential","content":"fixture"}`), &item); err != nil || item.Credentials != nil {
		t.Fatal("old archive incompatible", err)
	}
}

func TestCredentialClaimLeaseAndEmptyQueue(t *testing.T) {
	for _, empty := range []bool{false, true} {
		c := &credentialSQL{}
		var patch []byte
		c.query = func(q string, a []driver.NamedValue) (driver.Rows, error) {
			if strings.Contains(q, "ORDER BY i.id") {
				if !strings.Contains(q, "FOR UPDATE OF t SKIP LOCKED") || !strings.Contains(q, "lease_until") || !strings.Contains(q, "task_archives") {
					t.Fatal("claim lost durable lease/archive lock")
				}
				if empty {
					return &credentialRows{}, nil
				}
				return credentialRow(int64(7), int64(2)), nil
			}
			if strings.Contains(q, "SELECT deleted_at") {
				return credentialRow(nil), nil
			}
			if strings.Contains(q, "SELECT state FROM task_archives") {
				return &credentialRows{}, nil
			}
			return credentialRow(int64(7), int64(2), nil, "credential", "fixture", "fixture-token", "fixture", "observed", "unverified", []byte("[]"), "qa", time.Now(), time.Now(), patch, "plan"), nil
		}
		c.exec = func(_ string, a []driver.NamedValue) (driver.Result, error) {
			patch = []byte(a[1].Value.(string))
			return driver.RowsAffected(1), nil
		}
		item, err := credentialTestDB(t, c).ClaimCredentialJob(t.Context(), false, 2)
		if err != nil {
			t.Fatal(err)
		}
		if empty {
			if item != nil || c.committed || patch != nil {
				t.Fatal("empty queue mutated state")
			}
			continue
		}
		if item == nil || item.Credentials == nil || item.Credentials.State != "processing" || len(item.Credentials.Lease) != 32 || time.Until(item.Credentials.LeaseUntil) < 2*time.Minute || !c.committed {
			t.Fatal("claim missing lease")
		}
	}
}

func TestCredentialNullableAnalysisScan(t *testing.T) {
	for _, raw := range [][]byte{nil, []byte("null"), []byte(`{"state":"ready","accounts":[],"traffic":[],"traffic_state":"off"}`)} {
		c := &credentialSQL{query: func(string, []driver.NamedValue) (driver.Rows, error) {
			return credentialRow(int64(1), int64(2), nil, "credential", "fixture", "fixture", "fixture", "observed", "unverified", []byte("[]"), "qa", time.Now(), time.Now(), raw, ""), nil
		}}
		item, err := credentialTestDB(t, c).GetIntelligence(t.Context(), 1)
		if err != nil {
			t.Fatal("nullable archive analysis did not scan", err)
		}
		if len(raw) == 0 && item.Credentials != nil {
			t.Fatal("legacy archive fabricated analysis")
		}
	}
}

func TestCredentialAssetScopeIsExclusiveAndLive(t *testing.T) {
	d := &DB{}
	if _, err := d.ListIntelligence(t.Context(), IntelligenceFilter{TaskID: 1, AssetID: 2}); !errors.Is(err, ErrIntelligenceInvalid) {
		t.Fatal("mixed scope accepted")
	}
	c := &credentialSQL{query: func(q string, a []driver.NamedValue) (driver.Rows, error) {
		if strings.Contains(q, "FROM settings") {
			return credentialRow("true"), nil
		}
		if !strings.Contains(q, "i.asset_ids @> jsonb_build_array($1::bigint)") || !strings.Contains(q, "t.archived_at IS NULL") || !strings.Contains(q, "t.deleted_at IS NULL") || a[0].Value != int64(5) {
			t.Fatal("asset scope is not anchored to live associated records")
		}
		if strings.Contains(q, "GROUP BY kind") {
			return credentialRow("credential", int64(1)), nil
		}
		if strings.HasPrefix(q, "SELECT count(*)") {
			return credentialRow(int64(1)), nil
		}
		return credentialRow(int64(1), int64(2), nil, "credential", "fixture", "fixture", "fixture", "observed", "unverified", []byte("[5]"), "qa", time.Now(), time.Now(), nil, ""), nil
	}}
	page, err := credentialTestDB(t, c).ListIntelligence(t.Context(), IntelligenceFilter{AssetID: 5})
	if err != nil || page.Total != 1 || len(page.Items) != 1 || page.Items[0].Inherited || !page.CredentialAgentEnabled || !page.CredentialTrafficBinding {
		t.Fatal("asset DTO or scope failed", err)
	}
}
