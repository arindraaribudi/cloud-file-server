import type { ComponentType } from "../../lib/workflows";
import { CONDITION_KEY, GROUPS, describeCondition, uiName } from "../../lib/workflowUi";
import type { EditableComponent } from "./WorkflowDesigner";

type Line = { title: string; text: string };

// A plain-language reading of the workflow, so it can be checked at a glance.
export function WorkflowSummary({
  components,
  catalog,
}: {
  components: EditableComponent[];
  catalog: ComponentType[];
}) {
  const nameOf = (key: string) => uiName(key, catalog.find((t) => t.key === key)?.name ?? key);

  const trigger = components.find((c) => c.stage === "trigger");
  const when = trigger
    ? trigger.type === "trigger.file.remove"
      ? "a file is removed"
      : "a file is uploaded"
    : "(add a trigger)";

  // Steps grouped by route, in the order the routes first appear.
  const routes: string[] = [];
  const body = components.filter((c) => c.stage !== "trigger");
  for (const c of body) {
    const r = c.route ?? "";
    if (!routes.includes(r)) routes.push(r);
  }
  if (routes.length === 0) routes.push("");

  const sections = routes.map((route) => {
    const items = body.filter((c) => (c.route ?? "") === route);
    const process = items.filter((c) => c.stage === "process");
    const condition = process[0]?.type === CONDITION_KEY ? process[0] : undefined;
    const lines: Line[] = [];
    for (const step of process.filter((c) => c !== condition)) {
      lines.push({ title: "Then", text: nameOf(step.type) + (step.label ? " – " + step.label : "") });
    }
    const outputs = items.filter((c) => c.stage === "output");
    for (const group of GROUPS) {
      const members = outputs.filter((c) => c.type === group.key);
      if (members.length === 0) continue;
      const names = members.map((m) => m.label || nameOf(m.type)).join(", ");
      const together = members.length > 1 && members.every((m) => Boolean(m.parallel_group));
      const verb =
        group.key === "output.file.copy"
          ? "Copy to " + members.length + (members.length === 1 ? " folder" : " folders")
          : group.key === "output.notify.webhook"
            ? "Call " + members.length + (members.length === 1 ? " webhook / API" : " webhooks / APIs")
            : "Send " + members.length + (members.length === 1 ? " email" : " emails");
      lines.push({
        title: "Then",
        text: verb + (members.length > 1 ? (together ? " at the same time" : " one by one") : "") + ": " + names,
      });
    }
    return {
      condition: condition ? describeCondition(condition.config) : "any file",
      lines,
    };
  });

  const only = sections.length === 1 ? sections[0] : undefined;

  return (
    <div className="workflow-summary">
      <span className="stage-kicker">In plain words</span>
      <p className="summary-when">
        <b>When</b> {when}
      </p>
      {only ? (
        <ol>
          <li>
            <b>If</b> {only.condition}
          </li>
          {only.lines.map((line, i) => (
            <li key={i}>
              <b>{line.title}</b> {line.text}
            </li>
          ))}
        </ol>
      ) : (
        <ol className="summary-routes">
          {sections.map((section, index) => (
            <li key={index}>
              <b>Route {index + 1}</b> – if {section.condition}
              {section.lines.length > 0 && (
                <ul>
                  {section.lines.map((line, i) => (
                    <li key={i}>
                      <b>{line.title}</b> {line.text}
                    </li>
                  ))}
                </ul>
              )}
            </li>
          ))}
        </ol>
      )}
    </div>
  );
}
