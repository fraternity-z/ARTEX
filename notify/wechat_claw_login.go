package notify

import (
	"context"
	"errors"
	"maps"
	"net/url"
)

// WechatClawLogin 保存一次临时扫码状态；调用方负责过期清理并串行调用 Poll。
type WechatClawLogin struct {
	base, qr, token, recipient, cursor string
	started                            bool
	config                             map[string]any
}

// StartWechatClawLogin 返回临时会话及待编码为二维码的微信链接。
func StartWechatClawLogin(ctx context.Context) (*WechatClawLogin, string, error) {
	res, err := wechatClawRequest(ctx, wechatClawDefaultURL, "ilink/bot/get_bot_qrcode?bot_type=3", "", map[string]any{"local_token_list": []string{}}, false)
	if err != nil {
		return nil, "", err
	}
	qr, content := cfgString(res, "qrcode"), cfgString(res, "qrcode_img_content")
	if qr == "" || len(qr) > 4096 || len(content) > 2048 {
		return nil, "", errors.New("微信 ClawBot 没有返回有效二维码，请重新扫码")
	}
	if _, err := wechatClawURL(content); err != nil {
		return nil, "", err
	}
	return &WechatClawLogin{base: wechatClawDefaultURL, qr: qr}, content, nil
}

// Poll 等待扫码或激活消息。只有完成激活才返回可保存的渠道配置。
func (l *WechatClawLogin) Poll(ctx context.Context) (string, map[string]any, error) {
	if l.config != nil {
		return "confirmed", maps.Clone(l.config), nil
	}
	if l.token == "" {
		res, err := wechatClawRequest(ctx, l.base, "ilink/bot/get_qrcode_status?qrcode="+url.QueryEscape(l.qr), "", nil, true)
		if err != nil {
			return "wait", nil, err
		}
		switch cfgString(res, "status") {
		case "wait":
			return "wait", nil, nil
		case "scaned", "scanned":
			return "scanned", nil, nil
		case "scaned_but_redirect":
			base, err := wechatClawBaseURL("https://" + cfgString(res, "redirect_host"))
			if err != nil {
				return "failed", nil, Permanent(err)
			}
			l.base = base
			return "scanned", nil, nil
		case "confirmed":
			token := cfgString(res, "bot_token")
			if err := wechatClawToken(token); err != nil {
				return "failed", nil, Permanent(err)
			}
			if base := cfgString(res, "baseurl"); base != "" {
				validated, err := wechatClawBaseURL(base)
				if err != nil {
					return "failed", nil, Permanent(err)
				}
				l.base = validated
			}
			l.token, l.recipient = token, cfgString(res, "ilink_user_id")
			return "activating", nil, nil
		case "expired":
			return "expired", nil, nil
		default:
			return "failed", nil, Permanent(errors.New("微信 ClawBot 登录未完成，请重新扫码"))
		}
	}
	if !l.started {
		if _, err := wechatClawRequest(ctx, l.base, "ilink/bot/msg/notifystart", l.token, map[string]any{"base_info": wechatClawBaseInfo()}, false); err != nil {
			return "activating", nil, err
		}
		l.started = true
	}
	res, err := wechatClawRequest(ctx, l.base, "ilink/bot/getupdates", l.token, map[string]any{
		"get_updates_buf": l.cursor, "base_info": wechatClawBaseInfo(),
	}, true)
	if err != nil {
		return "activating", nil, err
	}
	if cursor := wechatClawResponseString(res, "get_updates_buf", "sync_buf"); cursor != "" {
		l.cursor = cursor
	}
	recipient, token := wechatClawContext(res, l.recipient)
	if token == "" {
		return "activating", nil, nil
	}
	l.config = map[string]any{"base_url": l.base, "bot_token": l.token, "chat_id": recipient, "context_token": token, "get_updates_buf": l.cursor}
	return "confirmed", maps.Clone(l.config), nil
}

func wechatClawResponseString(res map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := cfgString(res, key); value != "" {
			return value
		}
	}
	for _, key := range []string{"data", "result", "body", "payload"} {
		if nested, ok := res[key].(map[string]any); ok {
			if value := wechatClawResponseString(nested, keys...); value != "" {
				return value
			}
		}
	}
	return ""
}

func wechatClawContext(res map[string]any, expected string) (string, string) {
	for _, key := range []string{"msgs", "messages", "message_list", "updates"} {
		messages, _ := res[key].([]any)
		// 同一批可能含多条用户消息，取最后一条的最新上下文。
		for i := len(messages) - 1; i >= 0; i-- {
			msg, _ := messages[i].(map[string]any)
			from, token := cfgString(msg, "from_user_id"), cfgString(msg, "context_token")
			if nested, ok := msg["msg"].(map[string]any); ok {
				if from == "" {
					from = cfgString(nested, "from_user_id")
				}
				if token == "" {
					token = cfgString(nested, "context_token")
				}
			}
			if from != "" && token != "" && (expected == "" || from == expected) {
				return from, token
			}
		}
	}
	for _, key := range []string{"data", "result", "body", "payload"} {
		if nested, ok := res[key].(map[string]any); ok {
			if from, token := wechatClawContext(nested, expected); token != "" {
				return from, token
			}
		}
	}
	return "", ""
}
