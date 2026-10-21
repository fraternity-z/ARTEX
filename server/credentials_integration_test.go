package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/llmrec"
	"github.com/Autumn-27/norma/llm"
)

type credentialServerProvider struct {
	calls   int
	invalid bool
}

func (p *credentialServerProvider) Complete(context.Context, llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	p.calls++
	text := `{"accounts":[{"username":"qa-user","password":"qa-pass-321"}]}`
	if p.invalid {
		text = "invalid fixture JSON"
	}
	return llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{llm.TextBlock(text)}}, "end_turn", llm.Usage{InputTokens: 88, OutputTokens: 22}, nil
}

func (*credentialServerProvider) Stream(context.Context, llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	return func(func(llm.StreamEvent, error) bool) { panic("unexpected streaming") }
}

func credentialIntegrationServer(t *testing.T) (*Server, int64, func(string, string, string) *httptest.ResponseRecorder) {
	t.Helper()
	if _, _, err := db.DSN(); err != nil {
		t.Skip("test postgres not configured")
	}
	s, initial, request := trafficEvidenceServer(t)
	finding, err := s.m.pg.GetFinding(initial.FindingID)
	if err != nil {
		t.Fatal(err)
	}
	oldRecord, oldAgent, oldBind := s.m.pg.GetBool(db.SettingTaskIntelligence, false), s.m.pg.GetBool(db.SettingCredentialAgent, false), s.m.pg.GetBool(db.SettingCredentialTraffic, false)
	t.Cleanup(func() {
		if err := s.m.pg.SetIntelligenceSettings(context.Background(), &oldRecord, &oldAgent, &oldBind); err != nil {
			t.Error("restore credential settings", err)
		}
	})
	off := false
	if err := s.m.pg.SetIntelligenceSettings(t.Context(), &off, &off, &off); err != nil {
		t.Fatal(err)
	}
	return s, *finding.TaskID, request
}

