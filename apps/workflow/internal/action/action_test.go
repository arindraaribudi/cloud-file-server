package action

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/example/cos-workflow/internal/components"
	"github.com/example/cos-workflow/internal/model"
)

func testRunContext(runID uuid.UUID) *model.RunContext {
	return &model.RunContext{
		RunID: runID, WorkflowID: uuid.New(), Username: "alice",
		Path: "invoices/invoice.pdf", OriginalPath: "invoices/invoice.pdf",
		EventType: "upload", EventTime: time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC),
		MIME: "application/pdf", Size: 10,
	}
}

func testRequest(runID uuid.UUID, typeKey string, cfg string) *Request {
	return &Request{
		Workflow: &model.Workflow{ID: uuid.New(), Name: "Archive"},
		Run:      &model.Run{ID: runID, Status: "running", Source: "event"},
		Context:  testRunContext(runID),
		Component: &model.RunComponent{
			ID: uuid.New(), TypeKey: typeKey, Label: "test", Config: json.RawMessage(cfg),
		},
	}
}

func TestExecutorRenameTemplate(t *testing.T) {
	var got map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/v1/files/rename" {
			t.Errorf("path=%s", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		_ = json.NewEncoder(w).Encode(map[string]string{"path": "archive/2026-11-10/invoice.pdf"})
	}))
	defer server.Close()

	runID := uuid.New()
	e := NewExecutor(NewFileClient(server.URL, "tok", time.Second), nil)
	req := testRequest(runID, components.TypeProcessRename, `{"mode":"template","template":"archive/{{.Date}}/{{.FileName}}","collision":"fail"}`)
	res, err := e.Execute(t.Context(), req, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Path != "archive/2026-11-10/invoice.pdf" {
		t.Fatalf("path=%q", res.Path)
	}
	if got["to"] != "archive/2026-11-10/invoice.pdf" || got["from"] != "invoices/invoice.pdf" {
		t.Fatalf("request=%+v", got)
	}
}

func TestExecutorRenameCollisionSuffix(t *testing.T) {
	runID := uuid.MustParse("11111111-2222-3333-4444-555555555555")
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		to, _ := body["to"].(string)
		calls = append(calls, to)
		if to == "archive/invoice.pdf" {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"error":"exists"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"path": to})
	}))
	defer server.Close()

	e := NewExecutor(NewFileClient(server.URL, "tok", time.Second), nil)
	req := testRequest(runID, components.TypeProcessRename, `{"mode":"template","template":"archive/{{.FileName}}","collision":"suffix"}`)
	res, err := e.Execute(t.Context(), req, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Path != "archive/invoice_11111111.pdf" {
		t.Fatalf("path=%q", res.Path)
	}
	if len(calls) != 2 {
		t.Fatalf("calls=%v", calls)
	}
}

