package handlers

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/shnupta/workflow/internal/models"
)

func (h *Handler) registerNoteRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/notes", h.apiListNotes)
	mux.HandleFunc("POST /api/notes", h.apiCreateNote)
	mux.HandleFunc("GET /api/notes/{id}", h.apiGetNote)
	mux.HandleFunc("PATCH /api/notes/{id}", h.apiUpdateNote)
	mux.HandleFunc("DELETE /api/notes/{id}", h.apiDeleteNote)
}

func (h *Handler) apiListNotes(w http.ResponseWriter, r *http.Request) {
	taskID := r.URL.Query().Get("task_id")
	notes, err := h.db.ListNotes(taskID)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if notes == nil {
		notes = []*models.Note{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(notes)
}

func (h *Handler) apiCreateNote(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TaskID  string `json:"task_id"`
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", 400)
		return
	}
	n := &models.Note{
		TaskID:  body.TaskID,
		Content: body.Content,
		Title:   deriveTitleFromContent(body.Content),
	}
	if err := h.db.CreateNote(n); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(n)
}

func (h *Handler) apiGetNote(w http.ResponseWriter, r *http.Request) {
	n, err := h.db.GetNote(r.PathValue("id"))
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(n)
}

func (h *Handler) apiUpdateNote(w http.ResponseWriter, r *http.Request) {
	n, err := h.db.GetNote(r.PathValue("id"))
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	var body struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", 400)
		return
	}
	n.Content = body.Content
	n.Title = deriveTitleFromContent(body.Content)
	if err := h.db.UpdateNote(n); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(n)
}

func (h *Handler) apiDeleteNote(w http.ResponseWriter, r *http.Request) {
	if err := h.db.DeleteNote(r.PathValue("id")); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// deriveTitleFromContent extracts the first non-empty line (stripped of # heading markers).
func deriveTitleFromContent(content string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(strings.TrimLeft(line, "#"))
		if line != "" {
			if len(line) > 80 {
				return line[:77] + "..."
			}
			return line
		}
	}
	return ""
}
