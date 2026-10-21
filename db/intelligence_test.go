package db

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func testIntelligenceInput() IntelligenceInput {
	return IntelligenceInput{Kind: "credential", Title: "测试数据库账号", Content: `{"username":"qa","password":"local-fixture-secret"}`, Evidence: "本机测试文件 /tmp/fixture", Confidence: "observed"}
}

func TestIntelligenceValidation(t *testing.T) {
	for name, change := range map[string]func(*IntelligenceInput){
		"kind":                  func(in *IntelligenceInput) { in.Kind = "other" },
		"title":                 func(in *IntelligenceInput) { in.Title = "  " },
		"content":               func(in *IntelligenceInput) { in.Content = "" },
		"evidence":              func(in *IntelligenceInput) { in.Evidence = "" },
		"status":                func(in *IntelligenceInput) { in.Status = "confirmed" },
		"confidence":            func(in *IntelligenceInput) { in.Confidence = "unknown" },
		"inferred verification": func(in *IntelligenceInput) { in.Confidence, in.Status = "inferred", "verified" },
		"asset":                 func(in *IntelligenceInput) { in.AssetIDs = []int64{-1} },
		"long title":            func(in *IntelligenceInput) { in.Title = strings.Repeat("情", 201) },
		"long content":          func(in *IntelligenceInput) { in.Content = strings.Repeat("x", 32769) },
		"long evidence":         func(in *IntelligenceInput) { in.Evidence = strings.Repeat("x", 8193) },
		"many assets":           func(in *IntelligenceInput) { in.AssetIDs = make([]int64, 101) },
	} {
		t.Run(name, func(t *testing.T) {
			in := testIntelligenceInput()
			change(&in)
			if _, err := NormalizeIntelligence(in); !errors.Is(err, ErrIntelligenceInvalid) {
				t.Fatalf("accepted invalid input: %v", err)
			}
		})
	}
	for kind, defaultStatus := range map[string]string{"credential": "unverified", "lead": "pending", "sensitive_data": "suspected"} {
		in := testIntelligenceInput()
		in.Kind, in.AssetIDs, in.Title = kind, []int64{2, 1, 2}, "  正常标题  "
		got, err := NormalizeIntelligence(in)
		if err != nil || got.Status != defaultStatus || got.Title != "正常标题" || !reflect.DeepEqual(got.AssetIDs, []int64{1, 2}) {
			t.Fatalf("normalization failed: %+v %v", got, err)
		}
	}
	for kind, statuses := range map[string][]string{
		"credential":     {"unverified", "verified", "invalid"},
		"lead":           {"pending", "in_progress", "dismissed", "converted"},
		"sensitive_data": {"suspected", "confirmed", "dismissed"},
	} {
		for _, status := range statuses {
			if !ValidIntelligenceStatus(kind, status) {
				t.Fatalf("rejected %s/%s", kind, status)
			}
		}
	}
}

func TestIntelligenceFingerprintCanonicalJSON(t *testing.T) {
	first := IntelligenceInput{Content: `{"username":"qa","password":"fixture"}`, AssetIDs: []int64{2, 1, 2}}
	second := IntelligenceInput{Content: `{ "password":"fixture", "username":"qa" }`, AssetIDs: []int64{1, 2}}
	if intelligenceFingerprint(first) != intelligenceFingerprint(second) {
		t.Fatal("equivalent JSON and reordered assets must deduplicate")
	}
	second.Content = "different"
	if intelligenceFingerprint(first) == intelligenceFingerprint(second) {
		t.Fatal("different content collided")
	}
	first.Content, second.Content = `{"id":9007199254740992}`, `{"id":9007199254740993}`
	if intelligenceFingerprint(first) == intelligenceFingerprint(second) {
		t.Fatal("distinct large JSON numbers collided")
	}
}

