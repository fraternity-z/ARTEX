package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Autumn-27/artex/db"
)

func TestIntelligenceErrorsDoNotExposeStorageDetails(t *testing.T) {
	w := httptest.NewRecorder()
	intelligenceError(w, errors.New("postgres://user:fixture-secret@localhost/db"))
	if w.Code != 500 || strings.Contains(w.Body.String(), "fixture-secret") || strings.Contains(w.Body.String(), "postgres") {
		t.Fatalf("storage detail leaked: %d %s", w.Code, w.Body)
	}
	w = httptest.NewRecorder()
	intelligenceError(w, db.ErrTaskArchiveState)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "归档") {
		t.Fatalf("archive conflict unclear: %d %s", w.Code, w.Body)
	}
}

func TestIntelligenceHTTPInvalidInput(t *testing.T) {
	s := &Server{}
	for _, test := range []struct {
		name    string
		handler http.HandlerFunc
		taskID  string
		itemID  string
		body    string
	}{
		{"list task id", s.listIntelligence, "0", "", ""},
		{"detail task id", s.getIntelligence, "invalid", "1", ""},
		{"update task id", s.updateIntelligence, "-1", "1", `{"status":"verified"}`},
		{"update item id", s.updateIntelligence, "1", "invalid", `{"status":"verified"}`},
		{"update JSON", s.updateIntelligence, "1", "1", `{"status":`},
		{"update body bound", s.updateIntelligence, "1", "1", strings.Repeat(" ", 4097) + `{}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := httptest.NewRequest("PATCH", "/api/tasks/1/intelligence/1", strings.NewReader(test.body))
			r.SetPathValue("id", test.taskID)
			r.SetPathValue("intelID", test.itemID)
			w := httptest.NewRecorder()
			test.handler(w, r)
			if w.Code != 400 || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("invalid request accepted or cached: %d %s", w.Code, w.Body)
			}
		})
	}
}

func TestIntelligenceHTTPSettingsAndScope(t *testing.T) {
	if _, _, err := db.DSN(); err != nil {
		t.Skip("test postgres not configured")
	}
	s, initial, request := trafficEvidenceServer(t)
	finding, err := s.m.pg.GetFinding(initial.FindingID)
	if err != nil {
		t.Fatal(err)
	}
	taskID := *finding.TaskID
	previous := s.m.pg.GetBool(db.SettingTaskIntelligence, false)
	defer s.m.pg.SetBool(db.SettingTaskIntelligence, previous)
	if response := request("PUT", "/api/settings", `{"task_intelligence_enabled":true}`); response.Code != 200 {
		t.Fatal(response.Code, response.Body)
	}
	if !s.m.pg.GetBool(db.SettingTaskIntelligence, false) {
		t.Fatal("setting not persisted")
	}
	in := db.IntelligenceInput{Kind: "credential", Title: "test credential", Content: "local-api-fixture-secret", Evidence: "local fixture evidence", Confidence: "observed"}
	item, err := s.m.pg.RecordIntelligence(t.Context(), taskID, 0, "qa", in)
	if err != nil {
		t.Fatal(err)
	}
	base := fmt.Sprintf("/api/tasks/%d/intelligence", taskID)
	response := request("GET", base, "")
	if response.Code != 200 || response.Header().Get("Cache-Control") != "no-store" || !strings.Contains(response.Body.String(), in.Content) || !strings.Contains(response.Body.String(), in.Evidence) {
		t.Fatalf("list missing content or cache protection: %d %s", response.Code, response.Body)
	}
	response = request("GET", fmt.Sprintf("%s/%d", base, item.ID), "")
	if response.Code != 200 || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("detail unavailable", response.Code, response.Body)
	}
	if response := request("PATCH", fmt.Sprintf("%s/%d", base, item.ID), `{"status":"verified"}`); response.Code != 200 {
		t.Fatal(response.Code, response.Body)
	}
	if response := request("PATCH", fmt.Sprintf("%s/%d", base, item.ID), `{"status":"confirmed"}`); response.Code != 400 {
		t.Fatal("invalid status accepted", response.Code)
	}
	if response := request("PATCH", fmt.Sprintf("%s/%d", base, item.ID), `{"status":`); response.Code != 400 {
		t.Fatal("invalid JSON accepted", response.Code)
	}
	if response := request("PATCH", fmt.Sprintf("%s/%d", base, item.ID), strings.Repeat("x", 4097)); response.Code != 400 {
		t.Fatal("unbounded body accepted", response.Code)
	}
	if response := request("GET", base+"?q="+strings.Repeat("x", 501), ""); response.Code != 400 {
		t.Fatal("unbounded query accepted", response.Code)
	}
	if response := request("GET", "/api/tasks/not-a-number/intelligence", ""); response.Code != 400 {
		t.Fatal("invalid task id accepted", response.Code)
	}
	if response := request("GET", base+"/0", ""); response.Code != 400 {
		t.Fatal("invalid intelligence id accepted", response.Code)
	}
	other, err := s.m.pg.CreateTask("unrelated intelligence API", "test", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.m.pg.DeleteTask(other.ID)
	otherPath := fmt.Sprintf("/api/tasks/%d/intelligence/%d", other.ID, item.ID)
	for _, method := range []string{"GET", "PATCH"} {
		response := request(method, otherPath, `{"status":"invalid"}`)
		if response.Code != 404 {
			t.Fatal("cross task access", method, response.Code, response.Body)
		}
	}
	if _, err := s.m.pg.Exec(`INSERT INTO task_relations(task_id,source_task_id) VALUES($1,$2)`, other.ID, taskID); err != nil {
		t.Fatal(err)
	}
	response = request("GET", otherPath, "")
	var got db.Intelligence
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &got) != nil || !got.Inherited {
		t.Fatal("missing inherited record", response.Code, response.Body)
	}
	if response := request("PATCH", otherPath, `{"status":"invalid"}`); response.Code != 404 {
		t.Fatal("inherited mutation accepted")
	}
	if response := request("PUT", "/api/settings", `{"task_intelligence_enabled":false}`); response.Code != 200 {
		t.Fatal(response.Code, response.Body)
	}
	response = request("GET", base, "")
	var page db.IntelligencePage
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &page) != nil || page.Total != 1 || page.Enabled {
		t.Fatal("disable hid records or retained enabled state", response.Code, response.Body)
	}
	if err := s.m.pg.DeleteTask(other.ID); err != nil {
		t.Fatal(err)
	}
	if response := request("GET", fmt.Sprintf("/api/tasks/%d/intelligence", other.ID), ""); response.Code != 404 {
		t.Fatal("deleted task visible", response.Code)
	}
}
