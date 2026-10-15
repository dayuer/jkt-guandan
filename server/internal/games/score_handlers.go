package games

import (
	"encoding/json"
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
	var body struct {
		AFailLimit *int `json:"a_fail_limit"` // 3（默认）或 0（不回 2）
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "请求体不是合法的 JSON")
		return
	}
	limit := scoring.DefaultAFailLimit
	if body.AFailLimit != nil {
		limit = *body.AFailLimit
	}
	sc, err := h.svc.StartScore(r.Context(), r.PathValue("code"), userID, limit, r.Header.Get("Idempotency-Key"))
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
