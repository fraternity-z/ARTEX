package server

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/Autumn-27/artex/db"
)

func intelligenceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, db.ErrIntelligenceInvalid):
		writeErr(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, db.ErrIntelligenceNotFound):
		writeErr(w, http.StatusNotFound, "情报不存在或不属于当前任务")
	case errors.Is(err, db.ErrTaskArchiveState):
		writeErr(w, http.StatusConflict, "任务正在归档或恢复，暂时无法修改情报")
	default:
		writeErr(w, http.StatusInternalServerError, "任务情报存储操作失败")
	}
}

func (s *Server) intelligenceFilter(w http.ResponseWriter, r *http.Request) (db.IntelligenceFilter, bool) {
	f := db.IntelligenceFilter{}
	id, ok := pathInt(r, "id")
	if !ok || id <= 0 {
		writeErr(w, http.StatusBadRequest, "无效任务 ID")
		return f, false
	}
	if r.PathValue("scope") == "assets" {
		f.AssetID = id
	} else if scope := r.PathValue("scope"); scope == "" || scope == "tasks" {
		f.TaskID = id
	} else {
		writeErr(w, http.StatusBadRequest, "无效情报范围")
		return f, false
	}
	var exists bool
	var err error
	if f.TaskID > 0 {
		err = s.m.pg.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM tasks WHERE id=$1 AND deleted_at IS NULL AND archived_at IS NULL)`, id).Scan(&exists)
	} else {
		err = s.m.pg.QueryRowContext(r.Context(), "SELECT EXISTS(SELECT 1 FROM assets WHERE id=$1)", id).Scan(&exists)
	}
	if err != nil {
		intelligenceError(w, err)
		return f, false
	}
	if !exists {
		writeErr(w, http.StatusNotFound, "任务不存在")
		return f, false
	}
	query := r.URL.Query()
	f.Kind = query.Get("kind")
	f.Status = query.Get("status")
	f.Query = query.Get("q")
	f.Limit, _ = strconv.Atoi(query.Get("limit"))
	f.Offset, _ = strconv.Atoi(query.Get("offset"))
	if len(f.Query) > 500 {
		writeErr(w, http.StatusBadRequest, "搜索关键词过长")
		return f, false
	}
	return f, true
}

func (s *Server) listIntelligence(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	f, ok := s.intelligenceFilter(w, r)
	if !ok {
		return
	}
	page, err := s.m.pg.ListIntelligence(r.Context(), f)
	if err != nil {
		intelligenceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *Server) getIntelligence(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	f, ok := s.intelligenceFilter(w, r)
	if !ok {
		return
	}
	id, ok := pathInt(r, "intelID")
	if !ok || id <= 0 {
		writeErr(w, http.StatusBadRequest, "无效情报 ID")
		return
	}
	item, err := s.m.pg.GetIntelligence(r.Context(), id)
	if err != nil {
		intelligenceError(w, err)
		return
	}
	var allowed bool
	if f.TaskID > 0 {
		err = s.m.pg.QueryRowContext(r.Context(), `SELECT $1::bigint=$2::bigint OR EXISTS(SELECT 1 FROM task_relations WHERE task_id=$1 AND source_task_id=$2)`, f.TaskID, item.TaskID).Scan(&allowed)
	} else {
		for _, assetID := range item.AssetIDs {
			if assetID == f.AssetID {
				allowed = true
				break
			}
		}
	}
	if err != nil {
		intelligenceError(w, err)
		return
	}
	if !allowed {
		intelligenceError(w, db.ErrIntelligenceNotFound)
		return
	}
	item.Inherited = f.TaskID > 0 && item.TaskID != f.TaskID
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) updateIntelligence(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	taskID, ok := pathInt(r, "id")
	if !ok || taskID <= 0 {
		writeErr(w, http.StatusBadRequest, "无效任务 ID")
		return
	}
	id, ok := pathInt(r, "intelID")
	if !ok || id <= 0 {
		writeErr(w, http.StatusBadRequest, "无效情报 ID")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var request struct {
		Status string `json:"status"`
	}
	if err := decode(r, &request); err != nil {
		writeErr(w, http.StatusBadRequest, "无效请求")
		return
	}
	if err := s.m.pg.UpdateIntelligenceStatus(r.Context(), taskID, id, request.Status); err != nil {
		intelligenceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"updated": true})
}
