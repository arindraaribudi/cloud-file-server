import { useEffect } from "react";
import { useQuery } from "@tanstack/react-query";
import { listAllUsers, storageBackendLabel } from "../../lib/api";

/**
 * Combo box over the users that exist, used for every workflow field that
 * refers to a user. It never accepts free text, so a workflow can only point
 * at an available user.
 *
 * - `emptyLabel` set: the field is optional and "" is offered as that choice
 *   (e.g. "All users"). Unset: a user is required and the first one is
 *   selected automatically when the value is blank.
 * - A saved value that no longer matches any user stays visible and flagged
 *   "(unavailable)" instead of being silently replaced.
 */
export function UserSelect({
  id,
  value,
  onChange,
  emptyLabel,
  disabled,
}: {
  id?: string;
  value: string;
  onChange: (value: string) => void;
  emptyLabel?: string;
  disabled?: boolean;
}) {
  // Same ["users", ...] prefix as the users page so create/edit/delete there
  // invalidates this list too.
  const q = useQuery({ queryKey: ["users", "all"], queryFn: listAllUsers, staleTime: 30_000 });
  const users = q.data ?? [];
  const known = users.some((u) => u.username === value);
  const required = emptyLabel === undefined;

  useEffect(() => {
    const first = users[0];
    if (required && value === "" && first) onChange(first.username);
  }, [required, value, users, onChange]);

  if (q.isError) {
    return <p className="err">Could not load users: {String(q.error)}</p>;
  }

  return (
    <>
      <select
        id={id}
        value={value}
        disabled={disabled || q.isPending}
        onChange={(e) => onChange(e.target.value)}
      >
        {!required && <option value="">{emptyLabel}</option>}
        {required && value === "" && <option value="">{q.isPending ? "Loading users..." : "No users available"}</option>}
        {value !== "" && !known && !q.isPending && (
          <option value={value}>{value} (unavailable)</option>
        )}
        {value !== "" && !known && q.isPending && <option value={value}>{value}</option>}
        {users.map((u) => (
          <option key={u.id} value={u.username}>
            {u.username}
            {u.enabled ? "" : " (disabled)"}
            {u.storage_backend ? ` · ${storageBackendLabel(u.storage_backend)}` : ""}
          </option>
        ))}
      </select>
      {value !== "" && !known && !q.isPending && (
        <p className="err">User "{value}" no longer exists. Pick an available user.</p>
      )}
    </>
  );
}
