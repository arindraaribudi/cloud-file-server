package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/example/cos-workflow/internal/action"
	"github.com/example/cos-workflow/internal/components"
	"github.com/example/cos-workflow/internal/engine"
	"github.com/example/cos-workflow/internal/model"
	"github.com/example/cos-workflow/internal/store"
)

type fakeStore struct {
	mu        sync.Mutex
	workflows map[uuid.UUID]*model.Workflow
}

func newFakeStore() *fakeStore {
	return &fakeStore{workflows: map[uuid.UUID]*model.Workflow{}}
}

func (f *fakeStore) List(_ context.Context, search string, enabled *bool, _, _ int) ([]*model.Workflow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*model.Workflow, 0)
	for _, wf := range f.workflows {
		if search != "" && !strings.Contains(strings.ToLower(wf.Name), strings.ToLower(search)) {
			continue
		}
		if enabled != nil && wf.Enabled != *enabled {
			continue
		}
		out = append(out, cloneWorkflow(wf))
	}
	return out, nil
}

func (f *fakeStore) Get(_ context.Context, id uuid.UUID) (*model.Workflow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	wf, ok := f.workflows[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	return cloneWorkflow(wf), nil
}

func (f *fakeStore) Create(_ context.Context, wf *model.Workflow) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, existing := range f.workflows {
		if strings.EqualFold(existing.Name, wf.Name) {
			return store.ErrConflict
		}
	}
	wf.ID = uuid.New()
	wf.Version = 1
	wf.CreatedAt = time.Now()
	wf.UpdatedAt = wf.CreatedAt
	f.workflows[wf.ID] = cloneWorkflow(wf)
	return nil
}

func (f *fakeStore) Update(_ context.Context, wf *model.Workflow, expectedVersion int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	existing, ok := f.workflows[wf.ID]
	if !ok {
		return store.ErrNotFound
	}
	if existing.Version != expectedVersion {
		return store.ErrConflict
	}
	wf.Version = existing.Version + 1
	wf.CreatedAt = existing.CreatedAt
	wf.UpdatedAt = time.Now()
	f.workflows[wf.ID] = cloneWorkflow(wf)
	return nil
}

func (f *fakeStore) SetEnabled(_ context.Context, id uuid.UUID, enabled bool, expectedVersion int, _ string) (int, time.Time, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	wf, ok := f.workflows[id]
	if !ok {
		return 0, time.Time{}, store.ErrNotFound
	}
	if wf.Version != expectedVersion {
		return 0, time.Time{}, store.ErrConflict
	}
	wf.Enabled = enabled
	wf.Version++
	wf.UpdatedAt = time.Now()
	return wf.Version, wf.UpdatedAt, nil
}

func (f *fakeStore) Delete(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.workflows[id]; !ok {
		return store.ErrNotFound
	}
	delete(f.workflows, id)
	return nil
}

func (f *fakeStore) Ping(context.Context) error { return nil }

func (f *fakeStore) Audit(context.Context, string, string, map[string]any) error { return nil }

func cloneWorkflow(wf *model.Workflow) *model.Workflow {
	cp := *wf
	cp.Components = append([]model.Component(nil), wf.Components...)
	for i := range cp.Components {
		cp.Components[i].Config = append(json.RawMessage(nil), cp.Components[i].Config...)
	}
	return &cp
}

func testAPI() (*API, *fakeStore) {
	st := newFakeStore()
	return New(st, components.NewRegistry(), "svc-token"), st
}

func auth(req *http.Request, role string) {
	req.Header.Set("X-Workflow-Proxy-Token", "svc-token")
	req.Header.Set("X-Authenticated-User", "alice")
	req.Header.Set("X-Authenticated-Role", role)
}

