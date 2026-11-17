import type { RunComponent } from "../../lib/workflows";

const STAGE_ORDER: Record<string, number> = { trigger: 0, process: 1, output: 2 };

function pretty(value: unknown): string {
  if (value == null) return "";
  if (typeof value === "string") return value;
  try {
    return JSON.stringify(value, null, 2);
  } catch {
    return String(value);
  }
}

export function RunTimeline({ components }: { components: RunComponent[] }) {
  const ordered = [...components].sort((a, b) => {
    const sa = STAGE_ORDER[a.stage] ?? 9;
    const sb = STAGE_ORDER[b.stage] ?? 9;
    return sa === sb ? a.position - b.position : sa - sb;
  });

  if (ordered.length === 0) return <p className="muted">No components recorded for this run.</p>;

  return (
    <div className="run-timeline">
      {ordered.map((component) => {
        const output = pretty(component.output);
        return (
        <article className="timeline-item" key={component.id}>
          <div className="timeline-marker">
            <span className={"status-dot " + statusTone(component.status)} />
          </div>
          <div className="timeline-body">
            <div className="timeline-head">
              <div>
                <span className="stage-kicker">{component.stage}</span>
                <strong>{component.label || component.type}</strong>
                <span className="component-type">{component.type}</span>
              </div>
              <span className={"status-badge " + component.status}>{component.status.replaceAll("_", " ")}</span>
            </div>
            {component.error && <p className="err">{component.error}</p>}
            {output && (
              <details className="timeline-output">
                <summary>Output</summary>
                <pre>{output}</pre>
              </details>
            )}
          </div>
        </article>
        );
      })}
    </div>
  );
}

function statusTone(status: string) {
  switch (status) {
    case "succeeded":
    case "matched":
      return "on";
    case "failed":
      return "bad";
    case "running":
      return "busy";
    default:
      return "off";
  }
}
