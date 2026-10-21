package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Autumn-27/artex/db"
	actool "github.com/Autumn-27/norma/tool"
)

func intelligenceTestHooks(t *testing.T) {
	t.Helper()
	oldEnabled, oldRecord := IntelligenceEnabled, IntelligenceRecord
	oldResolve, oldAugment := ToolResolve, ToolAugment
	t.Cleanup(func() {
		IntelligenceEnabled, IntelligenceRecord = oldEnabled, oldRecord
		ToolResolve, ToolAugment = oldResolve, oldAugment
	})
	ToolResolve, ToolAugment = nil, nil
}

func intelligenceTestInput() db.IntelligenceInput {
	return db.IntelligenceInput{Kind: "lead", Title: "fixture path", Content: "/tmp/config", Evidence: "read local fixture", Confidence: "observed"}
}

func TestIntelligenceGateAndLiveDisable(t *testing.T) {
	intelligenceTestHooks(t)
	enabled, calls := false, 0
	IntelligenceEnabled = func() bool { return enabled }
	IntelligenceRecord = func(_ context.Context, taskID, nodeID int64, worker string, in db.IntelligenceInput) (int64, error) {
		calls++
		if taskID != 42 || nodeID != 7 || worker != "qa" || in.Status != "pending" {
			t.Fatalf("bad host attribution or normalization: %d %d %s %s", taskID, nodeID, worker, in.Status)
		}
		return 123, nil
	}
	ts := NewToolSet(nil, "qa")
	ts.SetTaskID(42)
	ts.SetOwnerNode(7)
	ctx := WithRunInfo(context.Background(), RunInfo{TaskID: 42, IntentID: 7})
	for _, state := range []bool{false, true} {
		enabled = state
		for _, role := range []string{"worker", "planner", "mainagent"} {
			base := ts.WorkerTools()
			if role == "planner" {
				base = ts.PlannerTools()
			} else if role == "mainagent" {
				base = ts.MainAgentTools()
			}
			tools, def, cleanup := AugmentTools(ctx, role, base)
			cleanup()
			found := false
			for _, tool := range tools {
				if tool.Name() == "record_intelligence" {
					found = true
				}
				if tool.Name() == "record_fact" {
					raw, _ := json.Marshal(tool.InputSchema())
					if strings.Contains(string(raw), "intelligence") != state {
						t.Fatalf("%s: incorrect intelligence schema gate", role)
					}
					if state {
						props := tool.InputSchema()["properties"].(map[string]any)
						facts := props["facts"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)
						if facts["intelligence"] == nil {
							t.Fatal("batch intelligence schema missing")
						}
					}
				}
			}
			if found != state || (def.IntelligenceGuidance != "") != state {
				t.Fatalf("%s: incorrect tool/guidance gate", role)
			}
		}
	}
	input, _ := json.Marshal(intelligenceTestInput())
	tool := ts.recordIntelligence()
	res, err := tool.Call(ctx, input, nil)
	if err != nil || res.IsError || calls != 1 || ts.Writes().Total() != 1 || !strings.Contains(ts.Writes().String(), "情报1") {
		t.Fatalf("record failed: %v %s", err, res.Flatten())
	}
	enabled = false
	res, err = tool.Call(ctx, input, nil)
	if err != nil || !res.IsError || calls != 1 {
		t.Fatal("live disable did not reject write")
	}
	if got := (WriteCounts{Facts: 1}).String(); got != "事实1 资产0 漏洞0" {
		t.Fatal("changed existing write summary", got)
	}
}

func TestIntelligenceTaskContextValidationAndHostFailures(t *testing.T) {
	intelligenceTestHooks(t)
	IntelligenceEnabled = func() bool { return true }
	calls := 0
	IntelligenceRecord = func(_ context.Context, taskID, nodeID int64, worker string, in db.IntelligenceInput) (int64, error) {
		calls++
		if taskID != 10 || nodeID != 20 {
			t.Fatal("context fallback lost source")
		}
		return 0, fmt.Errorf("fixture recorder unavailable")
	}
	ts := NewToolSet(nil, "qa")
	tools, def, cleanup := AugmentTools(context.Background(), "worker", ts.WorkerTools())
	cleanup()
	if def.IntelligenceGuidance != "" {
		t.Fatal("taskless guidance")
	}
	for _, tool := range tools {
		if tool.Name() == "record_intelligence" {
			t.Fatal("taskless tool exposed")
		}
	}
	input, _ := json.Marshal(intelligenceTestInput())
	res, _ := ts.recordIntelligence().Call(context.Background(), input, nil)
	if !res.IsError || calls != 0 {
		t.Fatal("taskless direct call accepted")
	}
	ctx := WithRunInfo(context.Background(), RunInfo{TaskID: 10, IntentID: 20})
	for _, invalid := range []json.RawMessage{json.RawMessage(`{"kind":`), json.RawMessage(`{"kind":"unknown"}`)} {
		res, _ = ts.recordIntelligence().Call(ctx, invalid, nil)
		if !res.IsError || calls != 0 {
			t.Fatal("invalid input reached recorder")
		}
	}
	res, _ = ts.recordIntelligence().Call(ctx, input, nil)
	if !res.IsError || calls != 1 || ts.Writes().Total() != 0 || !strings.Contains(res.Flatten(), "fixture recorder unavailable") {
		t.Fatal("host failure hidden or counted as success")
	}
	IntelligenceRecord = nil
	res, _ = ts.recordIntelligence().Call(ctx, input, nil)
	if !res.IsError {
		t.Fatal("missing host accepted")
	}
}

