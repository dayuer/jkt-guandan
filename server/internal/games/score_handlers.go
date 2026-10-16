package games

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/dayuer/jkt-guandan/server/internal/scoring"
)

// 牌桌计分的处理函数（#165，§5.6）。响应都是 toScoreJSON：现在的全部事实和重算后的状态。

func (h *handler) score(w http.ResponseWriter, r *http.Request) {
	sc, err := h.svc.Score(r.Context(), r.PathValue("code"), viewer(r))
	if err != nil {
		writeGameError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toScoreJSON(sc))
}

func (h *handler) startScore(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	// 请求体没有参数（#205）。早先客户端带的 `a_fail_limit` 落在未知字段里，忽略不报错；
	// 只有不是合法 JSON 才拒绝。空请求体也照收（客户端可以什么都不发）。
	var body struct{}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "bad_request", "请求体不是合法的 JSON")
		return
	}
	sc, err := h.svc.StartScore(r.Context(), r.PathValue("code"), userID, r.Header.Get("Idempotency-Key"))
	if err != nil {
		writeGameError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toScoreJSON(sc))
}

func (h *handler) recordRound(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	var body struct {
		ExpectedRoundNo int `json:"expected_round_no"`
		scoring.Round
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "请求体不是合法的 JSON")
		return
	}
	sc, err := h.svc.RecordRound(r.Context(), r.PathValue("code"), userID, body.ExpectedRoundNo,
		body.Round, r.Header.Get("Idempotency-Key"))
	if err != nil {
		writeGameError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toScoreJSON(sc))
}

func (h *handler) editRound(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	no, err := parseRoundNo(r.PathValue("no"))
	if err != nil {
		writeGameError(w, err)
		return
	}
	var body struct {
		scoring.Round
		// Was 是打开「改一局」时看到的原结果，带上就能发现别人刚改过（409 round_changed）。
		Was *scoring.Round `json:"was"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "请求体不是合法的 JSON")
		return
	}
	sc, err := h.svc.EditRound(r.Context(), r.PathValue("code"), userID, no, body.Round, body.Was)
	if err != nil {
		writeGameError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toScoreJSON(sc))
}
