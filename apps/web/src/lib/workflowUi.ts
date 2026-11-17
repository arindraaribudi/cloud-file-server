// Shared naming + description helpers for the workflow canvas and summary.

export const CONDITION_KEY = "process.condition";

export const DEFAULT_CONDITION: Record<string, unknown> = {
  path_target: "basename",
  path_regex: "",
  mime_patterns: [],
  mime_regex: "",
  negate: false,
};

const UI_NAMES: Record<string, string> = {
  "process.file.rename": "Rename file",
  "process.file.remove": "Delete file",
  "output.file.copy": "Copy to folder",
  "output.notify.webhook": "Webhook / API call",
  "output.notify.email": "Send email",
};

export function uiName(key: string, fallback: string): string {
  return UI_NAMES[key] ?? fallback;
}

// Output components of the same kind are shown together in one box.
export const GROUPS = [
  { id: "group-copy", key: "output.file.copy", type: "groupcopy", title: "Copy to folders" },
  { id: "group-webhook", key: "output.notify.webhook", type: "groupwebhook", title: "Webhook / API calls" },
  { id: "group-email", key: "output.notify.email", type: "groupemail", title: "Email" },
] as const;

/** Title of an action box; says how its steps run once there is more than one. */
export function groupTitle(title: string, parallel: boolean, count: number): string {
  if (count < 2) return title;
  return title + (parallel ? " · at the same time" : " · one by one");
}

/** Label of a Condition box; a collapsed route also says how many steps it holds. */
export function boxName(config: Record<string, unknown>, collapsedCount?: number): string {
  const base = conditionTitle(config);
  if (collapsedCount === undefined) return base;
  return base + " · " + collapsedCount + (collapsedCount === 1 ? " step" : " steps");
}

function str(v: unknown): string {
  return typeof v === "string" ? v.trim() : "";
}

function strings(v: unknown): string[] {
  return Array.isArray(v) ? v.filter((x): x is string => typeof x === "string" && x.trim() !== "") : [];
}

export function isTrivialCondition(config: Record<string, unknown>): boolean {
  return (
    !str(config.path_regex) &&
    strings(config.mime_patterns).length === 0 &&
    !str(config.mime_regex) &&
    config.negate !== true
  );
}

function conditionParts(config: Record<string, unknown>): string[] {
  const parts: string[] = [];
  const target = config.path_target === "full_path" ? "path" : "name";
  if (str(config.path_regex)) parts.push(target + " matches " + str(config.path_regex));
  const mimes = strings(config.mime_patterns);
  if (mimes.length) parts.push("type is " + mimes.join(" or "));
  if (str(config.mime_regex)) parts.push("type matches " + str(config.mime_regex));
  return parts;
}

/** Plain-language description of a condition, e.g. "name matches ^invoice and type is application/pdf". */
export function describeCondition(config: Record<string, unknown>): string {
  const parts = conditionParts(config);
  if (parts.length === 0) return config.negate === true ? "never (everything is excluded)" : "any file";
  const text = parts.join(" and ");
  return config.negate === true ? "NOT (" + text + ")" : text;
}

/** Short label drawn on the Condition box; long conditions collapse to a filter count. */
export function conditionTitle(config: Record<string, unknown>): string {
  const full = describeCondition(config);
  if (full.length <= 34) return "Condition: " + full;
  const n = conditionParts(config).length;
  return "Condition: " + n + (n === 1 ? " filter" : " filters") + (config.negate === true ? " (NOT)" : "");
}
