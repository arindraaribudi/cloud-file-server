package api

import (
	"net/http"
	"strconv"

	"github.com/example/cos-workflow/internal/components"
	"github.com/example/cos-workflow/internal/model"
)

func (a *API) catalog(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	stage := model.Stage(q.Get("stage"))
	switch stage {
	case "", model.StageTrigger, model.StageProcess, model.StageOutput:
	default:
		writeError(w, http.StatusBadRequest, "bad_request", "invalid stage", nil)
		return
	}
	if key := q.Get("key"); key != "" {
		version, _ := strconv.Atoi(q.Get("version"))
		t, ok := a.Registry.Get(key, version)
		if !ok {
			writeError(w, http.StatusNotFound, "not_found", "component type not found", nil)
			return
		}
		writeJSON(w, http.StatusOK, components.CatalogEntry{
			Key: t.Key, Name: t.Name, Description: t.Description,
			Stages: t.Stages, EventType: t.EventType, Version: t.Version,
			ConfigSchema: t.ConfigSchema, DefaultConfig: t.DefaultConfig,
		})
		return
	}
	writeJSON(w, http.StatusOK, a.Registry.Catalog(stage))
}
