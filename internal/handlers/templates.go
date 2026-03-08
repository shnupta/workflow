package handlers

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/shnupta/workflow/internal/models"
)

func (h *Handler) registerTemplateRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /templates", h.templatesPage)
	mux.HandleFunc("POST /templates", h.createTemplate)
	mux.HandleFunc("GET /api/templates", h.apiListTemplates)
	mux.HandleFunc("DELETE /api/templates/{id}", h.apiDeleteTemplate)
	mux.HandleFunc("POST /templates/{templateID}/use", h.createTaskFromTemplate)
	mux.HandleFunc("POST /api/tasks/{id}/save-as-template", h.apiSaveAsTemplate)
}

// templatesPage renders GET /templates — the full template list page.
func (h *Handler) templatesPage(w http.ResponseWriter, r *http.Request) {
	tmpls, err := h.db.ListTemplates()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if tmpls == nil {
		tmpls = []*models.TaskTemplate{}
	}
	h.render(w, "templates.html", map[string]interface{}{
		"Templates": tmpls,
		"WorkTypes": h.cfg().WorkTypes,
		"Nav":       "templates",
	})
}

// createTemplate handles POST /templates (HTML form).
func (h *Handler) createTemplate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		http.Error(w, "name is required", 400)
		return
	}
	_, err := h.db.CreateTemplate(
		name,
		r.FormValue("work_type"),
		r.FormValue("description"),
		sanitizeRecurrence(r.FormValue("recurrence")),
	)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	http.Redirect(w, r, "/templates", http.StatusSeeOther)
}

// apiListTemplates handles GET /api/templates — JSON list for JS dropdown.
func (h *Handler) apiListTemplates(w http.ResponseWriter, r *http.Request) {
	tmpls, err := h.db.ListTemplates()
	if err != nil {
		jsonError(w, err.Error(), 500)
		return
	}
	if tmpls == nil {
		tmpls = []*models.TaskTemplate{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tmpls)
}

// apiDeleteTemplate handles DELETE /api/templates/{id}.
func (h *Handler) apiDeleteTemplate(w http.ResponseWriter, r *http.Request) {
	if err := h.db.DeleteTemplate(r.PathValue("id")); err != nil {
		jsonError(w, err.Error(), 500)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// createTaskFromTemplate handles POST /tasks/from-template/{templateID}.
// Redirects to the new task form with template fields pre-populated as query params.
// The form's JS reads these and fills the fields on load.
func (h *Handler) createTaskFromTemplate(w http.ResponseWriter, r *http.Request) {
	tmpl, err := h.db.GetTemplate(r.PathValue("templateID"))
	if err != nil {
		http.Error(w, "template not found", 404)
		return
	}
	q := url.Values{}
	q.Set("from_template", "1")
	q.Set("tmpl_name", tmpl.Name)
	if tmpl.WorkType != "" {
		q.Set("work_type", tmpl.WorkType)
	}
	if tmpl.Description != "" {
		q.Set("description", tmpl.Description)
	}
	if tmpl.Recurrence != "" {
		q.Set("recurrence", tmpl.Recurrence)
	}
	http.Redirect(w, r, "/tasks/new?"+q.Encode(), http.StatusSeeOther)
}

// apiSaveAsTemplate handles POST /api/tasks/{id}/save-as-template.
// Body: {"name":"<template name>"} — name defaults to the task title if omitted.
func (h *Handler) apiSaveAsTemplate(w http.ResponseWriter, r *http.Request) {
	t, err := h.db.GetTask(r.PathValue("id"))
	if err != nil {
		jsonError(w, "task not found", 404)
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "invalid request body", 400)
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		name = t.Title
	}
	tmpl, err := h.db.CreateTemplate(name, t.WorkType, t.Description, t.Recurrence)
	if err != nil {
		jsonError(w, "failed to create template: "+err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(tmpl)
}
