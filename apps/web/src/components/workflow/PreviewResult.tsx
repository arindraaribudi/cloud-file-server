import type {
  ComponentType,
  PreviewResponse,
  PreviewStep,
  WorkflowComponentInput,
} from "../../lib/workflows";
import { CONDITION_KEY, describeCondition, uiName } from "../../lib/workflowUi";

function text(value: unknown): string {
  return value == null ? "" : String(value);
}

// One plain-language line (and optional detail block) per step.
function describe(step: PreviewStep): { lines: string[]; detail?: { title: string; body: string } } {
  const o = step.output ?? {};
  switch (step.type) {
    case "process.condition":
      return {
        lines: [
          o.condition === true
            ? "The file matches, so the next steps run."
            : "The file does not match, so the steps after this are skipped.",
        ],
      };
    case "process.file.rename":
      return {
        lines: [text(o.source_path) + "  →  " + text(o.destination_path)],
      };
    case "process.file.remove":
      return { lines: ["Delete " + text(o.path)] };
    case "output.file.copy": {
      const where = (o.destination_username ? text(o.destination_username) + ": " : "") + text(o.destination_path);
      const lines = [text(o.source_path) + "  →  " + where];
      if (o.collision) lines.push("If that name already exists: " + text(o.collision));
      return { lines };
    }
    case "output.notify.webhook": {
      const lines = [text(o.method) + " " + text(o.url)];
      if (o.signed) lines.push("Signed with HMAC");
      if (Array.isArray(o.extra_headers) && o.extra_headers.length > 0) {
        lines.push("Extra headers: " + o.extra_headers.join(", "));
      }
      return {
        lines,
        detail: o.body ? { title: "Request body", body: text(o.body) } : undefined,
      };
    }
    case "output.notify.email": {
      const to = Array.isArray(o.to) ? o.to.join(", ") : text(o.to);
      return {
        lines: ["To " + to, "Subject: " + text(o.subject)],
        detail: o.body ? { title: "Email body", body: text(o.body) } : undefined,
      };
    }
    default:
      return { lines: [] };
  }
}

// A condition that is not met ends the workflow for this file.
function stopsHere(step: PreviewStep): boolean {
  return step.type === "process.condition" && step.status === "ok" && step.output?.condition !== true;
}

const STATUS_TEXT: Record<PreviewStep["status"], string> = {
  ok: "would run",
  skipped: "would be skipped",
  failed: "would fail",
};

export function PreviewResult({
  data,
  catalog,
  components,
}: {
  data: PreviewResponse;
  catalog: ComponentType[];
  components: WorkflowComponentInput[];
}) {
  const kind = (step: PreviewStep) =>
    uiName(step.type, catalog.find((t) => t.key === step.type)?.name ?? step.type);

  // One section per route (one per Condition box), in the order they appear.
  const routes: string[] = [];
  for (const step of data.steps) {
    const r = step.route ?? "";
    if (!routes.includes(r)) routes.push(r);
  }
  const sections = routes.map((route) => {
    const condition = components.find(
      (c) => c.type === CONDITION_KEY && (c.route ?? "") === route,
    );
    return {
      route,
      title: condition ? describeCondition(condition.config) : "any file",
      steps: data.steps.filter((s) => (s.route ?? "") === route),
    };
  });
  const multiple = sections.length > 1;

  let number = 0;
  const renderSteps = (steps: PreviewStep[]) => (
    <ol className="preview-steps">
      {steps.map((step, index) => {
        number += 1;
        const d = step.status === "ok" ? describe(step) : { lines: [] as string[] };
        // Steps that share a parallel group with a neighbour run at the same time.
        const group = step.parallel_group;
        const together =
          Boolean(group) &&
          (steps[index - 1]?.parallel_group === group || steps[index + 1]?.parallel_group === group);
        return (
          <li className={"preview-step " + step.status} key={index}>
            <div className="preview-step-head">
              <span className="preview-step-no">{number}</span>
              <strong>{step.label || kind(step)}</strong>
              <span className="component-type">{kind(step)}</span>
              {together && <span className="preview-tag">at the same time</span>}
              <span className={"status-badge " + (stopsHere(step) ? "failed" : step.status === "ok" ? "succeeded" : step.status)}>
                {stopsHere(step) ? "route stops here" : STATUS_TEXT[step.status]}
              </span>
            </div>
            {d.lines.map((line, i) => (
              <p className="preview-line mono" key={i}>{line}</p>
            ))}
            {"detail" in d && d.detail && (
              <details className="timeline-output">
                <summary>{d.detail.title}</summary>
                <pre>{d.detail.body}</pre>
              </details>
            )}
            {step.status === "skipped" && step.reason && <p className="muted">{step.reason}</p>}
            {step.status === "failed" && step.error && <p className="err">{step.error}</p>}
          </li>
        );
      })}
    </ol>
  );

  return (
    <div className="preview">
      <div className={data.matched ? "preview-trigger yes" : "preview-trigger no"}>
        <strong>
          {data.matched
            ? "The trigger matches this file, so the workflow would start."
            : "The trigger does not match this file, so the workflow would not start."}
        </strong>
        {data.triggers.map((t, i) => (
          <span className="muted" key={i}>
            {(t.label || t.type) + ": " + t.reason}
          </span>
        ))}
        {!data.matched && (
          <span className="muted">The steps below show what they would do if it did.</span>
        )}
      </div>

      {data.steps.length === 0 ? (
        <p className="muted">There are no steps after the trigger.</p>
      ) : multiple ? (
        sections.map((section, index) => (
          <section className="preview-route" key={section.route || "default"}>
            <h3>
              Route {index + 1} <span className="muted">– if {section.title}</span>
            </h3>
            {renderSteps(section.steps)}
          </section>
        ))
      ) : (
        renderSteps(sections[0]?.steps ?? [])
      )}

      <p className="muted preview-foot">
        Preview only: no file was moved, copied or deleted, and no message was sent.
      </p>
    </div>
  );
}

