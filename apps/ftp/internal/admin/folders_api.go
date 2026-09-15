package admin

import (
	"encoding/json"
	"net/http"
	"strings"
)

type folderSuggestion struct {
	Name string `json:"name"`
}

// listFolders returns immediate-child folder names under ?prefix= so the
// "Provision user" form can offer them as <datalist> suggestions. Free text
// is still accepted; createUser PUTs the placeholder on submit.
func (a *API) listFolders(w http.ResponseWriter, r *http.Request) {
	if a.COSClient == nil {
		http.Error(w, "COS client not configured (server missing COS credentials)", http.StatusServiceUnavailable)
		return
	}
	prefix := r.URL.Query().Get("prefix")
	if prefix == "" {
		prefix = a.DefaultRootPrefix
	}
	// Bucket keys are stored without a leading "/" (see createUser's Put key).
	// A configured prefix like "/" or "/t/t/" would never match. Normalize so
	// the listing reflects the real bucket layout.
	prefix = strings.TrimPrefix(prefix, "/")
	entries, err := a.COSClient.List(r.Context(), prefix)
	if err != nil {
		http.Error(w, friendlyCOSErr("list folders", err), http.StatusBadGateway)
		return
	}
	out := make([]folderSuggestion, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir {
			continue
		}
		// Suggestion name is the entry's relative path (already relative to the
		// listed prefix). Returned bare so the client can match it directly
		// against the user-typed folder name.
		name := strings.Trim(strings.TrimSuffix(e.Name, "/"), "/")
		if name == "" {
			continue
		}
		out = append(out, folderSuggestion{Name: name})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}