func TestIntelligenceToolSeedBindingsAndResolvedGate(t *testing.T) {
	intelligenceTestHooks(t)
	found := false
	for _, seed := range BuiltinToolSeeds() {
		if seed.Key != "record_intelligence" {
			continue
		}
		found = true
		for _, role := range []string{"mainagent", "planner", "worker"} {
			bound := false
			for _, got := range seed.Agents {
				bound = bound || got == role
			}
			if !bound {
				t.Fatalf("missing default %s binding", role)
			}
		}
	}
	if !found {
		t.Fatal("missing tool seed")
	}
	IntelligenceEnabled = func() bool { return true }
	ToolResolve = func(_ context.Context, _ string, tools []actool.CoreTool) []actool.CoreTool { return nil }
	_, def, cleanup := AugmentTools(WithRunInfo(context.Background(), RunInfo{TaskID: 1}), "worker", NewToolSet(nil, "qa").WorkerTools())
	cleanup()
	if def.IntelligenceGuidance != "" {
		t.Fatal("guidance claims a DB-filtered tool exists")
	}
}

func TestIntelligenceAssetAndFactWriteback(t *testing.T) {
	intelligenceTestHooks(t)
	d := testDB(t)
	defer d.Close()
	task, err := d.CreateTask("intelligence tool integration", "local fixtures", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteTask(task.ID)
	ts := NewToolSet(d.Exploration(task.ExplorationID), "qa")
	ts.SetTaskID(task.ID)
	ts.SetAssetStore(d.Assets(), d.Companies())
	node, err := d.Exploration(task.ExplorationID).AddIntent(map[string]any{"summary": "fixture"}, 5, nil, "qa")
	if err != nil {
		t.Fatal(err)
	}
	ts.SetOwnerNode(node)
	IntelligenceEnabled = func() bool { return true }
	IntelligenceRecord = func(ctx context.Context, tid, nid int64, worker string, in db.IntelligenceInput) (int64, error) {
		item, err := d.RecordIntelligence(ctx, tid, nid, worker, in)
		if err != nil {
			return 0, err
		}
		return item.ID, nil
	}
	defer d.Exec(`DELETE FROM assets WHERE domain='intelligence-tool.example' OR root_domain='intelligence-tool.example'`)
	payload := map[string]any{"assets": []any{map[string]any{"type": "service", "url": "https://intelligence-tool.example", "auth": []any{map[string]any{"username": "qa", "password": "fixture"}}}}}
	for range 2 {
		result := callInsertAssets(t, ts, payload)
		if errs, ok := result["errors"].([]any); ok && len(errs) > 0 {
			t.Fatal(result)
		}
	}
	raw := json.RawMessage(`{"summary":"found useful fixture","intelligence":[{"kind":"lead","title":"fixture path","content":"/tmp/config","evidence":"read fixture","confidence":"observed"}]}`)
	res, err := ts.recordFact().Call(context.Background(), raw, nil)
	if err != nil || res.IsError {
		t.Fatal("fact intelligence", err, res.Flatten())
	}
	batch := json.RawMessage(`{"facts":[{"summary":"found sensitive fixture","intelligence":[{"kind":"sensitive_data","title":"fixture dataset","content":"sample@example.invalid","evidence":"read fixture dataset","confidence":"observed"}]}]}`)
	res, err = ts.recordFact().Call(context.Background(), batch, nil)
	if err != nil || res.IsError || strings.Contains(res.Flatten(), "errors") {
		t.Fatal("batch intelligence", err, res.Flatten())
	}
	page, err := d.ListIntelligence(t.Context(), db.IntelligenceFilter{TaskID: task.ID})
	if err != nil || page.Total != 3 || page.Counts["credential"] != 1 || page.Counts["lead"] != 1 || page.Counts["sensitive_data"] != 1 {
		t.Fatalf("writebacks missing: %+v %v", page, err)
	}
	for _, item := range page.Items {
		if item.NodeID == nil {
			t.Fatal("lost source node")
		}
		if item.Kind == "credential" && item.Status != "unverified" {
			t.Fatal("auto credential was marked verified")
		}
	}
	in := intelligenceTestInput()
	items := make([]db.IntelligenceInput, 21)
	for i := range items {
		items[i] = in
	}
	raw, _ = json.Marshal(map[string]any{"summary": "oversized intelligence", "intelligence": items})
	res, _ = ts.recordFact().Call(context.Background(), raw, nil)
	if !res.IsError || !strings.Contains(res.Flatten(), "20") {
		t.Fatal("oversized intelligence accepted")
	}
	IntelligenceEnabled = func() bool { return false }
	res, _ = ts.recordFact().Call(context.Background(), json.RawMessage(`{"summary":"late fact","intelligence":[{"kind":"lead","title":"late","content":"late","evidence":"fixture","confidence":"observed"}]}`), nil)
	if !res.IsError || !strings.Contains(res.Flatten(), "事实 #") || !strings.Contains(res.Flatten(), "关闭") {
		t.Fatal("disabled fact intelligence missing partial write notice")
	}
}
