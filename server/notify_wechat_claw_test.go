package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/notify"
)

func TestNotifyWechatClawSessionGuards(t *testing.T) {
	for _, state := range []string{"missing", "expired", "busy"} {
		t.Run(state, func(t *testing.T) {
			s := &Server{wechatClawLogins: map[string]*wechatClawPending{}}
			if state != "missing" {
				pending := &wechatClawPending{created: time.Now()}
				if state == "expired" {
					pending.created = time.Now().Add(-11 * time.Minute)
				}
				if state == "busy" {
					pending.mu.Lock()
					defer pending.mu.Unlock()
				}
				s.wechatClawLogins["test"] = pending
			}
			req := httptest.NewRequest("POST", "/api/notify/wechat-claw/login/test", nil)
			req.SetPathValue("id", "test")
			w := httptest.NewRecorder()
			s.notifyWechatClawPoll(w, req)
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("login response may be cached")
			}
			if state == "busy" {
				if w.Code != http.StatusConflict {
					t.Fatalf("concurrent poll: %d", w.Code)
				}
			} else if w.Code != 200 || !strings.Contains(w.Body.String(), "expired") || len(s.wechatClawLogins) != 0 {
				t.Fatalf("expired session not cleaned: %d %s", w.Code, w.Body)
			}
		})
	}
}

func TestNotifyWechatClawSessionLimit(t *testing.T) {
	s := &Server{wechatClawLogins: map[string]*wechatClawPending{}}
	for i := 0; i < 32; i++ {
		s.wechatClawLogins[fmt.Sprint(i)] = &wechatClawPending{created: time.Now()}
	}
	s.wechatClawLogins["expired"] = &wechatClawPending{created: time.Now().Add(-11 * time.Minute)}
	w := httptest.NewRecorder()
	s.notifyWechatClawStart(w, httptest.NewRequest("POST", "/api/notify/wechat-claw/login", nil))
	if w.Code != 429 || len(s.wechatClawLogins) != 32 {
		t.Fatalf("unbounded login sessions: %d", w.Code)
	}
}

func TestNotifyWechatClawRequiresAuthentication(t *testing.T) {
	s := &Server{}
	for _, path := range []string{"/api/notify/wechat-claw/login", "/api/notify/wechat-claw/login/test"} {
		w := httptest.NewRecorder()
		s.requireAuth(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("unauthenticated request reached login") })).ServeHTTP(w, httptest.NewRequest("POST", path, nil))
		if w.Code != 401 {
			t.Errorf("unauthenticated status: %d", w.Code)
		}
	}
}

func TestNotifyWechatClawDTOMasksBinding(t *testing.T) {
	cfg := map[string]any{"base_url": "https://ilinkai.weixin.qq.com", "bot_token": "fake-bot-secret", "context_token": "fake-context-secret", "get_updates_buf": "fake-cursor-secret", "chat_id": "recipient"}
	raw, _ := json.Marshal(cfg)
	dto := toNotifyChannelDTO(&db.NotificationChannel{Kind: notify.KindWechatClaw, Config: raw})
	for _, key := range []string{"bot_token", "context_token", "get_updates_buf"} {
		if !notify.IsMasked(dto.Config[key].(string)) {
			t.Errorf("%s exposed", key)
		}
	}
	merged, err := notify.PrepareConfigUpdate(notify.KindWechatClaw, cfg, dto.Config)
	if err != nil {
		t.Fatal(err)
	}
	ch, _ := notify.Get(notify.KindWechatClaw)
	if err := ch.Validate(merged); err != nil {
		t.Fatalf("round-trip binding invalid: %v", err)
	}
}
