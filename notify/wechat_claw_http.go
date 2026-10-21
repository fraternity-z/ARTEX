package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const wechatClawDefaultURL = "https://ilinkai.weixin.qq.com"

func wechatClawURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || (u.Port() != "" && u.Port() != "443") ||
		!(u.Hostname() == "weixin.qq.com" || strings.HasSuffix(u.Hostname(), ".weixin.qq.com")) {
		return nil, errors.New("微信 ClawBot 地址必须是微信官方 HTTPS 地址")
	}
	return u, nil
}

func wechatClawBaseURL(raw string) (string, error) {
	if raw == "" {
		raw = wechatClawDefaultURL
	}
	u, err := wechatClawURL(raw)
	if err != nil {
		return "", err
	}
	if (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return "", errors.New("微信 ClawBot 服务地址必须是 HTTPS 根地址")
	}
	return strings.TrimSuffix(u.String(), "/"), nil
}

type wechatClawError struct{ code int64 }

func (e *wechatClawError) Error() string {
	switch e.code {
	case -14:
		return "微信 ClawBot 登录已失效（-14），请重新扫码绑定"
	case -2:
		return "微信 ClawBot 无法准备投递（-2），请重新扫码并向 ClawBot 发送一条消息完成激活"
	default:
		return fmt.Sprintf("微信 ClawBot 返回错误 %d", e.code)
	}
}

// 独立设置长轮询超时及响应上限，复用通知传输层的拨号防护；错误不回显远端正文中的凭据。
func wechatClawRequest(ctx context.Context, base, endpoint, token string, payload any, poll bool) (map[string]any, error) {
	base, err := wechatClawBaseURL(base)
	if err != nil {
		return nil, Permanent(err)
	}
	var body io.Reader
	method := http.MethodGet
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, Permanent(errors.New("构造微信 ClawBot 请求失败"))
		}
		body, method = bytes.NewReader(raw), http.MethodPost
	}
	req, err := http.NewRequestWithContext(ctx, method, base+"/"+endpoint, body)
	if err != nil {
		return nil, Permanent(errors.New("微信 ClawBot 请求地址无效"))
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("AuthorizationType", "ilink_bot_token")
	req.Header.Set("X-WECHAT-UIN", wechatClawUIN())
	req.Header.Set("iLink-App-Id", "bot")
	req.Header.Set("iLink-App-ClientVersion", "65536")
	if token != "" {
		if err := wechatClawToken(token); err != nil {
			return nil, Permanent(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := *httpClient
	if poll {
		client.Timeout = 40 * time.Second
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.New("无法连接微信 ClawBot 服务，请检查网络后重试")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("微信 ClawBot 服务返回 HTTP %d", resp.StatusCode)
		if resp.StatusCode == 408 || resp.StatusCode == 429 || resp.StatusCode >= 500 {
			return nil, err
		}
		return nil, Permanent(err)
	}
	const limit = 1 << 20
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil || len(raw) > limit {
		return nil, errors.New("读取微信 ClawBot 响应失败或响应过大")
	}
	result := map[string]any{}
	if len(bytes.TrimSpace(raw)) != 0 {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if err := decoder.Decode(&result); err != nil || result == nil {
			return nil, errors.New("微信 ClawBot 返回了无效的 JSON 响应")
		}
		if decoder.Decode(new(any)) != io.EOF {
			return nil, errors.New("微信 ClawBot 返回了无效的 JSON 响应")
		}
	}
	for _, key := range []string{"ret", "errcode", "err_code"} {
		value, exists := result[key]
		if !exists {
			continue
		}
		code, err := strconv.ParseInt(strings.TrimSpace(fmt.Sprint(value)), 10, 64)
		if err != nil {
			return nil, errors.New("微信 ClawBot 返回了无效的业务状态")
		}
		if code != 0 {
			err := &wechatClawError{code: code}
			if code == 429 || code == -1 {
				return nil, err
			}
			return nil, Permanent(err)
		}
	}
	return result, nil
}
