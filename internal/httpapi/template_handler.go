package httpapi

import (
	"net/http"

	"github.com/hanan/avatar-catalog-backend/internal/service"
)

type templateHandler struct {
	templates *service.Templates
}

type registerTemplateBody struct {
	TemplateID jsonID `json:"templateId"`
	Name       string `json:"name"`
	Gender     string `json:"gender"`
}

type updateTemplateBody struct {
	Name   *string `json:"name"`
	Gender *string `json:"gender"`
}

// list menangani GET /v1/templates.
func (h *templateHandler) list(w http.ResponseWriter, r *http.Request) {
	limit, err := queryInt(r, "limit", 0)
	if err != nil {
		writeError(w, err)
		return
	}

	page, err := h.templates.List(r.Context(), r.URL.Query().Get("cursor"), limit)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, newListEnvelope(newTemplates(page.Templates), page.NextCursor, page.HasMore))
}

// get menangani GET /v1/templates/{templateId}.
func (h *templateHandler) get(w http.ResponseWriter, r *http.Request) {
	tpl, err := h.templates.Get(r.Context(), r.PathValue("templateId"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, newTemplate(tpl))
}

// register menangani POST /v1/templates.
//
// Mendaftarkan rig secara eksplisit beserta nama dan gendernya. Rig yang sudah
// terdaftar dijawab 200 tanpa menimpa apa pun, sehingga aman diulang.
func (h *templateHandler) register(w http.ResponseWriter, r *http.Request) {
	var body registerTemplateBody
	if !decodeJSON(w, r, &body) {
		return
	}

	tpl, created, err := h.templates.Register(r.Context(), service.RegisterTemplateInput{
		TemplateID: body.TemplateID.String(),
		Name:       body.Name,
		Gender:     body.Gender,
	})
	if err != nil {
		writeError(w, err)
		return
	}

	if !created {
		writeJSON(w, http.StatusOK, newTemplate(tpl))
		return
	}
	w.Header().Set("Location", "/v1/templates/"+tpl.TemplateID)
	writeJSON(w, http.StatusCreated, newTemplate(tpl))
}

// update menangani PATCH /v1/templates/{templateId}.
func (h *templateHandler) update(w http.ResponseWriter, r *http.Request) {
	var body updateTemplateBody
	if !decodeJSON(w, r, &body) {
		return
	}

	tpl, err := h.templates.Update(r.Context(), r.PathValue("templateId"), service.UpdateTemplateInput{
		Name:   body.Name,
		Gender: body.Gender,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, newTemplate(tpl))
}
