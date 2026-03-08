package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
)

func (h *Handler) registerTagRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/tasks/{id}/tags", h.apiAddTag)
	mux.HandleFunc("DELETE /api/tasks/{id}/tags/{tag}", h.apiRemoveTag)
	mux.HandleFunc("GET /api/tags", h.apiListAllTags)
}

func (h *Handler) apiAddTag(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("id")
	var body struct {
		Tag string `json:"tag"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	tag := strings.ToLower(strings.TrimSpace(body.Tag))
	if tag == "" {
		http.Error(w, "tag must not be blank", http.StatusBadRequest)
		return
	}
	if err := h.db.AddTag(taskID, tag); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	tags, err := h.db.ListTags(taskID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if tags == nil {
		tags = []string{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tags)
}

func (h *Handler) apiRemoveTag(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("id")
	tag := strings.ToLower(strings.TrimSpace(r.PathValue("tag")))
	if err := h.db.RemoveTag(taskID, tag); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) apiListAllTags(w http.ResponseWriter, r *http.Request) {
	tags, err := h.db.ListAllTags()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if tags == nil {
		tags = []string{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tags)
}
