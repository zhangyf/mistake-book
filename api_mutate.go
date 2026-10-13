package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"time"
)

// ============================================================================
// 改 / 删
//
// exam-bank/index.json 是单一共享对象，objstore 的 PutObject 没有 If-Match，
// 做不了真正的条件写。所以并发保护分两层：
//
//  1. 进程内 mutex —— 服务器是单 systemd 实例，串行化后绝大多数冲突就没了；
//  2. 写后校验 + 重试 —— Put 完重新读一次，确认改动真的在上面；
//     不在说明这期间被别的端（比如老师在本机跑 CLI index-add）覆盖过，重放。
//
// 做不到强一致（check-then-act 有 TOCTOU 窗口），但不会静默丢更新：
// 三次重放仍对不上就返回 409，前端提示重试。
// ============================================================================

var indexMu sync.Mutex

var errConflict = errors.New("索引在写入期间被其他端修改，请重试")

// mutate 在锁内对索引做一次改动并落盘，带写后校验与重试。
// verify 返回 nil 表示「改动确实生效了」。
func (s *server) mutate(apply func(idx *Index) error, verify func(idx *Index) bool) error {
	indexMu.Lock()
	defer indexMu.Unlock()

	const attempts = 3
	for i := 0; i < attempts; i++ {
		idx, err := s.st.GetIndex(bgctx())
		if err != nil {
			return err
		}
		if err := apply(idx); err != nil {
			return err // 业务错误（比如 id 不存在），不重试
		}
		if s.dryRun {
			return nil
		}
		if err := s.st.PutIndex(bgctx(), idx); err != nil {
			return err
		}
		// 写后校验
		after, err := s.st.GetIndex(bgctx())
		if err != nil {
			return err
		}
		if verify(after) {
			return nil
		}
		time.Sleep(150 * time.Millisecond)
	}
	return errConflict
}

type updateReq struct {
	ID     string   `json:"id"`
	Result string   `json:"result"`
	Exam   string   `json:"exam"`
	Source string   `json:"source"`
	Tags   []string `json:"tags"`
	Note   string   `json:"note"`
}

// PUT /api/entry  body: {"id":..., "result":"wrong", "tags":[...], "note":"..."}
func (s *server) updateEntry(w http.ResponseWriter, r *http.Request) {
	// body 读一次：既要解析成结构体，也要知道 note 键是否出现（空串=清空备注）
	body, err := io.ReadAll(r.Body)
	if err != nil {
		failJSON(w, 400, "读取请求体失败: "+err.Error())
		return
	}
	var raw map[string]json.RawMessage
	_ = json.Unmarshal(body, &raw)

	var req updateReq
	if err := json.Unmarshal(body, &req); err != nil {
		failJSON(w, 400, "请求体解析失败: "+err.Error())
		return
	}
	_, hasNote := raw["note"]
	_, hasExam := raw["exam"]
	_, hasSource := raw["source"]

	switch req.Source {
	case "", "校内作业", "课外班作业", "校内考试", "自我复习":
	default:
		failJSON(w, 400, "source 只能是：校内作业/课外班作业/校内考试/自我复习")
		return
	}
	if req.ID == "" {
		failJSON(w, 400, "缺少 id")
		return
	}
	if req.Result != "" {
		switch req.Result {
		case "correct", "wrong", "unknown":
		default:
			failJSON(w, 400, "result 只能是 correct/wrong/unknown")
			return
		}
	}

	now := time.Now().Format("2006-01-02 15:04:05")
	var notFound bool

	err = s.mutate(
		func(idx *Index) error {
			for i := range idx.Items {
				if idx.Items[i].ID != req.ID {
					continue
				}
				if req.Result != "" {
					idx.Items[i].Result = req.Result
				}
				if hasExam {
					idx.Items[i].Exam = req.Exam // 允许清空后重填
				}
				if hasSource {
					idx.Items[i].Source = req.Source
				}
				if req.Tags != nil {
					idx.Items[i].Tags = compactTags(req.Tags)
				}
				if req.Note != "" || hasNote {
					idx.Items[i].Note = req.Note
				}
				idx.Items[i].UpdatedAt = now
				notFound = false
				return nil
			}
			notFound = true
			return nil
		},
		func(idx *Index) bool {
			for _, it := range idx.Items {
				if it.ID != req.ID {
					continue
				}
				if req.Result != "" && it.Result != req.Result {
					return false
				}
				if hasExam && it.Exam != req.Exam {
					return false
				}
				if hasSource && it.Source != req.Source {
					return false
				}
				if req.Tags != nil && !reflect.DeepEqual(it.Tags, compactTags(req.Tags)) {
					return false
				}
				return true
			}
			return false
		},
	)
	if notFound {
		failJSON(w, 404, "索引里没有 id="+req.ID)
		return
	}
	if err != nil {
		writeConflict(w, err)
		return
	}
	writeJSON(w, 200, map[string]interface{}{
		"success": true, "id": req.ID, "updated_at": now, "dry_run": s.dryRun,
	})
}

// DELETE /api/entry?id=...
//
// 只删索引条目，md 和原图保留在 COS（要彻底清理另说，删数据不可逆）。
func (s *server) deleteEntry(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		failJSON(w, 400, "缺少 id")
		return
	}

	var removed bool
	err := s.mutate(
		func(idx *Index) error {
			kept := make([]Entry, 0, len(idx.Items))
			for _, it := range idx.Items {
				if it.ID == id {
					removed = true
					continue
				}
				kept = append(kept, it)
			}
			idx.Items = kept
			return nil
		},
		func(idx *Index) bool {
			for _, it := range idx.Items {
				if it.ID == id {
					return false
				}
			}
			return true
		},
	)
	if !removed {
		failJSON(w, 404, "索引里没有 id="+id)
		return
	}
	if err != nil {
		writeConflict(w, err)
		return
	}
	writeJSON(w, 200, map[string]interface{}{
		"success": true, "removed": id, "dry_run": s.dryRun,
	})
}

func writeConflict(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	if errors.Is(err, errConflict) {
		status = http.StatusConflict
	}
	failJSON(w, status, err.Error())
}

func compactTags(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, t := range in {
		t = trimSpace(t)
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

func trimSpace(s string) string { return strings.TrimSpace(s) }
