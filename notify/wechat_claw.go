package notify

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"time"
)

type wechatClawChannel struct{}

func (wechatClawChannel) Kind() string { return KindWechatClaw }

// 取保守默认值，避免个人会话被连续通知刷屏。
func (wechatClawChannel) DefaultRatePerMin() int { return 20 }
func (wechatClawChannel) SecretKeys() []string {
	return []string{"bot_token", "context_token", "get_updates_buf"}
}
func (wechatClawChannel) DestinationKeys() []string { return []string{"base_url", "chat_id"} }

func (wechatClawChannel) Validate(cfg map[string]any) error {
	if err := wechatClawToken(cfgString(cfg, "bot_token")); err != nil {
		return err
	}
	if token := cfgString(cfg, "context_token"); token == "" || IsMasked(token) {
		return errors.New("请先在微信中向 ClawBot 发送一条消息完成激活")
	}
	if cfgString(cfg, "chat_id") == "" {
		return errors.New("缺少接收通知的 iLink 用户 ID，请重新扫码绑定")
	}
	_, err := wechatClawBaseURL(cfgString(cfg, "base_url"))
	return err
}

func (c wechatClawChannel) Send(ctx context.Context, cfg map[string]any, m Message) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := c.Validate(cfg); err != nil {
		return 0, Permanent(err)
	}
	base, _ := wechatClawBaseURL(cfgString(cfg, "base_url"))
	token := cfgString(cfg, "bot_token")
	if _, err := wechatClawRequest(ctx, base, "ilink/bot/msg/notifystart", token, map[string]any{"base_info": wechatClawBaseInfo()}, false); err != nil {
		return 0, err
	}
	text, kept := wechatClawText(m)
	msg := map[string]any{
		"from_user_id": "", "to_user_id": cfgString(cfg, "chat_id"),
		"context_token": cfgString(cfg, "context_token"), "client_id": "artex:" + rand.Text(),
		"message_type": 2, "message_state": 2,
		"item_list": []any{map[string]any{"type": 1, "text_item": map[string]any{"text": text}}},
	}
	body := map[string]any{"msg": msg, "base_info": wechatClawBaseInfo()}
	_, err := wechatClawRequest(ctx, base, "ilink/bot/sendmessage", token, body, false)
	var businessErr *wechatClawError
	if errors.As(err, &businessErr) && businessErr.code == -2 {
		// 只在明确拒收时刷新上下文；网络超时后立即重发可能造成重复通知。
		// shortcut: 按需读取一批消息刷新上下文；需要长期后台同步时再持久化游标。
		updates, refreshErr := wechatClawRequest(ctx, base, "ilink/bot/getupdates", token, map[string]any{
			"get_updates_buf": cfgString(cfg, "get_updates_buf"), "base_info": wechatClawBaseInfo(),
		}, false)
		if refreshErr != nil {
			return 0, refreshErr
		}
		_, fresh := wechatClawContext(updates, cfgString(cfg, "chat_id"))
		if fresh != "" && fresh != msg["context_token"] {
			msg["context_token"] = fresh
			_, err = wechatClawRequest(ctx, base, "ilink/bot/sendmessage", token, body, false)
		}
	}
	if err != nil {
		return 0, err
	}
	return kept, nil
}

func wechatClawText(m Message) (string, int) {
	const limit = 1800
	render := func(it Item, idx int) string {
		text := fmt.Sprintf("%d. %s · %s\n", idx+1, SeverityLabel(it.Severity), OneLine(it.Title(), 160))
		if it.IsStatusChange() {
			text += "状态变更：" + StatusLabel(it.FromStatus) + " → " + StatusLabel(it.ToStatus) + "\n"
		}
		if it.VulnClass != "" {
			text += "类型：" + OneLine(it.VulnClass, 80) + "\n"
		}
		if assets := assetLine(it.Assets, maxAssetsShown); assets != "" {
			text += "资产：" + OneLine(assets, 200) + "\n"
		}
		if it.Summary != "" {
			text += "摘要：" + OneLine(it.Summary, maxSummaryRunes) + "\n"
		}
		if it.DetailURL != "" {
			text += it.DetailURL + "\n"
		}
		return text + "\n"
	}
	footer := ""
	if m.Batch && m.HomeURL != "" {
		footer = "查看全部：" + m.HomeURL
	}
	kept := packItemCount(m.Items, limit, 160, footer, runeSize, render)
	var b strings.Builder
	b.WriteString("ARTEX 漏洞通知\n")
	if m.Batch {
		fmt.Fprintf(&b, "近 %d 分钟 · 本条 %d / %d 条\n", m.WindowMinutes, kept, len(m.Items))
		if kept < len(m.Items) {
			b.WriteString("其余条目将在后续消息继续发送\n")
		}
	}
	for i, it := range m.Items[:kept] {
		b.WriteString(render(it, i))
	}
	b.WriteString(footer)
	return TruncateRunes(b.String(), limit), kept
}

func wechatClawToken(token string) error {
	if token == "" || IsMasked(token) {
		return errors.New("请先扫码登录微信 ClawBot")
	}
	for _, ch := range token {
		if ch < 33 || ch > 126 {
			return errors.New("微信 ClawBot 令牌包含非法字符，请重新扫码")
		}
	}
	return nil
}

func wechatClawBaseInfo() map[string]string {
	return map[string]string{"channel_version": "1.0.0", "bot_agent": "ARTEX/1.0.0"}
}

func wechatClawUIN() string {
	var buf [4]byte
	_, _ = rand.Read(buf[:])
	return base64.StdEncoding.EncodeToString([]byte(fmt.Sprint(binary.BigEndian.Uint32(buf[:]))))
}
