package notify

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

type wechatClawRoundTrip func(*http.Request) (*http.Response, error)

func (f wechatClawRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func mockWechatClaw(t *testing.T, handler func(*http.Request) (int, string)) {
	t.Helper()
	previous := httpClient
	httpClient = &http.Client{Transport: wechatClawRoundTrip(func(r *http.Request) (*http.Response, error) {
		status, body := handler(r)
		return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}
	t.Cleanup(func() { httpClient = previous })
}

func wechatClawConfig() map[string]any {
	return map[string]any{"bot_token": "test-bot-secret", "context_token": "test-context-secret", "get_updates_buf": "test-cursor", "chat_id": "recipient@im.wechat", "base_url": wechatClawDefaultURL}
}

func TestWechatClawValidationAndMasking(t *testing.T) {
	ch, ok := Get(KindWechatClaw)
	if !ok || ch.DefaultRatePerMin() != 20 {
		t.Fatal("missing channel metadata")
	}
	cfg := wechatClawConfig()
	if err := ch.Validate(cfg); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"bot_token", "context_token", "chat_id"} {
		bad := maps.Clone(cfg)
		bad[key] = ""
		if err := ch.Validate(bad); err == nil {
			t.Errorf("missing %s accepted", key)
		}
		if _, err := ch.Send(context.Background(), bad, singleMsg()); !IsPermanent(err) {
			t.Errorf("invalid config should be permanent: %v", err)
		}
	}
	for _, token := range []string{"line\nbreak", "令牌", MaskedPrefix} {
		bad := maps.Clone(cfg)
		bad["bot_token"] = token
		if ch.Validate(bad) == nil {
			t.Errorf("bad token accepted")
		}
	}
	masked := MaskConfig(KindWechatClaw, cfg)
	for _, key := range ch.SecretKeys() {
		if !IsMasked(masked[key].(string)) {
			t.Errorf("%s unmasked", key)
		}
	}
	merged, err := PrepareConfigUpdate(KindWechatClaw, cfg, masked)
	if err != nil || !reflect.DeepEqual(merged, cfg) {
		t.Fatalf("unchanged save lost credentials: %v", err)
	}
	for _, key := range ch.DestinationKeys() {
		changed := maps.Clone(masked)
		changed[key] = "changed"
		if _, err := PrepareConfigUpdate(KindWechatClaw, cfg, changed); err == nil {
			t.Errorf("changing %s retained secrets", key)
		}
	}
	for _, raw := range []string{"", wechatClawDefaultURL, "https://region.weixin.qq.com/"} {
		if _, err := wechatClawBaseURL(raw); err != nil {
			t.Errorf("valid base: %v", err)
		}
	}
	for _, raw := range []string{"http://ilinkai.weixin.qq.com", "https://weixin.qq.com.evil.test", "https://evilweixin.qq.com", "https://user@ilinkai.weixin.qq.com", "https://ilinkai.weixin.qq.com:8443", "https://ilinkai.weixin.qq.com/path", "https://ilinkai.weixin.qq.com?", "https://ilinkai.weixin.qq.com#x", "http://127.0.0.1", "%"} {
		bad := maps.Clone(cfg)
		bad["base_url"] = raw
		if ch.Validate(bad) == nil {
			t.Errorf("unsafe base accepted: %s", raw)
		}
	}
}

func TestWechatClawSendProtocolAndContextRecovery(t *testing.T) {
	var paths, ids []string
	mockWechatClaw(t, func(r *http.Request) (int, string) {
		paths = append(paths, r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer test-bot-secret" || r.Header.Get("AuthorizationType") != "ilink_bot_token" || r.Header.Get("iLink-App-Id") != "bot" || r.Header.Get("iLink-App-ClientVersion") != "65536" {
			t.Error("missing iLink headers")
		}
		if _, err := base64.StdEncoding.DecodeString(r.Header.Get("X-WECHAT-UIN")); err != nil {
			t.Error("invalid UIN")
		}
		if r.Method != "POST" {
			t.Errorf("method: %s", r.Method)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["base_info"] == nil {
			t.Error("base_info missing")
		}
		switch r.URL.Path {
		case "/ilink/bot/msg/notifystart":
			return 200, `{"ret":0}`
		case "/ilink/bot/getupdates":
			if payload["get_updates_buf"] != "test-cursor" {
				t.Error("cursor lost")
			}
			return 200, `{"msgs":[{"from_user_id":"recipient@im.wechat","context_token":"fresh"},{"from_user_id":"other","context_token":"wrong"}]}`
		case "/ilink/bot/sendmessage":
			msg := payload["msg"].(map[string]any)
			ids = append(ids, msg["client_id"].(string))
			if msg["to_user_id"] != "recipient@im.wechat" || msg["message_type"] != float64(2) || msg["message_state"] != float64(2) {
				t.Error("incorrect recipient/type/state")
			}
			items := msg["item_list"].([]any)
			text := items[0].(map[string]any)["text_item"].(map[string]any)["text"].(string)
			if !strings.Contains(text, "SQL注入") || !strings.Contains(text, "https://artex.local/") {
				t.Error("missing notification content")
			}
			if msg["context_token"] == "test-context-secret" {
				return 200, `{"ret":-2,"errmsg":"prepare failed"}`
			}
			if msg["context_token"] != "fresh" {
				t.Error("context for wrong recipient")
			}
			return 200, `{}`
		default:
			t.Fatalf("unexpected endpoint %s", r.URL.Path)
			return 500, ""
		}
	})
	cfg := wechatClawConfig()
	kept, err := (wechatClawChannel{}).Send(context.Background(), cfg, singleMsg())
	if err != nil || kept != 1 {
		t.Fatalf("kept=%d err=%v", kept, err)
	}
	if len(paths) != 4 || len(ids) != 2 || ids[0] != ids[1] {
		t.Fatalf("unexpected recovery requests: %v", paths)
	}
	if cfg["context_token"] != "test-context-secret" {
		t.Fatal("Send mutated shared config")
	}
}

func TestWechatClawTextPacksCompleteItems(t *testing.T) {
	m := batchMsg(100)
	text, kept := wechatClawText(m)
	if kept <= 0 || kept >= len(m.Items) || runeSize(text) > 1800 {
		t.Fatalf("kept=%d size=%d", kept, runeSize(text))
	}
	for i := 0; i < kept; i++ {
		if !strings.Contains(text, fmt.Sprintf("%d. ", i+1)) {
			t.Fatalf("counted missing item %d", i)
		}
	}
	if !strings.Contains(text, "后续消息") || !strings.Contains(text, m.HomeURL) {
		t.Fatal("missing remainder or home link")
	}
	m = singleMsg()
	m.Items[0].Name = strings.Repeat("长", 3000)
	m.Items[0].FromStatus, m.Items[0].ToStatus = "pending", "fixed"
	text, kept = wechatClawText(m)
	if kept != 1 || runeSize(text) > 1800 || !strings.Contains(text, "待处理 → 已修复") {
		t.Fatal("invalid single status notification")
	}
	if _, kept := wechatClawText(Message{}); kept != 0 {
		t.Error("empty message counted")
	}
}

func TestWechatClawResponseErrors(t *testing.T) {
	for _, tc := range []struct {
		status            int
		body              string
		failed, permanent bool
	}{
		{200, "", false, false}, {200, `{}`, false, false}, {200, `{"ret":"0"}`, false, false},
		{200, `{"ret":0,"errcode":"-14","errmsg":"test-bot-secret"}`, true, true},
		{200, `{"err_code":-2}`, true, true}, {200, `{"ret":429}`, true, false}, {200, `{"ret":-1}`, true, false},
		{200, `null`, true, false}, {200, `[]`, true, false}, {200, `{"ret":null}`, true, false},
		{200, `test-bot-secret`, true, false}, {200, `{} {}`, true, false},
		{401, "test-bot-secret", true, true}, {302, "test-bot-secret", true, true},
		{408, "test-bot-secret", true, false}, {429, "test-bot-secret", true, false}, {503, "test-bot-secret", true, false},
	} {
		t.Run(fmt.Sprintf("%d_%s", tc.status, tc.body), func(t *testing.T) {
			mockWechatClaw(t, func(*http.Request) (int, string) { return tc.status, tc.body })
			_, err := wechatClawRequest(context.Background(), "", "ilink/bot/sendmessage", "test-bot-secret", map[string]any{}, false)
			if (err != nil) != tc.failed || IsPermanent(err) != tc.permanent {
				t.Fatalf("unexpected result: %v", err)
			}
			if err != nil && strings.Contains(err.Error(), "test-bot-secret") {
				t.Fatal("credential leaked")
			}
		})
	}
	t.Run("response limit", func(t *testing.T) {
		mockWechatClaw(t, func(*http.Request) (int, string) { return 200, strings.Repeat(" ", (1<<20)+1) })
		if _, err := wechatClawRequest(context.Background(), "", "test", "", nil, false); err == nil {
			t.Fatal("oversize response accepted")
		}
	})
	t.Run("transport error redacted", func(t *testing.T) {
		previous := httpClient
		httpClient = &http.Client{Transport: wechatClawRoundTrip(func(*http.Request) (*http.Response, error) { return nil, errors.New("test-bot-secret") })}
		t.Cleanup(func() { httpClient = previous })
		_, err := wechatClawRequest(context.Background(), "", "test", "", nil, false)
		if err == nil || strings.Contains(err.Error(), "test-bot-secret") {
			t.Fatal("unsafe transport error")
		}
	})
}

func TestWechatClawLoginFlow(t *testing.T) {
	step := 0
	responses := []string{
		`{"qrcode":"opaque + token","qrcode_img_content":"https://weixin.qq.com/scan?id=qr"}`,
		`{"status":"wait"}`, `{"status":"scaned_but_redirect","redirect_host":"region.weixin.qq.com"}`,
		`{"status":"scaned"}`, `{"status":"confirmed","bot_token":"test-bot-secret","ilink_user_id":"recipient@im.wechat","baseurl":"https://region.weixin.qq.com"}`,
		`{"ret":0}`, `{"get_updates_buf":"cursor-1","msgs":[{"from_user_id":"other","context_token":"wrong"}]}`,
		`{"data":{"sync_buf":"cursor-2","messages":[{"msg":{"from_user_id":"recipient@im.wechat","context_token":"old"}},{"msg":{"from_user_id":"recipient@im.wechat","context_token":"fresh"}}]}}`,
	}
	mockWechatClaw(t, func(r *http.Request) (int, string) {
		if step == 0 {
			if r.Method != "POST" || r.URL.Query().Get("bot_type") != "3" || r.Header.Get("Authorization") != "" {
				t.Error("incorrect login request")
			}
		} else if step < 5 {
			if r.Method != "GET" || r.URL.Query().Get("qrcode") != "opaque + token" {
				t.Error("incorrect QR poll")
			}
		} else if step > 5 {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if step == 7 && body["get_updates_buf"] != "cursor-1" {
				t.Error("cursor not advanced")
			}
		}
		if step >= 3 && r.URL.Host != "region.weixin.qq.com" {
			t.Error("regional redirect ignored")
		}
		if step >= len(responses) {
			t.Fatal("unexpected extra request")
		}
		body := responses[step]
		step++
		return 200, body
	})
	login, qr, err := StartWechatClawLogin(context.Background())
	if err != nil || !strings.Contains(qr, "/scan") {
		t.Fatalf("start: %v", err)
	}
	for _, want := range []string{"wait", "scanned", "scanned", "activating", "activating", "confirmed", "confirmed"} {
		status, cfg, err := login.Poll(context.Background())
		if err != nil || status != want {
			t.Fatalf("want=%s got=%s err=%v", want, status, err)
		}
		if want != "confirmed" && cfg != nil {
			t.Fatal("credentials returned before activation")
		}
		if want == "confirmed" {
			if cfg["context_token"] != "fresh" || cfg["chat_id"] != "recipient@im.wechat" || cfg["get_updates_buf"] != "cursor-2" {
				t.Fatal("incorrect binding")
			}
			cfg["context_token"] = "caller mutation"
		}
	}
}

func TestWechatClawLoginRejectsUnsafeResponses(t *testing.T) {
	for _, body := range []string{`{}`, `{"qrcode":"qr","qrcode_img_content":"https://evil.test/scan"}`, `{"ret":-14}`} {
		t.Run(body, func(t *testing.T) {
			mockWechatClaw(t, func(*http.Request) (int, string) { return 200, body })
			if _, _, err := StartWechatClawLogin(context.Background()); err == nil {
				t.Error("invalid QR accepted")
			}
		})
	}
	for _, tc := range []struct {
		body, status string
		failed       bool
	}{
		{`{"status":"expired"}`, "expired", false}, {`{"status":"unknown"}`, "failed", true},
		{`{"status":"confirmed"}`, "failed", true},
		{`{"status":"confirmed","bot_token":"token","baseurl":"https://evil.test"}`, "failed", true},
		{`{"status":"scaned_but_redirect","redirect_host":"evil.test"}`, "failed", true},
	} {
		t.Run(tc.body, func(t *testing.T) {
			mockWechatClaw(t, func(*http.Request) (int, string) { return 200, tc.body })
			login := &WechatClawLogin{base: wechatClawDefaultURL, qr: "qr"}
			status, cfg, err := login.Poll(context.Background())
			if status != tc.status || cfg != nil || (err != nil) != tc.failed {
				t.Fatalf("status=%s err=%v", status, err)
			}
		})
	}
}