const validWorkflowBody = `
{
  "name": "Archive invoices",
  "description": "test",
  "enabled": false,
  "trigger_mode": "any",
  "components": [
    {"stage":"trigger","type":"trigger.file.upload","config":{"path_target":"basename","path_regex":"^invoice_","mime_patterns":["application/pdf"]}},
    {"stage":"process","type":"process.file.rename","config":{"mode":"template","template":"archive/{{.Date}}/{{.Base}}{{.Ext}}","collision":"suffix"}},
    {"stage":"output","type":"output.notify.email","config":{"to":["ops@example.com"],"subject_template":"done","body_template":"ok"}}
  ]
}
`

func TestAPICreateGetAndList(t *testing.T) {
	a, _ := testAPI()
	handler := a.Routes()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/workflows", strings.NewReader(validWorkflowBody))
	auth(req, "admin")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", w.Code, w.Body.String())
	}
	var created model.Workflow
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.ID == uuid.Nil || created.Version != 1 {
		t.Fatalf("created=%+v", created)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/workflows/"+created.ID.String(), nil)
	auth(req, "readonly")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("get status=%d body=%s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/workflows", nil)
	auth(req, "readonly")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestAPICreateKeepsParallelGroup(t *testing.T) {
	a, _ := testAPI()
	body := `{"name":"fanout","enabled":false,"trigger_mode":"any","components":[
	  {"stage":"trigger","type":"trigger.file.upload","config":{"path_target":"basename"}},
	  {"stage":"output","label":"A","type":"output.file.copy","parallel_group":"output.file.copy","config":{"destination_path":"a/{{.FileName}}","collision":"suffix"}},
	  {"stage":"output","label":"B","type":"output.file.copy","parallel_group":"output.file.copy","config":{"destination_path":"b/{{.FileName}}","collision":"suffix"}}
	]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/workflows", strings.NewReader(body))
	auth(req, "admin")
	w := httptest.NewRecorder()
	a.Routes().ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", w.Code, w.Body.String())
	}
	var created model.Workflow
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	for _, c := range created.Components {
		want := ""
		if c.Stage == model.StageOutput {
			want = "output.file.copy"
		}
		if c.ParallelGroup != want {
			t.Fatalf("component %q parallel_group=%q want %q", c.Label, c.ParallelGroup, want)
		}
	}

	bad := strings.Replace(body, `"label":"B","type":"output.file.copy","parallel_group":"output.file.copy"`, `"label":"B","type":"output.file.copy","parallel_group":"not valid!"`, 1)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/workflows", strings.NewReader(bad))
	auth(req, "admin")
	w = httptest.NewRecorder()
	a.Routes().ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid parallel_group status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestAPIPreview(t *testing.T) {
	a, _ := testAPI()
	body := `{"sample":{"username":"alice","path":"scans/scan_001.pdf","mime":"application/pdf","event_type":"upload"},
	  "components":[
	  {"stage":"trigger","type":"trigger.file.upload","config":{"path_target":"basename","path_regex":"^scan_"}},
	  {"stage":"output","label":"Folder A","type":"output.file.copy","config":{"destination_path":"a/{{.FileName}}","collision":"suffix"}}
	]}`
	do := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/workflows/preview", strings.NewReader(body))
		auth(req, "admin")
		w := httptest.NewRecorder()
		a.Routes().ServeHTTP(w, req)
		return w
	}
	if w := do(); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("without an engine status=%d, want 503", w.Code)
	}

	a.Engine = &engine.Engine{Actions: action.NewExecutor(nil, nil)}
	w := do()
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var got struct {
		Matched bool `json:"matched"`
		Steps   []struct {
			Label  string         `json:"label"`
			Status string         `json:"status"`
			Output map[string]any `json:"output"`
		} `json:"steps"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Matched || len(got.Steps) != 1 || got.Steps[0].Status != "ok" ||
		got.Steps[0].Output["destination_path"] != "a/scan_001.pdf" {
		t.Fatalf("response=%s", w.Body.String())
	}
}

func TestAPICreateKeepsRoutes(t *testing.T) {
	a, _ := testAPI()
	body := `{"name":"routes","enabled":false,"trigger_mode":"any","components":[
	  {"stage":"trigger","type":"trigger.file.upload","config":{"path_target":"basename"}},
	  {"stage":"process","type":"process.condition","route":"r1","config":{"path_target":"basename","path_regex":"^a"}},
	  {"stage":"output","label":"A","type":"output.file.copy","route":"r1","config":{"destination_path":"a/{{.FileName}}","collision":"suffix"}},
	  {"stage":"process","type":"process.condition","route":"r2","config":{"path_target":"basename","path_regex":"^b"}},
	  {"stage":"output","label":"B","type":"output.file.copy","route":"r2","config":{"destination_path":"b/{{.FileName}}","collision":"suffix"}}
	]}`
	post := func(b string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/workflows", strings.NewReader(b))
		auth(req, "admin")
		w := httptest.NewRecorder()
		a.Routes().ServeHTTP(w, req)
		return w
	}
	w := post(body)
	if w.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", w.Code, w.Body.String())
	}
	var created model.Workflow
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	routes := map[string]string{}
	for _, c := range created.Components {
		if c.Stage != model.StageTrigger {
			routes[c.TypeKey+"/"+c.Label] = c.Route
		}
	}
	if routes["output.file.copy/A"] != "r1" || routes["output.file.copy/B"] != "r2" {
		t.Fatalf("routes=%v", routes)
	}
	// a trigger cannot belong to a route, and route keys are restricted
	if w := post(strings.Replace(body, `{"stage":"trigger","type":"trigger.file.upload",`, `{"stage":"trigger","type":"trigger.file.upload","route":"r1",`, 1)); w.Code != http.StatusBadRequest {
		t.Fatalf("trigger with a route status=%d", w.Code)
	}
	if w := post(strings.Replace(body, `"route":"r2"`, `"route":"bad route!"`, 1)); w.Code != http.StatusBadRequest {
		t.Fatalf("invalid route status=%d", w.Code)
	}
}