func TestCredentialProcessingFlowBindingAndScopes(t *testing.T) {
	s, taskID, request := credentialIntegrationServer(t)
	for _, body := range []string{`{"credential_traffic_binding":true}`, `{"credential_agent_enabled":true}`} {
		if r := request("PUT", "/api/settings", body); r.Code != 400 {
			t.Fatal("ungated setting", r.Code, r.Body)
		}
	}
	for _, body := range []string{`{"task_intelligence_enabled":true}`, `{"credential_agent_enabled":true}`} {
		if r := request("PUT", "/api/settings", body); r.Code != 200 {
			t.Fatal(r.Code, r.Body)
		}
	}
	if s.settingsPayload()[db.SettingCredentialTraffic] != false {
		t.Fatal("binding default must be off")
	}
	assetID, err := s.m.Assets().UpsertHTTPService(db.UpsertHTTPServiceReq{URL: "http://evidence.local", TaskID: taskID})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.m.pg.Exec(`DELETE FROM assets WHERE id=$1`, assetID) })
	in := db.IntelligenceInput{Kind: "credential", Title: "structured fixture", Content: `{"username":"qa-user","password":"qa-pass-321"}`, Evidence: "http://evidence.local local fixture", Confidence: "observed", AssetIDs: []int64{assetID}}
	item, err := s.m.pg.RecordIntelligence(t.Context(), taskID, 0, "fixture", in)
	if err != nil {
		t.Fatal(err)
	}
	mock := &credentialServerProvider{}
	s.llmOn = true
	s.llmProv = llmrec.Wrap(mock, s.m.pg, "fixture", "fixture", "", "", func() bool { return false })
	s.llmCfg.Model = "fixture"
	process := func(binding bool) *db.Intelligence {
		t.Helper()
		job, err := s.m.pg.ClaimCredentialJob(t.Context(), binding, taskID)
		if err != nil || job == nil {
			t.Fatal("missing credential job", err)
		}
		s.processCredentialJob(t.Context(), job)
		got, err := s.m.pg.GetIntelligence(t.Context(), job.ID)
		if err != nil || got.Credentials == nil {
			t.Fatal("missing processed credentials", err)
		}
		return got
	}
	got := process(false)
	if got.Credentials.State != "ready" || len(got.Credentials.Accounts) != 1 || mock.calls != 0 || got.Credentials.TrafficState != "off" {
		t.Fatalf("rules: %+v calls=%d", got.Credentials, mock.calls)
	}
	for id, body := range map[string]string{
		"credential-proof":      `{"username":"qa-user","password":"qa-pass-321"}`,
		"credential-wrong":      `{"username":"other-user","password":"qa-pass-321"}`,
		"credential-other-host": `{"username":"qa-user","password":"qa-pass-321"}`,
	} {
		seedServerEvidenceFlow(t, s, id, []byte(body))
		// Direct fixture inserts bypass the capture writer's full-text indexing.
		if _, err := s.m.traffic.DB().Exec(`INSERT INTO ex_fts(rowid,content) SELECT rowid,? FROM exchanges WHERE id=?`, body, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.m.traffic.DB().Exec(`UPDATE exchanges SET host='other.local',url='http://other.local/' WHERE id='credential-other-host'`); err != nil {
		t.Fatal(err)
	}
	if r := request("PUT", "/api/settings", `{"credential_traffic_binding":true}`); r.Code != 200 {
		t.Fatal(r.Code, r.Body)
	}
	got = process(true)
	if got.Credentials.TrafficState != "ready" || len(got.Credentials.Traffic) != 1 || got.Credentials.Traffic[0].ID != "credential-proof" || mock.calls != 0 || got.Status != "unverified" {
		t.Fatalf("binding: %+v calls=%d", got.Credentials, mock.calls)
	}
	base := fmt.Sprintf("/api/tasks/%d/intelligence/%d/traffic/credential-proof", taskID, item.ID)
	if r := request("GET", base, ""); r.Code != 200 || r.Header().Get("Cache-Control") != "no-store" || !strings.Contains(r.Body.String(), "qa-pass-321") || !strings.Contains(r.Body.String(), `"unavailable":false`) {
		t.Fatal("live flow detail", r.Code, r.Body)
	}
	other, err := s.m.pg.CreateTask("credential scope", "local", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.m.pg.DeleteTask(other.ID) })
	otherPath := fmt.Sprintf("/api/tasks/%d/intelligence/%d", other.ID, item.ID)
	if r := request("GET", otherPath+"/traffic/credential-proof", ""); r.Code != 404 {
		t.Fatal("unrelated task reads flow", r.Code)
	}
	if _, err := s.m.pg.Exec(`INSERT INTO task_relations(task_id,source_task_id) VALUES($1,$2)`, other.ID, taskID); err != nil {
		t.Fatal(err)
	}
	if r := request("GET", otherPath+"/traffic/credential-proof", ""); r.Code != 200 {
		t.Fatal("inherited read failed", r.Code)
	}
	if r := request("POST", otherPath+"/credentials/retry", ""); r.Code != 404 {
		t.Fatal("inherited write accepted", r.Code)
	}
	if r := request("GET", fmt.Sprintf("/api/assets/%d/intelligence/%d/traffic/credential-proof", assetID, item.ID), ""); r.Code != 200 {
		t.Fatal("asset read failed", r.Code, r.Body)
	}
	for _, table := range []string{"exchange_bodies", "exchanges"} {
		if _, err := s.m.traffic.DB().Exec("DELETE FROM " + table + " WHERE id='credential-proof'"); err != nil {
			t.Fatal(err)
		}
	}
	if r := request("GET", base, ""); r.Code != 200 || !strings.Contains(r.Body.String(), `"unavailable":true`) || !strings.Contains(r.Body.String(), "qa-pass-321") {
		t.Fatal("saved excerpt lost", r.Code, r.Body)
	}
	if r := request("POST", fmt.Sprintf("/api/tasks/%d/intelligence/%d/credentials/retry", taskID, item.ID), ""); r.Code != 202 {
		t.Fatal(r.Code, r.Body)
	}
	got = process(true)
	if len(got.Credentials.Traffic) != 1 || got.Credentials.Traffic[0].ID != "credential-proof" || !strings.Contains(got.Credentials.Traffic[0].Excerpt, "qa-pass-321") {
		t.Fatal("retry lost saved evidence")
	}
	in.Title, in.Content = "prose fixture", "qa-user 的密码为 qa-pass-321"
	prose, err := s.m.pg.RecordIntelligence(t.Context(), taskID, 0, "fixture", in)
	if err != nil {
		t.Fatal(err)
	}
	got = process(true)
	if got.ID != prose.ID || got.Credentials.Method != "agent" || mock.calls != 1 || got.Credentials.InputTokens != 88 || got.Credentials.OutputTokens != 22 {
		t.Fatalf("model: %+v calls=%d", got.Credentials, mock.calls)
	}
	var count int
	if err := s.m.pg.QueryRow(`SELECT count(*) FROM llm_usage WHERE task_id=$1 AND worker=$2 AND input_tokens=88 AND output_tokens=22`, fmt.Sprint(taskID), db.CredentialAgentKey).Scan(&count); err != nil || count != 1 {
		t.Fatal("usage not attributed", count, err)
	}
	if r := request("PUT", "/api/settings", `{"credential_agent_enabled":false}`); r.Code != 200 {
		t.Fatal(r.Code, r.Body)
	}
	if s.m.pg.GetBool(db.SettingCredentialTraffic, true) {
		t.Fatal("dependent switch not cleared")
	}
	if r := request("POST", fmt.Sprintf("/api/tasks/%d/intelligence/%d/credentials/retry", taskID, item.ID), ""); r.Code != 409 {
		t.Fatal("off retry accepted", r.Code)
	}
	list := request("GET", fmt.Sprintf("/api/tasks/%d/intelligence", taskID), "")
	var page db.IntelligencePage
	if list.Code != 200 || json.Unmarshal(list.Body.Bytes(), &page) != nil || len(page.Items) != 2 || page.CredentialAgentEnabled || page.Items[0].Credentials == nil {
		t.Fatal("off hid derived credentials", list.Code, list.Body)
	}
}

