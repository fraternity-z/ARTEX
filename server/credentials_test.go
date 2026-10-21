package server

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/traffic"
)

func TestCredentialHostBoundary(t *testing.T) {
	hosts := map[string]bool{"example.invalid": true, "api.exact.invalid": false}
	for _, test := range []struct {
		url     string
		allowed bool
	}{
		{"https://example.invalid/login", true}, {"http://sub.example.invalid:8080/", true},
		{"https://api.exact.invalid/", true}, {"https://child.api.exact.invalid/", false},
		{"https://evil-example.invalid/", false}, {"https://example.invalid.evil.invalid/", false},
		{"https://example.invalid@evil.invalid/", false}, {"file://example.invalid/", false},
		{"//example.invalid/", false}, {"broken", false},
	} {
		if got := matchesCredentialHost(test.url, hosts); got != test.allowed {
			t.Fatalf("host boundary %q = %v", test.url, got)
		}
	}
}

func TestCredentialTrafficMatchesRequireSecretAndAccount(t *testing.T) {
	for _, test := range []struct {
		raw     string
		account db.CredentialAccount
		match   bool
	}{
		{"alice fixture-password", db.CredentialAccount{Username: "alice", Password: "fixture-password"}, true},
		{"bob fixture-password", db.CredentialAccount{Username: "alice", Password: "fixture-password"}, false},
		{"alice unrelated", db.CredentialAccount{Username: "alice", Password: "fixture-password"}, false},
		{"short", db.CredentialAccount{Password: "short"}, false},
		{"long-password", db.CredentialAccount{Password: "long-password"}, true},
		{"fixture-token", db.CredentialAccount{Token: "fixture-token"}, true},
		{"short", db.CredentialAccount{Token: "short"}, false},
		{"token=fixture%2Btoken", db.CredentialAccount{Token: "fixture+token"}, true},
	} {
		if got := credentialMatches(test.raw, test.account); got != test.match {
			t.Fatal("secret/account matching failed")
		}
	}
	raw := strings.Repeat("文", 1000) + "fixture-password" + strings.Repeat("尾", 1000)
	excerpt := credentialExcerpt(raw, []db.CredentialAccount{{Password: "fixture-password"}})
	if !utf8.ValidString(excerpt) || len([]rune(excerpt)) > 600 || !strings.Contains(excerpt, "fixture-password") {
		t.Fatal("excerpt lost match or bounds")
	}
}

func TestCredentialTrafficUsesCapturedHostAndLiteralSecret(t *testing.T) {
	tr, err := traffic.Open(t.TempDir(), "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	s := &Server{m: &Manager{traffic: tr}}
	seed := func(id, host, body string) {
		_, err := tr.DB().Exec("INSERT INTO exchanges(id,ts,host,method,url_template,url,status,content_type,req_len,resp_len,path) VALUES(?,1,?,'POST','/',?,200,'text/plain',0,?,'')", id, host, "https://"+host+"/", len(body))
		if err != nil {
			t.Fatal(err)
		}
		_, err = tr.DB().Exec("INSERT INTO exchange_bodies(id,req_head,resp_head,req_body,resp_body) VALUES(?,'POST / HTTP/1.1','HTTP/1.1 200 OK',?,?)", id, []byte{}, []byte(body))
		if err != nil {
			t.Fatal(err)
		}
	}
	seed("12345678-001", "evidence.invalid", "alice fixture-password")
	seed("12345678-002", "evil.invalid", "alice fixture-password")
	seed("12345678-003", "evidence.invalid", "bob fixture-password")
	item := &db.Intelligence{IntelligenceInput: db.IntelligenceInput{Content: "https://evidence.invalid/", Evidence: "12345678-001 12345678-002 12345678-003"}}
	bound, state, _ := s.matchCredentialTraffic(t.Context(), item, []db.CredentialAccount{{Username: "alice", Password: "fixture-password"}})
	if state != "ready" || len(bound) != 1 || bound[0].ID != "12345678-001" || len(bound[0].Accounts) != 1 || bound[0].Accounts[0] != 0 {
		t.Fatalf("flow trust boundary failed: state=%s count=%d", state, len(bound))
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	bound, state, _ = s.matchCredentialTraffic(ctx, item, []db.CredentialAccount{{Token: "fixture-password"}})
	if len(bound) != 0 || state != "error" {
		t.Fatal("cancelled matching read flow")
	}
	item.Content = ""
	bound, state, _ = s.matchCredentialTraffic(t.Context(), item, nil)
	if len(bound) != 0 || state != "unavailable" {
		t.Fatal("flow IDs alone created host scope")
	}
	s.m.traffic = nil
	if _, state, _ := s.matchCredentialTraffic(t.Context(), item, nil); state != "unavailable" {
		t.Fatal("missing traffic storage did not degrade")
	}
}

func TestCredentialRetryInvalidIDsAreNotCached(t *testing.T) {
	s := &Server{}
	for _, id := range []string{"0", "-1", "invalid"} {
		r := httptest.NewRequest("POST", "/", nil)
		r.SetPathValue("id", id)
		w := httptest.NewRecorder()
		s.retryCredentials(w, r)
		if w.Code != 400 || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("invalid retry accepted or cached")
		}
	}
}