func TestExecutorRemoveMissingOK(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"missing"}`))
	}))
	defer server.Close()

	runID := uuid.New()
	e := NewExecutor(NewFileClient(server.URL, "tok", time.Second), nil)
	req := testRequest(runID, components.TypeProcessRemove, `{"path_template":"","missing_ok":true}`)
	if _, err := e.Execute(t.Context(), req, false); err != nil {
		t.Fatal(err)
	}
}

func TestExecutorDryRunDoesNotCallFileAPI(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	runID := uuid.New()
	e := NewExecutor(NewFileClient(server.URL, "tok", time.Second), nil)
	req := testRequest(runID, components.TypeProcessRename, `{"mode":"template","template":"archive/{{.FileName}}","collision":"fail"}`)
	res, err := e.Execute(t.Context(), req, true)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("calls=%d, want 0", calls)
	}
	if res.Path != "archive/invoice.pdf" {
		t.Fatalf("path=%q", res.Path)
	}
}

func TestSendWebhookMethodGetHasNoBody(t *testing.T) {
	var gotMethod string
	var gotLen int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		b, _ := io.ReadAll(r.Body)
		gotLen = len(b)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	n := NewNotifier(NotifierConfig{AllowHTTP: true, AllowPrivate: true})
	req := testRequest(uuid.New(), components.TypeOutputWebhook, `{}`)
	out, err := n.SendWebhook(t.Context(), req, components.WebhookConfig{URL: server.URL, Method: "GET"})
	if err != nil {
		t.Fatal(err)
	}
	if gotMethod != "GET" || gotLen != 0 || out["method"] != "GET" {
		t.Fatalf("method=%s bodyLen=%d out=%v", gotMethod, gotLen, out)
	}
}

func TestPreviewWebhookRendersBodyWithoutSending(t *testing.T) {
	// nothing listens on this port: a real send would fail
	n := NewNotifier(NotifierConfig{AllowHTTP: true, AllowPrivate: true})
	req := testRequest(uuid.New(), components.TypeOutputWebhook, `{}`)
	out, err := n.PreviewWebhook(t.Context(), req, components.WebhookConfig{
		URL: "http://127.0.0.1:1/hook", Method: "PUT",
		BodyTemplate: `{"file":"{{.FileName}}","user":"{{.Username}}"}`,
		Headers:      map[string]string{"X-B": "1", "X-A": "2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out["method"] != "PUT" || out["body"] != `{"file":"invoice.pdf","user":"alice"}` {
		t.Fatalf("preview=%v", out)
	}
	if h, _ := out["extra_headers"].([]string); len(h) != 2 || h[0] != "X-A" {
		t.Fatalf("extra_headers=%v", out["extra_headers"])
	}

	get, err := n.PreviewWebhook(t.Context(), req, components.WebhookConfig{URL: "http://127.0.0.1:1/x", Method: "GET"})
	if err != nil {
		t.Fatal(err)
	}
	if _, hasBody := get["body"]; hasBody {
		t.Fatalf("GET preview must not include a body: %v", get)
	}

	if _, err := n.PreviewWebhook(t.Context(), req, components.WebhookConfig{URL: ""}); err == nil {
		t.Fatal("an empty URL must fail the preview, as it would fail a real run")
	}
}

func TestPreviewEmailNeedsSMTP(t *testing.T) {
	req := testRequest(uuid.New(), components.TypeOutputEmail, `{}`)
	cfg := components.EmailConfig{To: []string{"ops@example.com"}, SubjectTemplate: "File {{.FileName}}"}
	if _, err := (&Notifier{}).PreviewEmail(req, cfg); err == nil {
		t.Fatal("expected an error when SMTP is not configured")
	}
	n := &Notifier{SMTPHost: "smtp.example.com", SMTPFrom: "wf@example.com"}
	out, err := n.PreviewEmail(req, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if out["subject"] != "File invoice.pdf" {
		t.Fatalf("preview=%v", out)
	}
}

func TestExecutorConditionStops(t *testing.T) {
	runID := uuid.New()
	e := NewExecutor(nil, nil)
	req := testRequest(runID, components.TypeProcessCondition, `{"path_target":"basename","path_regex":"^scan_","negate":false}`)
	res, err := e.Execute(t.Context(), req, false)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Stop {
		t.Fatal("expected condition to stop the run")
	}
}

func TestExecutorConditionContinues(t *testing.T) {
	runID := uuid.New()
	e := NewExecutor(nil, nil)
	req := testRequest(runID, components.TypeProcessCondition, `{"path_target":"basename","path_regex":"^invoice","negate":false}`)
	res, err := e.Execute(t.Context(), req, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Stop {
		t.Fatal("expected condition to continue the run")
	}
}

func TestTemplatePathTraversalAndSuffix(t *testing.T) {
	if _, err := cleanRelPath("../secret"); err == nil {
		t.Fatal("expected traversal error")
	}
	if got := addSuffix("a/b.txt", "11111111-aaaa"); got != "a/b_11111111.txt" {
		t.Fatalf("suffix=%q", got)
	}
}

func TestNotifierWebhookSignature(t *testing.T) {
	t.Setenv("TEST_WEBHOOK_SECRET", "s3cr3t")
	type captured struct {
		body string
		sig  string
		ts   string
		wf   string
		run  string
		comp string
		idem string
	}
	ch := make(chan captured, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		ch <- captured{
			body: string(body), sig: r.Header.Get("X-Workflow-Signature"),
			ts: r.Header.Get("X-Workflow-Timestamp"), wf: r.Header.Get("X-Workflow-Id"),
			run: r.Header.Get("X-Workflow-Run-Id"), comp: r.Header.Get("X-Workflow-Component-Id"),
			idem: r.Header.Get("Idempotency-Key"),
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	runID := uuid.New()
	req := testRequest(runID, components.TypeOutputWebhook, `{"url":"x","secret_env":"TEST_WEBHOOK_SECRET"}`)
	req.Run.Event = json.RawMessage(`{"event_type":"upload","path":"invoices/invoice.pdf"}`)
	n := NewNotifier(NotifierConfig{AllowHTTP: true, AllowPrivate: true})
	out, err := n.SendWebhook(t.Context(), req, components.WebhookConfig{
		URL: server.URL, SecretEnv: "TEST_WEBHOOK_SECRET",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out["status"] != http.StatusOK {
		t.Fatalf("out=%+v", out)
	}
	c := <-ch
	if c.wf != req.Context.WorkflowID.String() || c.run != runID.String() || c.comp != req.Component.ID.String() {
		t.Fatalf("correlation=%+v", c)
	}
	if c.idem != runID.String()+":"+req.Component.ID.String() {
		t.Fatalf("idempotency=%q", c.idem)
	}
	mac := hmac.New(sha256.New, []byte("s3cr3t"))
	_, _ = mac.Write([]byte(c.ts + "." + c.body))
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if c.sig != want {
		t.Fatalf("signature=%q, want %q", c.sig, want)
	}
	if !strings.Contains(c.body, "Archive") {
		t.Fatalf("body=%s", c.body)
	}
}

func TestNotifierRejectsPrivateByDefault(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	n := NewNotifier(NotifierConfig{AllowHTTP: true})
	req := testRequest(uuid.New(), components.TypeOutputWebhook, `{}`)
	_, err := n.SendWebhook(t.Context(), req, components.WebhookConfig{URL: server.URL})
	if err == nil {
		t.Fatal("expected private target rejection")
	}
}

func TestNotifierWebhook5xxIsRetryable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	n := NewNotifier(NotifierConfig{AllowHTTP: true, AllowPrivate: true})
	req := testRequest(uuid.New(), components.TypeOutputWebhook, `{}`)
	_, err := n.SendWebhook(t.Context(), req, components.WebhookConfig{URL: server.URL})
	if err == nil || !IsRetryable(err) {
		t.Fatalf("err=%v retryable=%v", err, IsRetryable(err))
	}
}