type blockingCredentialProvider struct{ entered chan struct{} }

func (p *blockingCredentialProvider) Complete(ctx context.Context, _ llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	close(p.entered)
	<-ctx.Done()
	return llm.Message{}, "", llm.Usage{}, errors.New("cancelled fixture")
}

func (*blockingCredentialProvider) Stream(context.Context, llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	return func(func(llm.StreamEvent, error) bool) { panic("unexpected streaming") }
}

func TestCredentialDisableCancelsInFlightModel(t *testing.T) {
	s, taskID, request := credentialIntegrationServer(t)
	on := true
	if err := s.m.pg.SetIntelligenceSettings(t.Context(), &on, &on, nil); err != nil {
		t.Fatal(err)
	}
	item, err := s.m.pg.RecordIntelligence(t.Context(), taskID, 0, "fixture", db.IntelligenceInput{Kind: "credential", Title: "cancel fixture", Content: "qa-user 的密码为 qa-pass-321", Evidence: "local fixture", Confidence: "observed"})
	if err != nil {
		t.Fatal(err)
	}
	mock := &blockingCredentialProvider{entered: make(chan struct{})}
	s.llmOn, s.llmProv = true, mock
	job, err := s.m.pg.ClaimCredentialJob(t.Context(), false, taskID)
	if err != nil || job == nil {
		t.Fatal("missing credential job", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan struct{})
	go func() { s.processCredentialJob(ctx, job); close(done) }()
	defer func() { cancel(); <-done }()
	select {
	case <-mock.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("model never started")
	}
	if r := request("PUT", "/api/settings", `{"credential_agent_enabled":false}`); r.Code != 200 {
		t.Fatal(r.Code, r.Body)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("500ms switch polling did not cancel model")
	}
	got, err := s.m.pg.GetIntelligence(t.Context(), item.ID)
	if err != nil || got.Credentials == nil || got.Credentials.State != "queued" || len(got.Credentials.Accounts) != 0 || got.Content != item.Content {
		t.Fatal("off published result or lost raw input", err)
	}
	if next, err := s.m.pg.ClaimCredentialJob(t.Context(), false, taskID); err != nil || next != nil {
		t.Fatal("off admitted a new run", err)
	}
}

func TestCredentialFailedModelCanRetry(t *testing.T) {
	s, taskID, request := credentialIntegrationServer(t)
	on := true
	if err := s.m.pg.SetIntelligenceSettings(t.Context(), &on, &on, nil); err != nil {
		t.Fatal(err)
	}
	item, err := s.m.pg.RecordIntelligence(t.Context(), taskID, 0, "fixture", db.IntelligenceInput{Kind: "credential", Title: "retry fixture", Content: "qa-user 的密码为 qa-pass-321", Evidence: "local fixture", Confidence: "observed"})
	if err != nil {
		t.Fatal(err)
	}
	mock := &credentialServerProvider{invalid: true}
	s.llmOn, s.llmProv = true, mock
	job, err := s.m.pg.ClaimCredentialJob(t.Context(), false, taskID)
	if err != nil || job == nil {
		t.Fatal("missing credential job", err)
	}
	s.processCredentialJob(t.Context(), job)
	got, err := s.m.pg.GetIntelligence(t.Context(), item.ID)
	if err != nil || got.Credentials == nil || got.Credentials.State != "error" || mock.calls != 1 {
		t.Fatal("failure not recorded", err)
	}
	if again, err := s.m.pg.ClaimCredentialJob(t.Context(), false, taskID); err != nil || again != nil {
		t.Fatal("failed model automatically retried", err)
	}
	mock.invalid = false
	if r := request("POST", fmt.Sprintf("/api/tasks/%d/intelligence/%d/credentials/retry", taskID, item.ID), ""); r.Code != 202 {
		t.Fatal(r.Code, r.Body)
	}
	job, err = s.m.pg.ClaimCredentialJob(t.Context(), false, taskID)
	if err != nil || job == nil {
		t.Fatal("missing retry job", err)
	}
	s.processCredentialJob(t.Context(), job)
	got, err = s.m.pg.GetIntelligence(t.Context(), item.ID)
	if err != nil || got.Credentials == nil || got.Credentials.State != "ready" || len(got.Credentials.Accounts) != 1 || mock.calls != 2 {
		t.Fatal("explicit retry did not call model again", err)
	}
}
