import { useId, useState } from "react";
import type { JsonSchema } from "../../lib/workflows";
import { UserSelect } from "./UserSelect";

type SchemaFormProps = {
  schema: JsonSchema;
  value: Record<string, unknown>;
  onChange: (value: Record<string, unknown>) => void;
  disabled?: boolean;
};

function fieldLabel(field: JsonSchema, key: string) {
  return field.title ?? key.replaceAll("_", " ");
}

function isLongText(key: string) {
  return /template|body|subject|path|regex|pattern|replacement|description/i.test(key);
}

export function SchemaForm({ schema, value, onChange, disabled }: SchemaFormProps) {
  const uid = useId();
  const properties = schema.properties ?? {};
  const entries = Object.entries(properties);
  const required = new Set(schema.required ?? []);

  if (entries.length === 0) {
    return <p className="muted">No configuration fields.</p>;
  }

  function set(key: string, next: unknown) {
    onChange({ ...value, [key]: next });
  }

  return (
    <div className="schema-form">
      {entries.map(([key, field]) => {
        const id = uid + "-" + key;
        const label = fieldLabel(field, key);
        const current = value[key] !== undefined ? value[key] : field.default;
        const isRequired = required.has(key);
        const description = field.description ? <p className="field-help">{field.description}</p> : null;

        if (field.type === "boolean") {
          return (
            <div className="schema-field schema-check" key={key}>
              <label htmlFor={id}>
                <input
                  id={id}
                  type="checkbox"
                  checked={Boolean(current)}
                  disabled={disabled}
                  onChange={(e) => set(key, e.target.checked)}
                />
                {" "}{label}
              </label>
              {description}
            </div>
          );
        }

        if (field["x-ref"] === "user") {
          return (
            <div className="schema-field" key={key}>
              <label htmlFor={id}>{label}{isRequired ? " *" : ""}</label>
              <UserSelect
                id={id}
                value={typeof current === "string" ? current : ""}
                emptyLabel={isRequired ? undefined : field["x-empty-label"] ?? "(none)"}
                disabled={disabled}
                onChange={(next) => set(key, next)}
              />
              {description}
            </div>
          );
        }

        if (field.enum && field.enum.length > 0) {
          return (
            <div className="schema-field" key={key}>
              <label htmlFor={id}>{label}{isRequired ? " *" : ""}</label>
              <select
                id={id}
                value={typeof current === "string" ? current : ""}
                disabled={disabled}
                onChange={(e) => set(key, e.target.value)}
              >
                {!isRequired && <option value="">(none)</option>}
                {field.enum.map((option) => (
                  <option key={option} value={option}>{option}</option>
                ))}
              </select>
              {description}
            </div>
          );
        }

        if (field.type === "array") {
          const itemType = field.items?.type;
          if (itemType === "string") {
            const list = Array.isArray(current)
              ? current.filter((item): item is string => typeof item === "string")
              : [];
            return (
              <div className="schema-field" key={key}>
                <label htmlFor={id}>{label}{isRequired ? " *" : ""}</label>
                <input
                  id={id}
                  type="text"
                  value={list.join(", ")}
                  placeholder="comma-separated"
                  disabled={disabled}
                  onChange={(e) => set(key, e.target.value.split(",").map((s) => s.trim()).filter(Boolean))}
                />
                {description}
              </div>
            );
          }
          return (
            <JsonField
              key={key}
              id={id}
              label={label}
              value={current}
              required={isRequired}
              disabled={disabled}
              onChange={(next) => set(key, next)}
            />
          );
        }

        if (field.type === "object" || field.additionalProperties) {
          return (
            <JsonField
              key={key}
              id={id}
              label={label}
              value={current}
              required={isRequired}
              disabled={disabled}
              onChange={(next) => set(key, next)}
            />
          );
        }

        if (field.type === "number" || field.type === "integer") {
          return (
            <div className="schema-field" key={key}>
              <label htmlFor={id}>{label}{isRequired ? " *" : ""}</label>
              <input
                id={id}
                type="number"
                value={typeof current === "number" ? current : ""}
                disabled={disabled}
                onChange={(e) => set(key, e.target.value === "" ? undefined : Number(e.target.value))}
              />
              {description}
            </div>
          );
        }

        if (field.type === "string" || field.type === undefined) {
          const text = typeof current === "string" ? current : current == null ? "" : String(current);
          return (
            <div className="schema-field" key={key}>
              <label htmlFor={id}>{label}{isRequired ? " *" : ""}</label>
              {isLongText(key) ? (
                <textarea
                  id={id}
                  value={text}
                  rows={3}
                  disabled={disabled}
                  onChange={(e) => set(key, e.target.value)}
                />
              ) : (
                <input
                  id={id}
                  type="text"
                  value={text}
                  disabled={disabled}
                  onChange={(e) => set(key, e.target.value)}
                />
              )}
              {description}
            </div>
          );
        }

        return (
          <JsonField
            key={key}
            id={id}
            label={label}
            value={current}
            required={isRequired}
            disabled={disabled}
            onChange={(next) => set(key, next)}
          />
        );
      })}
    </div>
  );
}

function JsonField({
  id,
  label,
  value,
  required,
  disabled,
  onChange,
}: {
  id: string;
  label: string;
  value: unknown;
  required?: boolean;
  disabled?: boolean;
  onChange: (value: unknown) => void;
}) {
  const [text, setText] = useState(() => JSON.stringify(value ?? {}, null, 2));
  const [invalid, setInvalid] = useState(false);

  return (
    <div className="schema-field">
      <label htmlFor={id}>{label}{required ? " *" : ""}</label>
      <textarea
        id={id}
        rows={4}
        value={text}
        disabled={disabled}
        aria-invalid={invalid}
        onChange={(e) => {
          setText(e.target.value);
          try {
            onChange(JSON.parse(e.target.value));
            setInvalid(false);
          } catch {
            setInvalid(true);
          }
        }}
      />
      {invalid && <p className="err">Invalid JSON</p>}
    </div>
  );
}
