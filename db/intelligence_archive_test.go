package db

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
)

func TestIntelligenceArchiveRoundTrip(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := d.EnsureLLMRecordsTable(); err != nil {
		t.Fatal(err)
	}
	if err := d.EnsureLLMUsageTable(); err != nil {
		t.Fatal(err)
	}
	task, err := d.CreateTask("intelligence archive", "fixture", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	other, err := d.CreateTask("intelligence shared asset", "fixture", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	var ownedAsset, sharedAsset int64
	defer func() {
		_, _ = d.Exec(`DELETE FROM task_archives WHERE task_id=$1`, task.ID)
		_ = d.DeleteTask(task.ID)
		_ = d.DeleteTask(other.ID)
		_, _ = d.Exec(`DELETE FROM assets WHERE id=ANY($1::bigint[])`, []int64{ownedAsset, sharedAsset})
	}()
	if err := d.SetPaused(task.ID, true); err != nil {
		t.Fatal(err)
	}
	ownedAsset, err = d.Assets().UpsertRootDomain(UpsertRootDomainReq{Domain: fmt.Sprintf("intelligence-owned-%d.example.invalid", task.ID), TaskID: task.ID})
	if err != nil {
		t.Fatal(err)
	}
	sharedAsset, err = d.Assets().UpsertRootDomain(UpsertRootDomainReq{Domain: fmt.Sprintf("intelligence-shared-%d.example.invalid", task.ID), TaskID: task.ID})
	if err != nil {
		t.Fatal(err)
	}
	intent, err := d.Exploration(task.ExplorationID).AddIntent(map[string]any{"summary": "read fixture"}, 5, nil, "qa")
	if err != nil {
		t.Fatal(err)
	}
	in := testIntelligenceInput()
	in.AssetIDs = []int64{ownedAsset, sharedAsset}
	item, err := d.RecordIntelligence(t.Context(), task.ID, intent, "qa", in)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.UpdateIntelligenceStatus(t.Context(), task.ID, item.ID, "verified"); err != nil {
		t.Fatal(err)
	}
	analysis := CredentialAnalysis{State: "ready", Method: "rules", Accounts: []CredentialAccount{{Username: "qa@fixture.invalid", Password: "fixture.invalid"}}, TrafficState: "ready", Traffic: []CredentialTraffic{{ID: "fixture.invalid", URL: "https://fixture.invalid/login", Method: "POST", Status: 200, Side: "request", Excerpt: "fixture.invalid account excerpt", Accounts: []int{0}}}}
	rawAnalysis, err := json.Marshal(analysis)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`UPDATE task_intelligence SET credential_analysis=$2::jsonb WHERE id=$1`, item.ID, string(rawAnalysis)); err != nil {
		t.Fatal(err)
	}
	in.AssetIDs = []int64{sharedAsset}
	if _, err := d.RecordIntelligence(t.Context(), other.ID, 0, "qa", in); err != nil {
		t.Fatal(err)
	}
	archive, err := d.QueueTaskArchive(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{ArchiveQueued, Archiving} {
		if _, err := d.Exec(`UPDATE task_archives SET state=$2 WHERE id=$1`, archive.ID, state); err != nil {
			t.Fatal(err)
		}
		if _, err := d.RecordIntelligence(t.Context(), task.ID, intent, "qa", in); !errors.Is(err, ErrTaskArchiveState) {
			t.Fatalf("archive state %s accepted intelligence: %v", state, err)
		}
		if err := d.UpdateIntelligenceStatus(t.Context(), task.ID, item.ID, "invalid"); !errors.Is(err, ErrTaskArchiveState) {
			t.Fatalf("archive state %s accepted status mutation: %v", state, err)
		}
	}
	snapshot, err := d.SnapshotTaskArchive(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.DataCounts["task_intelligence"] != 1 || snapshot.DataCounts["assets"] != 2 {
		t.Fatal("snapshot omitted intelligence or assets", snapshot.DataCounts)
	}
	for _, id := range snapshot.ExclusiveAssetIDs {
		if id == sharedAsset {
			t.Fatal("asset referenced by other intelligence marked exclusive")
		}
	}
	if err := d.CompleteTaskArchive(archive.ID, snapshot, "/tmp/intelligence-fixture.tar.zst", "fixture", 100, 50); err != nil {
		t.Fatal(err)
	}
	if _, err := d.GetIntelligence(t.Context(), item.ID); !errors.Is(err, ErrIntelligenceNotFound) {
		t.Fatalf("cold intelligence not purged: %v", err)
	}
	if _, err := d.RecordIntelligence(t.Context(), task.ID, 0, "qa", in); !errors.Is(err, ErrIntelligenceNotFound) {
		t.Fatalf("archived task accepted writes: %v", err)
	}
	if err := d.UpdateIntelligenceStatus(t.Context(), task.ID, item.ID, "invalid"); !errors.Is(err, ErrIntelligenceNotFound) {
		t.Fatalf("archived item mutation: %v", err)
	}
	var count int
	if err := d.QueryRow(`SELECT count(*) FROM assets WHERE id=$1`, sharedAsset).Scan(&count); err != nil || count != 1 {
		t.Fatalf("shared intelligence asset lost: %d %v", count, err)
	}
	if _, err := d.QueueTaskArchiveRestore(archive.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`UPDATE task_archives SET state=$2 WHERE id=$1`, archive.ID, Restoring); err != nil {
		t.Fatal(err)
	}
	if _, err := d.RestoreTaskArchive(archive.ID, snapshot, 0); err != nil {
		t.Fatal(err)
	}
	got, err := d.GetIntelligence(t.Context(), item.ID)
	if err != nil || got.Status != "verified" || got.Content != item.Content || got.Evidence != item.Evidence || got.NodeID == nil || *got.NodeID != intent || len(got.AssetIDs) != 2 {
		t.Fatalf("intelligence restore lost provenance/content/triage: %+v %v", got, err)
	}
	if !reflect.DeepEqual(got.Credentials, &analysis) {
		t.Fatal("archive lost credential accounts or traffic excerpt")
	}
	if err := d.CompleteTaskArchiveRestore(archive.ID); err != nil {
		t.Fatal(err)
	}
	in.Content = "new fixture after restore"
	newItem, err := d.RecordIntelligence(t.Context(), task.ID, intent, "qa", in)
	if err != nil || newItem.ID <= item.ID {
		t.Fatalf("restored sequence collision: %+v %v", newItem, err)
	}
}

func TestIntelligenceArchiveRowsWithoutCredentialAnalysis(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	task, err := d.CreateTask("legacy credential fixture.invalid", "fixture.invalid", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteTask(task.ID)
	item, err := d.RecordIntelligence(t.Context(), task.ID, 0, "qa", testIntelligenceInput())
	if err != nil {
		t.Fatal(err)
	}
	tx, err := d.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var raw []byte
	if err := tx.QueryRow(`SELECT json_agg(t) FROM task_intelligence t WHERE id=$1`, item.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var rows []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rows); err != nil {
		t.Fatal(err)
	}
	delete(rows[0], "credential_analysis")
	raw, err = json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`DELETE FROM task_intelligence WHERE id=$1`, item.ID); err != nil {
		t.Fatal(err)
	}
	if err := insertArchiveRows(tx, "task_intelligence", raw); err != nil {
		t.Fatal(err)
	}
	var missing bool
	var content string
	if err := tx.QueryRow(`SELECT credential_analysis IS NULL,content FROM task_intelligence WHERE id=$1`, item.ID).Scan(&missing, &content); err != nil {
		t.Fatal(err)
	}
	if !missing || content != item.Content {
		t.Fatal("legacy archive fabricated analysis or lost content")
	}
}

func TestIntelligenceProtectsOtherTaskAssetsOnDelete(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	owner, err := d.CreateTask("intelligence asset owner", "fixture", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	other, err := d.CreateTask("intelligence asset reader", "fixture", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	var assetID int64
	defer func() {
		_ = d.DeleteTask(owner.ID)
		_ = d.DeleteTask(other.ID)
		_, _ = d.Exec(`DELETE FROM assets WHERE id=$1`, assetID)
	}()
	host := fmt.Sprintf("intelligence-protected-%d.example.invalid", owner.ID)
	assetID, err = d.Assets().UpsertRootDomain(UpsertRootDomainReq{Domain: host, TaskID: owner.ID})
	if err != nil {
		t.Fatal(err)
	}
	in := testIntelligenceInput()
	in.AssetIDs = []int64{assetID}
	if _, err := d.RecordIntelligence(t.Context(), other.ID, 0, "qa", in); err != nil {
		t.Fatal(err)
	}
	result, err := d.DeleteTaskCascadePrepared(owner.ID, true, false, false, func(preparation TaskDeletePreparation) error {
		for _, candidate := range preparation.TrafficHosts {
			if candidate == host {
				t.Fatal("other task intelligence host marked deletable")
			}
		}
		return nil
	})
	if err != nil || result.AssetsDeleted != 0 {
		t.Fatalf("shared asset deletion: %+v %v", result, err)
	}
	var count int
	if err := d.QueryRow(`SELECT count(*) FROM assets WHERE id=$1`, assetID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("shared asset lost: %d %v", count, err)
	}
	page, err := d.ListIntelligence(t.Context(), IntelligenceFilter{TaskID: other.ID})
	if err != nil || page.Total != 1 || len(page.Items[0].AssetIDs) != 1 {
		t.Fatalf("other task intelligence lost: %+v %v", page, err)
	}
}