func TestIntelligenceProvenanceDedupAndFiltering(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	create := func(name string) *Task {
		task, err := d.CreateTask(name, "local fixture", nil, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		return task
	}
	first, second, third := create("intelligence source"), create("intelligence consumer"), create("intelligence transitive")
	defer d.DeleteTask(first.ID)
	defer d.DeleteTask(second.ID)
	defer d.DeleteTask(third.ID)
	store := d.Exploration(first.ExplorationID)
	intent, err := store.AddIntent(map[string]any{"summary": "read fixture"}, 5, nil, "qa")
	if err != nil {
		t.Fatal(err)
	}
	in := testIntelligenceInput()
	item, err := d.RecordIntelligence(t.Context(), first.ID, intent, "worker-qa", in)
	if err != nil {
		t.Fatal(err)
	}
	in.Content, in.Title = `{ "password":"local-fixture-secret", "username":"qa" }`, "不同标题"
	again, err := d.RecordIntelligence(t.Context(), first.ID, intent, "worker-qa", in)
	if err != nil || again.ID != item.ID {
		t.Fatalf("dedup failed: %+v %v", again, err)
	}
	if _, err := d.RecordIntelligence(t.Context(), second.ID, intent, "qa", in); !errors.Is(err, ErrIntelligenceInvalid) {
		t.Fatalf("cross-task node accepted: %v", err)
	}
	in.AssetIDs = []int64{9223372036854775807}
	if _, err := d.RecordIntelligence(t.Context(), first.ID, intent, "qa", in); !errors.Is(err, ErrIntelligenceInvalid) {
		t.Fatalf("missing asset accepted: %v", err)
	}
	in.AssetIDs = nil
	if err := d.UpdateIntelligenceStatus(t.Context(), second.ID, item.ID, "verified"); !errors.Is(err, ErrIntelligenceNotFound) {
		t.Fatalf("cross-task mutation accepted: %v", err)
	}
	if err := d.UpdateIntelligenceStatus(t.Context(), first.ID, item.ID, "confirmed"); !errors.Is(err, ErrIntelligenceInvalid) {
		t.Fatalf("invalid status accepted: %v", err)
	}
	if err := d.UpdateIntelligenceStatus(t.Context(), first.ID, item.ID, "verified"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.RecordIntelligence(t.Context(), first.ID, intent, "qa", in); err != nil {
		t.Fatal(err)
	}
	got, err := d.GetIntelligence(t.Context(), item.ID)
	if err != nil || got.Status != "verified" || got.SourceSession != fmt.Sprintf("intent:%d", intent) {
		t.Fatalf("duplicate reset triage or source: %+v %v", got, err)
	}
	sensitive := IntelligenceInput{Kind: "sensitive_data", Title: "样例数据", Content: "qa@example.invalid", Evidence: "/tmp/users-fixture.json", Confidence: "observed"}
	if _, err := d.RecordIntelligence(t.Context(), first.ID, 0, "planner", sensitive); err != nil {
		t.Fatal(err)
	}
	page, err := d.ListIntelligence(t.Context(), IntelligenceFilter{TaskID: first.ID, Limit: 1})
	if err != nil || page.Total != 2 || len(page.Items) != 1 || page.Counts["credential"] != 1 || page.Counts["sensitive_data"] != 1 {
		t.Fatalf("counts/pagination: %+v %v", page, err)
	}
	page, err = d.ListIntelligence(t.Context(), IntelligenceFilter{TaskID: first.ID, Kind: "credential", Status: "verified", Query: "worker-qa"})
	if err != nil || page.Total != 1 || page.Items[0].Content != item.Content || page.Items[0].Evidence != item.Evidence {
		t.Fatalf("filtered content or evidence missing: %+v %v", page, err)
	}
	page, err = d.ListIntelligence(t.Context(), IntelligenceFilter{TaskID: first.ID, Kind: "sensitive_data", Query: "样例"})
	if err != nil || page.Total != 1 || page.Items[0].Content != sensitive.Content || page.Items[0].SourceSession != "plan" {
		t.Fatalf("sensitive data missing: %+v %v", page, err)
	}
	page, err = d.ListIntelligence(t.Context(), IntelligenceFilter{TaskID: first.ID, Offset: 1, Limit: 1})
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != item.ID {
		t.Fatalf("offset: %+v %v", page, err)
	}
	page, err = d.ListIntelligence(t.Context(), IntelligenceFilter{TaskID: second.ID})
	if err != nil || page.Total != 0 {
		t.Fatalf("unrelated task access: %+v %v", page, err)
	}
	if _, err := d.Exec(`INSERT INTO task_relations(task_id,source_task_id) VALUES($1,$2),($3,$1)`, second.ID, first.ID, third.ID); err != nil {
		t.Fatal(err)
	}
	page, err = d.ListIntelligence(t.Context(), IntelligenceFilter{TaskID: second.ID})
	if err != nil || page.Total != 2 || !page.Items[0].Inherited {
		t.Fatalf("direct inheritance: %+v %v", page, err)
	}
	page, err = d.ListIntelligence(t.Context(), IntelligenceFilter{TaskID: third.ID})
	if err != nil || page.Total != 0 {
		t.Fatalf("recursive inheritance leaked: %+v %v", page, err)
	}
	if _, err := d.ListIntelligence(t.Context(), IntelligenceFilter{}); !errors.Is(err, ErrIntelligenceInvalid) {
		t.Fatal("unscoped list allowed")
	}
	if _, err := d.ListIntelligence(t.Context(), IntelligenceFilter{TaskID: first.ID, Query: strings.Repeat("x", 501)}); !errors.Is(err, ErrIntelligenceInvalid) {
		t.Fatal("unbounded query allowed")
	}
	if _, err := d.GetIntelligence(t.Context(), 0); !errors.Is(err, ErrIntelligenceNotFound) {
		t.Fatal("missing item found")
	}
}

func TestIntelligenceSourceSessionAmbiguity(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	task, err := d.CreateTask("intelligence source provenance", "fixture", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteTask(task.ID)
	store := d.Exploration(task.ExplorationID)
	first, err := store.AddIntent(map[string]any{"summary": "first"}, 5, nil, "qa")
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.AddIntent(map[string]any{"summary": "second"}, 5, nil, "qa")
	if err != nil {
		t.Fatal(err)
	}
	fact, err := store.AddNode(KindFact, map[string]any{"summary": "fixture"}, 0, "confirmed", "qa", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Link(first, RelYields, fact); err != nil {
		t.Fatal(err)
	}
	item, err := d.RecordIntelligence(t.Context(), task.ID, fact, "work#3", testIntelligenceInput())
	if err != nil {
		t.Fatal(err)
	}
	got, err := d.GetIntelligence(t.Context(), item.ID)
	if err != nil || got.SourceSession != fmt.Sprintf("intent:%d", first) {
		t.Fatalf("fact source: %+v %v", got, err)
	}
	if err := store.Link(second, RelYields, fact); err != nil {
		t.Fatal(err)
	}
	got, err = d.GetIntelligence(t.Context(), item.ID)
	if err != nil || got.SourceSession != "" {
		t.Fatalf("ambiguous source guessed: %+v %v", got, err)
	}
	in := testIntelligenceInput()
	in.Content = "orphan fixture"
	item, err = d.RecordIntelligence(t.Context(), task.ID, 0, "work#3", in)
	if err != nil {
		t.Fatal(err)
	}
	got, err = d.GetIntelligence(t.Context(), item.ID)
	if err != nil || got.SourceSession != "" {
		t.Fatalf("display label treated as source: %+v %v", got, err)
	}
}

func TestIntelligenceArchiveAssetRemapPreservesManualTriage(t *testing.T) {
	row := map[string]any{"kind": "credential", "title": "manual validation", "content": "fixture", "evidence": "fixture", "confidence": "inferred", "status": "verified", "asset_ids": []int64{10, 20}}
	raw, _ := json.Marshal([]any{row})
	tables, err := remapArchiveAssetReferences(map[string]json.RawMessage{"task_intelligence": raw}, map[int64]int64{10: 30})
	if err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	if err := json.Unmarshal(tables["task_intelligence"], &rows); err != nil {
		t.Fatal(err)
	}
	expected := intelligenceFingerprint(IntelligenceInput{Content: "fixture", AssetIDs: []int64{20, 30}})
	if len(rows) != 1 || rows[0]["fingerprint"] != expected || rows[0]["status"] != "verified" {
		t.Fatal("remap lost dedup or manual triage")
	}
}
