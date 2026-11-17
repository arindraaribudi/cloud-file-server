import { useState } from "react";
import {
  previewWorkflow,
  testMatch,
  type ComponentType,
  type PreviewResponse,
  type TestMatchResponse,
  type TriggerMode,
  type WorkflowComponentInput,
} from "../../lib/workflows";
import { PreviewResult } from "./PreviewResult";
import { UserSelect } from "./UserSelect";

export function TriggerTestPanel({
  components,
  catalog,
  triggerMode,
}: {
  components: WorkflowComponentInput[];
  catalog: ComponentType[];
  triggerMode: TriggerMode;
}) {
  const [username, setUsername] = useState("");
  const [path, setPath] = useState("invoices/invoice_001.pdf");
  const [mime, setMime] = useState("application/pdf");
  const [eventType, setEventType] = useState<"upload" | "remove">("upload");
  const [result, setResult] = useState<TestMatchResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [preview, setPreview] = useState<PreviewResponse | null>(null);
  const [previewError, setPreviewError] = useState<string | null>(null);
  const [previewing, setPreviewing] = useState(false);

  const triggers = components.filter((c) => c.stage === "trigger");
  const sample = { username, path, mime, event_type: eventType };

  async function runPreview() {
    setPreviewing(true);
    setPreviewError(null);
    setResult(null);
    try {
      setPreview(await previewWorkflow({ sample, components }));
    } catch (err) {
      setPreview(null);
      setPreviewError(String(err));
    } finally {
      setPreviewing(false);
    }
  }

  async function run() {
    setLoading(true);
    setError(null);
    setPreview(null);
    try {
      const res = await testMatch({
        sample,
        trigger_mode: triggerMode,
        components: triggers,
      });
      setResult(res);
    } catch (err) {
      setError(String(err));
    } finally {
      setLoading(false);
    }
  }

  return (
    <section className="test-panel">
      <div className="test-panel-head">
        <div>
          <span className="eyebrow">Try it</span>
          <h2>Test against a sample file</h2>
          <p className="muted">
            Describe a file below. Preview uses your current canvas, including changes you have not
            saved, and changes nothing.
          </p>
        </div>
        <div className="form-actions">
          <button type="button" className="btn btn-ghost" onClick={run} disabled={loading || triggers.length === 0 || !username}>
            {loading ? "Testing..." : "Test triggers"}
          </button>
          <button
            type="button"
            className="btn btn-primary"
            onClick={runPreview}
            disabled={previewing || triggers.length === 0 || !username}
          >
            {previewing ? "Working..." : "Preview what would happen"}
          </button>
        </div>
      </div>
      <div className="test-grid">
        <div className="field">
          <label htmlFor="test-username">Username</label>
          <UserSelect id="test-username" value={username} onChange={setUsername} />
        </div>
        <div className="field">
          <label htmlFor="test-path">Path</label>
          <input id="test-path" className="mono" value={path} onChange={(e) => setPath(e.target.value)} />
        </div>
        <div className="field">
          <label htmlFor="test-mime">MIME</label>
          <input id="test-mime" className="mono" value={mime} onChange={(e) => setMime(e.target.value)} />
        </div>
        <div className="field">
          <label htmlFor="test-event">Event</label>
          <select id="test-event" value={eventType} onChange={(e) => setEventType(e.target.value as "upload" | "remove")}>
            <option value="upload">upload</option>
            <option value="remove">remove</option>
          </select>
        </div>
      </div>
      {triggers.length === 0 && <p className="muted">Add a trigger to the canvas to test.</p>}
      {error && <p className="err">{error}</p>}
      {previewError && <p className="err">{previewError}</p>}
      {preview && <PreviewResult data={preview} catalog={catalog} components={components} />}
      {result && (
        <div className="test-result">
          <div className={result.matched ? "test-match yes" : "test-match no"}>
            {result.matched ? "Matched" : "Not matched"}
          </div>
          {result.triggers.map((t, index) => (
            <div className="test-trigger" key={(t.component_id ?? t.type) + index}>
              <span className={t.matched ? "status-dot on" : "status-dot off"} />
              <strong>{t.label || t.type}</strong>
              <span className="muted">{t.reason}</span>
            </div>
          ))}
          {result.workflows?.map((w) => (
            <div className="test-workflow" key={w.id}>
              <strong>{w.name}</strong> {w.matched ? "matched" : "did not match"}
            </div>
          ))}
        </div>
      )}
    </section>
  );
}
