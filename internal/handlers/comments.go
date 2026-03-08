package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/shnupta/workflow/internal/models"
)

func (h *Handler) registerCommentRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/tasks/{id}/comments", h.apiListComments)
	mux.HandleFunc("POST /api/tasks/{id}/comments", h.apiCreateComment)
	mux.HandleFunc("DELETE /api/comments/{id}", h.apiDeleteComment)
}

func (h *Handler) apiListComments(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("id")
	comments, err := h.db.ListComments(taskID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Always return an array, never null.
	out := make([]*commentResponse, len(comments))
	for i, c := range comments {
		out[i] = toCommentResponse(c)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

func (h *Handler) apiCreateComment(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("id")
	var body struct {
		Body string `json:"body"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	body.Body = strings.TrimSpace(body.Body)
	if body.Body == "" {
		http.Error(w, "body is required", http.StatusBadRequest)
		return
	}
	c, err := h.db.CreateComment(taskID, body.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(toCommentResponse(c))
}

func (h *Handler) apiDeleteComment(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid comment id", http.StatusBadRequest)
		return
	}
	if err := h.db.DeleteComment(id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// commentResponse is the JSON shape sent to the client. We include
// formatted_time alongside the raw created_at so the template and JS don't
// have to reformat it.
type commentResponse struct {
	ID            int64  `json:"id"`
	TaskID        string `json:"task_id"`
	Body          string `json:"body"`
	CreatedAt     string `json:"created_at"`
	FormattedTime string `json:"formatted_time"`
}

func toCommentResponse(c *models.Comment) *commentResponse {
	return &commentResponse{
		ID:            c.ID,
		TaskID:        c.TaskID,
		Body:          c.Body,
		CreatedAt:     c.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		FormattedTime: c.FormattedTime(),
	}
}
