package db

import (
	"errors"
	"reflect"
	"testing"
)

func TestCredentialLiveSettingsAndLeases(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	oldRecording := d.GetBool(SettingTaskIntelligence, false)
	oldAgent, oldBinding := d.GetBool(SettingCredentialAgent, false), d.GetBool(SettingCredentialTraffic, false)
	defer func() {
		if err := d.SetIntelligenceSettings(t.Context(), &oldRecording, &oldAgent, &oldBinding); err != nil {
			t.Error(err)
		}
	}()
	on, off := true, false
	set := func(recording, agent, binding *bool) {
		t.Helper()
		if err := d.SetIntelligenceSettings(t.Context(), recording, agent, binding); err != nil {
			t.Fatal(err)
		}
	}
	settings := func(recording, agent, binding bool) {
		t.Helper()
		if d.GetBool(SettingTaskIntelligence, !recording) != recording || d.GetBool(SettingCredentialAgent, !agent) != agent || d.GetBool(SettingCredentialTraffic, !binding) != binding {
			t.Fatal("unexpected persisted switch state")
		}
	}
	set(&off, nil, nil)
	if err := d.SetIntelligenceSettings(t.Context(), nil, &on, &on); !errors.Is(err, ErrIntelligenceInvalid) {
		t.Fatalf("missing recording dependency: %v", err)
	}
	settings(false, false, false)
	if err := d.SetIntelligenceSettings(t.Context(), &on, &off, &on); !errors.Is(err, ErrIntelligenceInvalid) {
		t.Fatalf("missing agent dependency: %v", err)
	}
	settings(false, false, false) // Rejected multi-switch updates must roll back together.
	set(&on, &on, &on)
	settings(true, true, true)
	set(nil, &off, nil)
	settings(true, false, false)
	set(nil, &on, &on)
	set(&off, nil, nil)
	settings(false, false, false)
	set(&on, &on, &off)

	task, err := d.CreateTask("credential lease fixture.invalid", "fixture.invalid", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = d.Exec(`DELETE FROM task_archives WHERE task_id=$1`, task.ID)
		if err := d.DeleteTask(task.ID); err != nil {
			t.Error(err)
		}
	}()
	in := testIntelligenceInput()
	in.Content, in.Evidence = `{"username":"qa@fixture.invalid","password":"fixture.invalid"}`, "https://fixture.invalid/source"
	item, err := d.RecordIntelligence(t.Context(), task.ID, 0, "qa", in)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.UpdateIntelligenceStatus(t.Context(), task.ID, item.ID, "verified"); err != nil {
		t.Fatal(err)
	}
	claim := func(bind bool) *Intelligence {
		t.Helper()
		got, err := d.ClaimCredentialJob(t.Context(), bind, task.ID)
		if err != nil || got == nil || got.ID != item.ID || got.Credentials == nil || got.Credentials.State != "processing" || got.Credentials.Lease == "" {
			t.Fatalf("claim failed: %+v %v", got, err)
		}
		return got
	}
	empty := func(bind bool) {
		t.Helper()
		if got, err := d.ClaimCredentialJob(t.Context(), bind, task.ID); err != nil || got != nil {
			t.Fatalf("unexpected duplicate claim: %+v %v", got, err)
		}
	}
	read := func() *Intelligence {
		t.Helper()
		got, err := d.GetIntelligence(t.Context(), item.ID)
		if err != nil || got.Credentials == nil {
			t.Fatalf("read result: %+v %v", got, err)
		}
		return got
	}
	first := claim(false)
	empty(false)
	if err := d.RetryCredentialJob(t.Context(), task.ID, item.ID); !errors.Is(err, ErrIntelligenceNotFound) {
		t.Fatalf("processing retry accepted: %v", err)
	}
	result := CredentialAnalysis{State: "ready", Method: "rules", SourceHash: CredentialSourceHash(in.Content), Accounts: []CredentialAccount{{Username: "qa@fixture.invalid", Password: "fixture.invalid"}}, TrafficState: "off"}
	if err := d.FinishCredentialJob(t.Context(), first, result); err != nil {
		t.Fatal(err)
	}
	got := read()
	if got.Status != "verified" || got.Content != item.Content || got.Evidence != item.Evidence || got.Credentials.State != "ready" || got.Credentials.Lease != "" || got.Credentials.ProcessedAt == nil || !reflect.DeepEqual(got.Credentials.Accounts, result.Accounts) {
		t.Fatal("completion lost analysis, raw evidence or manual triage")
	}
	empty(false)
	set(nil, nil, &on)
	second := claim(true)
	if !reflect.DeepEqual(second.Credentials.Accounts, result.Accounts) {
		t.Fatal("late binding lost extracted accounts")
	}
	stale := result
	stale.State = "error"
	if err := d.FinishCredentialJob(t.Context(), first, stale); err != nil {
		t.Fatal(err)
	}
	if got := read(); got.Credentials.State != "processing" || got.Credentials.Lease != second.Credentials.Lease {
		t.Fatal("stale completion overwrote newer lease")
	}
	set(nil, &off, nil)
	if err := d.FinishCredentialJob(t.Context(), second, result); err != nil {
		t.Fatal(err)
	}
	if read().Credentials.State != "queued" {
		t.Fatal("disabled agent published completion")
	}
	empty(false)
	set(nil, &on, nil)
	third := claim(false)
	if _, err := d.Exec(`UPDATE task_intelligence SET credential_analysis=jsonb_set(credential_analysis,'{lease_until}',to_jsonb(now()-interval '1 minute')) WHERE id=$1`, item.ID); err != nil {
		t.Fatal(err)
	}
	recovered := claim(false)
	if recovered.Credentials.Lease == third.Credentials.Lease {
		t.Fatal("expired lease was not renewed")
	}
	if err := d.FinishCredentialJob(t.Context(), third, stale); err != nil {
		t.Fatal(err)
	}
	if read().Credentials.Lease != recovered.Credentials.Lease {
		t.Fatal("expired completion overwrote recovered lease")
	}
	if err := d.FinishCredentialJob(t.Context(), recovered, result); err != nil {
		t.Fatal(err)
	}
	other, err := d.CreateTask("credential ownership fixture.invalid", "fixture.invalid", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteTask(other.ID)
	if err := d.RetryCredentialJob(t.Context(), other.ID, item.ID); !errors.Is(err, ErrIntelligenceNotFound) {
		t.Fatalf("cross-task retry accepted: %v", err)
	}
	if err := d.RetryCredentialJob(t.Context(), task.ID, item.ID); err != nil {
		t.Fatal(err)
	}
	if got := read(); got.Credentials.State != "queued" || !got.Credentials.ForceExtract {
		t.Fatal("retry did not request extraction")
	}
	active := claim(false)
	if err := d.SetPaused(task.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := d.QueueTaskArchive(task.ID); err != nil {
		t.Fatal(err)
	}
	empty(false)
	if err := d.FinishCredentialJob(t.Context(), active, result); !errors.Is(err, ErrTaskArchiveState) {
		t.Fatalf("archive accepted completion: %v", err)
	}
	if err := d.RetryCredentialJob(t.Context(), task.ID, item.ID); !errors.Is(err, ErrTaskArchiveState) {
		t.Fatalf("archive accepted retry: %v", err)
	}
}