func TestAPIAuthAndRole(t *testing.T) {
	a, _ := testAPI()
	handler := a.Routes()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workflows", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("missing token status=%d", w.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/v1/workflows", strings.NewReader(validWorkflowBody))
	auth(req, "readonly")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("readonly create status=%d", w.Code)
	}
}

func TestAPIUpdateVersionConflict(t *testing.T) {
	a, _ := testAPI()
	handler := a.Routes()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/workflows", strings.NewReader(validWorkflowBody))
	auth(req, "admin")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", w.Code, w.Body.String())
	}
	var created model.Workflow
	_ = json.Unmarshal(w.Body.Bytes(), &created)

	body := strings.Replace(validWorkflowBody, `"enabled": false`, `"enabled": false, "version": 99`, 1)
	req = httptest.NewRequest(http.MethodPut, "/api/v1/workflows/"+created.ID.String(), strings.NewReader(body))
	auth(req, "admin")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("conflict status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestAPICatalogAndTestMatch(t *testing.T) {
	a, _ := testAPI()
	handler := a.Routes()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workflow-components?stage=trigger", nil)
	auth(req, "readonly")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("catalog status=%d body=%s", w.Code, w.Body.String())
	}

	body := `
{"sample":{"username":"alice","path":"invoices/invoice_1.pdf","mime":"application/pdf","event_type":"upload"},
 "components":[{"stage":"trigger","type":"trigger.file.upload","config":{"path_target":"basename","path_regex":"^invoice_","mime_patterns":["application/pdf"]}}]}
`
	req = httptest.NewRequest(http.MethodPost, "/api/v1/workflows/test-match", strings.NewReader(body))
	auth(req, "readonly")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("test-match status=%d body=%s", w.Code, w.Body.String())
	}
	var res testMatchResponse
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if !res.Matched {
		t.Fatalf("expected match: %+v", res)
	}
}

func TestAPICreateRejectsInvalidComponent(t *testing.T) {
	a, _ := testAPI()
	handler := a.Routes()
	body := bytes.NewBufferString(`{"name":"bad","trigger_mode":"any","components":[{"stage":"trigger","type":"trigger.file.upload","config":{"path_target":"basename","path_regex":"("}}]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/workflows", body)
	auth(req, "admin")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}
