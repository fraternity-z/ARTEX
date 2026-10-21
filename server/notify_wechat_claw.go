package server

import (
	"crypto/rand"
	"net/http"
	"sync"
	"time"

	"github.com/Autumn-27/artex/notify"
)

type wechatClawPending struct {
	mu      sync.Mutex
	created time.Time
	login   *notify.WechatClawLogin
}

func (s *Server) notifyWechatClawStart(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	s.wechatClawMu.Lock()
	if s.wechatClawLogins == nil {
		s.wechatClawLogins = make(map[string]*wechatClawPending)
	}
	for id, pending := range s.wechatClawLogins {
		if time.Since(pending.created) >= 10*time.Minute {
			delete(s.wechatClawLogins, id)
		}
	}
	if len(s.wechatClawLogins) >= 32 {
		s.wechatClawMu.Unlock()
		writeErr(w, http.StatusTooManyRequests, "扫码会话过多，请稍后再试")
		return
	}
	id := rand.Text()
	pending := &wechatClawPending{created: time.Now()}
	pending.mu.Lock()
	s.wechatClawLogins[id] = pending
	s.wechatClawMu.Unlock()
	defer pending.mu.Unlock()
	login, content, err := notify.StartWechatClawLogin(r.Context())
	if err != nil {
		s.wechatClawMu.Lock()
		delete(s.wechatClawLogins, id)
		s.wechatClawMu.Unlock()
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	pending.login = login
	writeJSON(w, http.StatusOK, map[string]any{"login_id": id, "qr_content": content})
}

func (s *Server) notifyWechatClawPoll(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	id := r.PathValue("id")
	s.wechatClawMu.Lock()
	pending := s.wechatClawLogins[id]
	if pending != nil && time.Since(pending.created) >= 10*time.Minute {
		delete(s.wechatClawLogins, id)
		pending = nil
	}
	s.wechatClawMu.Unlock()
	if pending == nil {
		writeJSON(w, http.StatusOK, map[string]any{"status": "expired"})
		return
	}
	if !pending.mu.TryLock() {
		writeErr(w, http.StatusConflict, "正在查询扫码状态，请等待当前请求完成")
		return
	}
	defer pending.mu.Unlock()
	status, cfg, err := pending.login.Poll(r.Context())
	result := map[string]any{"status": status}
	if err != nil {
		result["message"] = err.Error()
		if notify.IsPermanent(err) {
			status, result["status"] = "failed", "failed"
		}
	}
	if cfg != nil {
		result["config"] = cfg
	}
	if status == "failed" || status == "expired" {
		s.wechatClawMu.Lock()
		delete(s.wechatClawLogins, id)
		s.wechatClawMu.Unlock()
	}
	writeJSON(w, http.StatusOK, result)
}
