import { useEffect, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import type { FTPUser } from "../lib/api";
import { listFolders } from "../lib/api";
import { generatePassword, validatePassword } from "../lib/password";

export type UserFormValues = {
  username: string;
  root_folder: string;
  password: string;
  enabled: boolean;
};

function FolderCombobox({
  id,
  label,
  prefix,
  value,
  onChange,
  options,
  placeholder,
  required,
  disabled,
  note,
}: {
  id: string;
  label: string;
  prefix?: string;
  value: string;
  onChange: (v: string) => void;
  options: string[];
  placeholder?: string;
  required?: boolean;
  disabled?: boolean;
  note?: string;
}) {
  const [open, setOpen] = useState(false);
  const [highlight, setHighlight] = useState(0);
  const [prefixWidth, setPrefixWidth] = useState(0);
  const boxRef = useRef<HTMLDivElement | null>(null);
  const prefixRef = useRef<HTMLSpanElement | null>(null);

  useEffect(() => {
    setPrefixWidth(prefixRef.current?.offsetWidth ?? 0);
  }, [prefix]);

  const trimmed = value.replace(/\/+$/, "");
  const matches = trimmed === ""
    ? options
    : options.filter((n) => n.toLowerCase().includes(trimmed.toLowerCase()));
  const showList = open && !disabled && matches.length > 0;

  useEffect(() => { setHighlight(0); }, [value, open]);

  useEffect(() => {
    function onDocClick(e: MouseEvent) {
      if (boxRef.current && !boxRef.current.contains(e.target as Node)) setOpen(false);
    }
    document.addEventListener("mousedown", onDocClick);
    return () => document.removeEventListener("mousedown", onDocClick);
  }, []);

  function pick(name: string) {
    onChange(name.replace(/^\/+/, ""));
    setOpen(false);
  }

  return (
    <div className="field">
      <label htmlFor={id}>{label}</label>
      <div className="combobox" ref={boxRef}>
        {prefix && <span className="combobox-prefix" ref={prefixRef}>{prefix}</span>}
        <input
          id={id}
          type="text"
          value={value}
          disabled={disabled}
          style={prefix ? { paddingLeft: prefixWidth + 16 } : undefined}
          onChange={(e) => onChange(e.target.value.replace(/^\/+/, ""))}
          onFocus={() => setOpen(true)}
          onKeyDown={(e) => {
            if (!showList) {
              if (e.key === "ArrowDown") setOpen(true);
              return;
            }
            if (e.key === "ArrowDown") {
              e.preventDefault();
              setHighlight((i) => Math.min(i + 1, matches.length - 1));
            } else if (e.key === "ArrowUp") {
              e.preventDefault();
              setHighlight((i) => Math.max(i - 1, 0));
            } else if (e.key === "Enter") {
              e.preventDefault();
              const p = matches[highlight];
              if (p) pick(p);
            } else if (e.key === "Escape") {
              setOpen(false);
            }
          }}
          placeholder={placeholder}
          required={required}
          autoComplete="off"
          role="combobox"
          aria-expanded={showList}
          aria-controls={`${id}-options`}
          aria-autocomplete="list"
        />
        <button
          type="button"
          className="combobox-caret"
          aria-label={`Show ${label.toLowerCase()} options`}
          onClick={() => setOpen((o) => !o)}
          tabIndex={-1}
          disabled={disabled}
        >
          ▾
        </button>
        {showList && (
          <ul id={`${id}-options`} role="listbox" className="combobox-menu">
            {matches.map((name, i) => (
              <li
                key={name}
                role="option"
                aria-selected={i === highlight}
                className={i === highlight ? "is-active" : ""}
                onMouseDown={(e) => { e.preventDefault(); pick(name); }}
                onMouseEnter={() => setHighlight(i)}
              >
                {name}
              </li>
            ))}
          </ul>
        )}
      </div>
      {note && <p className="form-note">{note}</p>}
    </div>
  );
}

export function UserForm({
  mode,
  initial,
  submitting,
  error,
  onSubmit,
  onCancel,
}: {
  mode: "create" | "edit";
  initial?: FTPUser;
  submitting: boolean;
  error?: string | null;
  onSubmit: (values: UserFormValues) => void;
  onCancel: () => void;
}) {
  const initialSegments = (initial?.root_folder ?? "").replace(/^\/+/, "").split("/").filter(Boolean);

  const [username, setUsername] = useState(initial?.username ?? "");
  const [rootFolder, setRootFolder] = useState(initialSegments[0] ?? "");
  const [subFolder, setSubFolder] = useState(initialSegments.slice(1).join("/"));
  const [password, setPassword] = useState("");
  const [showPassword, setShowPassword] = useState(false);
  const [enabled, setEnabled] = useState(initial?.enabled ?? true);

  const trimmedRoot = rootFolder.replace(/\/+$/, "");
  const trimmedSub = subFolder.replace(/\/+$/, "");
  const combinedPath = trimmedRoot + (trimmedSub ? "/" + trimmedSub : "");

  const prevRootRef = useRef(trimmedRoot);
  useEffect(() => {
    if (prevRootRef.current !== trimmedRoot) {
      prevRootRef.current = trimmedRoot;
      setSubFolder("");
    }
  }, [trimmedRoot]);

  const rootFoldersQ = useQuery({
    queryKey: ["folders"],
    queryFn: () => listFolders(),
    retry: false,
  });
  const rootNames = (rootFoldersQ.data ?? []).map((f) => f.name.replace(/\/+$/, ""));
  const willCreateRoot = trimmedRoot !== "" && !rootNames.includes(trimmedRoot);

  const subFoldersQ = useQuery({
    queryKey: ["folders", trimmedRoot],
    queryFn: () => listFolders(trimmedRoot),
    enabled: trimmedRoot !== "",
    retry: false,
  });
  const subNames = (subFoldersQ.data ?? []).map((f) => f.name.replace(/\/+$/, ""));
  const willCreateSub = trimmedSub !== "" && !subNames.includes(trimmedSub);

  const passwordError = mode === "create" ? validatePassword(password) : null;

  function submit(e: React.FormEvent) {
    e.preventDefault();
    onSubmit({ username, root_folder: "/" + combinedPath, password, enabled });
  }

  return (
    <form className="form-card" onSubmit={submit}>
      <span className="eyebrow">{mode === "create" ? "New user" : "Edit user"}</span>
      <h2>{mode === "create" ? "Add user" : `Edit ${initial?.username}`}</h2>

      <div className="field">
        <label htmlFor="username">Username</label>
        <input
          id="username"
          type="text"
          value={username}
          onChange={(e) => setUsername(e.target.value)}
          readOnly={mode === "edit"}
          autoFocus={mode === "create"}
          required
        />
      </div>

      <FolderCombobox
        id="root_folder"
        label="Root folder"
        prefix="/"
        value={rootFolder}
        onChange={setRootFolder}
        options={rootNames}
        required
        note={willCreateRoot ? `Will create new folder "/${trimmedRoot}" on save.` : undefined}
      />

      <FolderCombobox
        id="sub_folder"
        label="Subfolder (optional)"
        prefix={trimmedRoot ? `/${trimmedRoot}/` : "/"}
        value={subFolder}
        onChange={setSubFolder}
        options={subNames}
        placeholder={trimmedRoot ? undefined : "select root folder first"}
        disabled={!trimmedRoot}
        note={willCreateSub ? `Will create new folder "/${combinedPath}" on save.` : undefined}
      />

      {mode === "create" && (
        <div className="field">
          <label htmlFor="password">Initial password</label>
          <input
            id="password"
            type={showPassword ? "text" : "password"}
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            required
          />
          <div className="form-actions">
            <button
              type="button"
              className="btn btn-ghost"
              onClick={() => {
                setPassword(generatePassword());
                setShowPassword(true);
              }}
            >
              Generate
            </button>
            <button type="button" className="btn btn-ghost" onClick={() => setShowPassword((s) => !s)}>
              {showPassword ? "Hide" : "Show"}
            </button>
          </div>
          {password && passwordError && <p className="err">{passwordError}</p>}
        </div>
      )}

      <div className="switch-row">
        <span className="switch">
          <input type="checkbox" checked={enabled} onChange={(e) => setEnabled(e.target.checked)} />
          <span className="track" />
          <span className="thumb" />
        </span>
        <label>{enabled ? "Enabled" : "Disabled"}</label>
      </div>

      {error && <p className="err">{error}</p>}

      <div className="form-actions">
        <button type="submit" className="btn btn-primary" disabled={submitting || !!passwordError}>
          {submitting ? "Saving..." : mode === "create" ? "Add user" : "Save changes"}
        </button>
        <button type="button" className="btn btn-ghost" onClick={onCancel}>
          Cancel
        </button>
      </div>

      {rootFoldersQ.isError && (
        <div className="modal-backdrop" role="dialog" aria-modal="true" aria-labelledby="cos-err-title">
          <div className="modal-dialog">
            <h3 id="cos-err-title">Can't load folders</h3>
            <p className="modal-body">
              Could not load folder suggestions: {String(rootFoldersQ.error?.message ?? rootFoldersQ.error)}
            </p>
            <div className="form-actions">
              <button type="button" className="btn btn-ghost" onClick={() => rootFoldersQ.refetch()}>
                Retry
              </button>
              <button type="button" className="btn btn-primary" onClick={onCancel}>
                Close
              </button>
            </div>
          </div>
        </div>
      )}
    </form>
  );
}
