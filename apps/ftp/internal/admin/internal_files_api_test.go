package admin

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/spf13/afero"
)

func TestRequireServiceToken(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	t.Run("unconfigured", func(t *testing.T) {
		api := &API{}
		w := httptest.NewRecorder()
		api.RequireServiceToken(next).ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/internal/v1/files/rename", nil))
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("status=%d, want 503", w.Code)
		}
	})

	cases := []struct {
		name  string
		token string
		want  int
	}{
		{"missing", "", http.StatusUnauthorized},
		{"wrong", "nope", http.StatusUnauthorized},
		{"correct", "secret", http.StatusNoContent},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api := &API{WorkflowServiceToken: "secret"}
			req := httptest.NewRequest(http.MethodPost, "/internal/v1/files/rename", nil)
			if tc.token != "" {
				req.Header.Set("X-Workflow-Token", tc.token)
			}
			w := httptest.NewRecorder()
			api.RequireServiceToken(next).ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Fatalf("status=%d, want %d", w.Code, tc.want)
			}
		})
	}
}

func internalTestRouter(api *API, method, path string, handler http.HandlerFunc) http.Handler {
	r := chi.NewRouter()
	r.Group(func(r chi.Router) {
		r.Use(api.RequireServiceToken)
		r.Method(method, path, handler)
	})
	return r
}

func TestInternalRenameRejectsPathTraversalBeforeDB(t *testing.T) {
	api := &API{WorkflowServiceToken: "secret"}
	router := internalTestRouter(api, http.MethodPost, "/internal/v1/files/rename", api.internalRename)

	req := httptest.NewRequest(http.MethodPost, "/internal/v1/files/rename",
		strings.NewReader(`{"username":"alice","from":"../secret","to":"ok.txt"}`))
	req.Header.Set("X-Workflow-Token", "secret")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s, want 400", w.Code, w.Body.String())
	}
}

func TestInternalCopyRejectsPathTraversalBeforeDB(t *testing.T) {
	api := &API{WorkflowServiceToken: "secret"}
	router := internalTestRouter(api, http.MethodPost, "/internal/v1/files/copy", api.internalCopy)

	req := httptest.NewRequest(http.MethodPost, "/internal/v1/files/copy",
		strings.NewReader(`{"source_username":"alice","source_path":"../secret","target_path":"ok.txt"}`))
	req.Header.Set("X-Workflow-Token", "secret")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s, want 400", w.Code, w.Body.String())
	}
}

func TestInternalRenameRejectsUnknownJSONField(t *testing.T) {
	api := &API{WorkflowServiceToken: "secret"}
	router := internalTestRouter(api, http.MethodPost, "/internal/v1/files/rename", api.internalRename)

	req := httptest.NewRequest(http.MethodPost, "/internal/v1/files/rename",
		strings.NewReader(`{"username":"alice","from":"a.txt","to":"b.txt","bogus":true}`))
	req.Header.Set("X-Workflow-Token", "secret")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s, want 400", w.Code, w.Body.String())
	}
}

func TestCopyAfero(t *testing.T) {
	src := afero.NewMemMapFs()
	dst := afero.NewMemMapFs()
	if err := afero.WriteFile(src, "a.txt", []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := copyAfero(src, dst, "a.txt", "archive/a.txt", 5); err != nil {
		t.Fatal(err)
	}
	got, err := afero.ReadFile(dst, "archive/a.txt")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello" {
		t.Fatalf("got %q", got)
	}
	if err := copyAfero(src, dst, "a.txt", "archive/b.txt", maxFallbackCopyBytes+1); !errors.Is(err, errCopyTooLarge) {
		t.Fatalf("got %v, want errCopyTooLarge", err)
	}
}

func TestInternalStatRejectsEmptyPath(t *testing.T) {
	api := &API{WorkflowServiceToken: "secret"}
	router := internalTestRouter(api, http.MethodGet, "/internal/v1/files/stat", api.internalStat)

	req := httptest.NewRequest(http.MethodGet, "/internal/v1/files/stat?username=alice&path=", nil)
	req.Header.Set("X-Workflow-Token", "secret")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s, want 400", w.Code, w.Body.String())
	}
}
