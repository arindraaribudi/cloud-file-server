import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { useMemo, useState } from "react";
import {
  createColumnHelper,
  flexRender,
  getCoreRowModel,
  getFilteredRowModel,
  getPaginationRowModel,
  getSortedRowModel,
  useReactTable,
  type ColumnFiltersState,
  type SortingState,
} from "@tanstack/react-table";
import { api, deleteUser, updateUser, type FTPUser } from "../lib/api";
import { Stamp } from "../components/Stamp";

function FolderIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2Z" />
    </svg>
  );
}

function EditIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <path d="M17 3a2.85 2.83 0 1 1 4 4L7.5 20.5 2 22l1.5-5.5Z" />
    </svg>
  );
}

function ResetIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <rect x="4" y="11" width="16" height="9" rx="2" />
      <path d="M8 11V7a4 4 0 0 1 8 0v4" />
    </svg>
  );
}

function KeyIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <circle cx="8" cy="15" r="4" />
      <path d="m10.5 12.5 8-8M16 5l3 3m-6.5 1.5L15 12" />
    </svg>
  );
}

function PowerIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <path d="M12 2v8" />
      <path d="M6.5 6.5a8 8 0 1 0 11 0" />
    </svg>
  );
}

function TrashIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <path d="M3 6h18" />
      <path d="M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2m3 0-1 14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2L4 6" />
      <path d="M10 11v6M14 11v6" />
    </svg>
  );
}

const columnHelper = createColumnHelper<FTPUser>();

export function Users() {
  const qc = useQueryClient();
  const nav = useNavigate();
  const [search, setSearch] = useState("");
  const [sorting, setSorting] = useState<SortingState>([]);
  const [deleteTarget, setDeleteTarget] = useState<FTPUser | null>(null);
  const [disableTarget, setDisableTarget] = useState<FTPUser | null>(null);

  const q = useQuery({
    queryKey: ["users"],
    queryFn: () => api<FTPUser[]>("/api/v1/users"),
  });

  const toggleEnabled = useMutation({
    mutationFn: (u: FTPUser) =>
      updateUser(u.username, {
        root_folder: u.root_folder,
        enabled: !u.enabled,
        ftp_enabled: u.ftp_enabled,
        sftp_enabled: u.sftp_enabled,
      }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["users"] }),
  });

  const toggleProtocol = useMutation({
    mutationFn: (p: { u: FTPUser; protocol: "ftp_enabled" | "sftp_enabled" }) =>
      updateUser(p.u.username, {
        root_folder: p.u.root_folder,
        enabled: p.u.enabled,
        ftp_enabled: p.protocol === "ftp_enabled" ? !p.u.ftp_enabled : p.u.ftp_enabled,
        sftp_enabled: p.protocol === "sftp_enabled" ? !p.u.sftp_enabled : p.u.sftp_enabled,
      }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["users"] }),
  });

  const removeUser = useMutation({
    mutationFn: (username: string) => deleteUser(username),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["users"] });
      setDeleteTarget(null);
    },
  });

  const columnFilters: ColumnFiltersState = useMemo(
    () => (search ? [{ id: "username", value: search }] : []),
    [search],
  );

  const columns = useMemo(
    () => [
      columnHelper.accessor("username", {
        header: "Username",
        cell: (info) => <span className="mono">{info.getValue()}</span>,
        filterFn: (row, columnId, value) =>
          String(row.getValue(columnId)).toLowerCase().includes(String(value).toLowerCase()),
      }),
      columnHelper.accessor("root_folder", {
        header: "Root folder",
        cell: (info) => <span className="mono">{info.getValue()}</span>,
      }),
      columnHelper.accessor("enabled", {
        header: "Status",
        cell: (info) => {
          const u = info.row.original;
          return (
            <div className="status-cell">
              <Stamp label={info.getValue() ? "Enabled" : "Disabled"} tone={info.getValue() ? "blue" : "red"} />
              <div className="status-proto">
                <button
                  className={`proto-toggle ${u.ftp_enabled ? "on" : "off"}`}
                  onClick={() => toggleProtocol.mutate({ u, protocol: "ftp_enabled" })}
                  title={u.enabled ? (u.ftp_enabled ? "Disable FTP" : "Enable FTP") : "User is disabled"}
                  disabled={!u.enabled || toggleProtocol.isPending}
                >
                  FTP
                </button>
                <button
                  className={`proto-toggle ${u.sftp_enabled ? "on" : "off"}`}
                  onClick={() => toggleProtocol.mutate({ u, protocol: "sftp_enabled" })}
                  title={u.enabled ? (u.sftp_enabled ? "Disable SFTP" : "Enable SFTP") : "User is disabled"}
                  disabled={!u.enabled || toggleProtocol.isPending}
                >
                  SFTP
                </button>
              </div>
            </div>
          );
        },
      }),
      columnHelper.accessor("created_at", {
        header: "Created",
        cell: (info) => <span className="mono">{info.getValue()}</span>,
      }),
      columnHelper.accessor("last_login", {
        header: "Last login",
        cell: (info) => <span className="mono">{info.getValue() ?? "Never"}</span>,
      }),
      columnHelper.display({
        id: "actions",
        header: "",
        cell: (info) => {
          const u = info.row.original;
          return (
            <div className="row-actions">
              <button
                aria-label={`Browse files for ${u.username}`}
                title="Browse files"
                onClick={() => nav({ to: "/files/$userId", from: "/users", params: { userId: String(u.id) } })}
              >
                <FolderIcon />
              </button>
              <button
                aria-label={`Edit ${u.username}`}
                title="Edit"
                onClick={() => nav({ to: "/users/$username/edit", from: "/users", params: { username: u.username } })}
              >
                <EditIcon />
              </button>
              <button
                aria-label={`Reset password for ${u.username}`}
                title="Reset password"
                onClick={() =>
                  nav({ to: "/users/$username/password", from: "/users", params: { username: u.username } })
                }
              >
                <ResetIcon />
              </button>
              <button
                aria-label={`Set SFTP key for ${u.username}`}
                title="Set SFTP public key"
                onClick={() =>
                  nav({ to: "/users/$username/sftp-key", from: "/users", params: { username: u.username } })
                }
              >
                <KeyIcon />
              </button>
              <button
                aria-label={u.enabled ? `Disable ${u.username}` : `Enable ${u.username}`}
                title={u.enabled ? "Disable" : "Enable"}
                className={u.enabled ? "danger" : ""}
                onClick={() => {
                  if (u.enabled) setDisableTarget(u);
                  else toggleEnabled.mutate(u);
                }}
                disabled={toggleEnabled.isPending}
              >
                <PowerIcon />
              </button>
              <button aria-label={`Delete ${u.username}`} title="Delete" className="danger" onClick={() => setDeleteTarget(u)}>
                <TrashIcon />
              </button>
            </div>
          );
        },
      }),
    ],
    [nav, toggleEnabled, toggleProtocol],
  );

  const table = useReactTable({
    data: q.data ?? [],
    columns,
    state: { sorting, columnFilters },
    onSortingChange: setSorting,
    getCoreRowModel: getCoreRowModel(),
    getSortedRowModel: getSortedRowModel(),
    getFilteredRowModel: getFilteredRowModel(),
    getPaginationRowModel: getPaginationRowModel(),
    initialState: { pagination: { pageSize: 10 } },
  });

  return (
    <section>
      <div className="page-head">
        <span className="eyebrow">Manage accounts</span>
        <h1>Users</h1>
        <div className="rule" />
      </div>

      <div className="ledger-toolbar">
        <input
          type="search"
          placeholder="Search username..."
          value={search}
          onChange={(e) => setSearch(e.target.value)}
        />
        <Link to="/users/new" className="btn btn-primary">
          + New user
        </Link>
      </div>

      {q.isPending && <p>loading...</p>}
      {q.error && <p className="err">{String(q.error)}</p>}

      {q.data && (
        <>
          <div className="ledger">
            <table>
              <thead>
                {table.getHeaderGroups().map((hg) => (
                  <tr key={hg.id}>
                    {hg.headers.map((header) => {
                      const sortable = header.column.getCanSort();
                      const sortDir = header.column.getIsSorted();
                      return (
                        <th
                          key={header.id}
                          onClick={sortable ? header.column.getToggleSortingHandler() : undefined}
                          className={sortable ? "sortable" : undefined}
                        >
                          {header.isPlaceholder ? null : flexRender(header.column.columnDef.header, header.getContext())}
                          {sortDir === "asc" && " ▲"}
                          {sortDir === "desc" && " ▼"}
                        </th>
                      );
                    })}
                  </tr>
                ))}
              </thead>
              <tbody>
                {table.getRowModel().rows.map((row) => (
                  <tr key={row.id}>
                    {row.getVisibleCells().map((cell) => (
                      <td key={cell.id}>{flexRender(cell.column.columnDef.cell, cell.getContext())}</td>
                    ))}
                  </tr>
                ))}
                {table.getRowModel().rows.length === 0 && (
                  <tr>
                    <td colSpan={columns.length} className="empty-cell">
                      No users found.
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>

          <div className="pagination">
            <button className="btn btn-ghost" onClick={() => table.previousPage()} disabled={!table.getCanPreviousPage()}>
              Previous
            </button>
            <span>
              Page {table.getState().pagination.pageIndex + 1} of {Math.max(table.getPageCount(), 1)}
            </span>
            <button className="btn btn-ghost" onClick={() => table.nextPage()} disabled={!table.getCanNextPage()}>
              Next
            </button>
          </div>
        </>
      )}

      {deleteTarget && (
        <div className="modal-backdrop" role="dialog" aria-modal="true" aria-labelledby="delete-user-title">
          <div className="modal-dialog">
            <h3 id="delete-user-title">Delete user</h3>
            <p className="modal-body">
              Permanently delete FTP user <strong>{deleteTarget.username}</strong>? This cannot be undone.
            </p>
            {removeUser.isError && <p className="err">{String(removeUser.error)}</p>}
            <div className="form-actions">
              <button type="button" className="btn btn-ghost" onClick={() => setDeleteTarget(null)} disabled={removeUser.isPending}>
                Cancel
              </button>
              <button
                type="button"
                className="btn btn-danger"
                onClick={() => removeUser.mutate(deleteTarget.username)}
                disabled={removeUser.isPending}
              >
                {removeUser.isPending ? "Deleting..." : "Delete"}
              </button>
            </div>
          </div>
        </div>
      )}

      {disableTarget && (
        <div className="modal-backdrop" role="dialog" aria-modal="true" aria-labelledby="disable-user-title">
          <div className="modal-dialog">
            <h3 id="disable-user-title">Disable user</h3>
            <p className="modal-body">
              Disable FTP user <strong>{disableTarget.username}</strong>? They won't be able to log in via FTP or SFTP until re-enabled.
            </p>
            {toggleEnabled.isError && <p className="err">{String(toggleEnabled.error)}</p>}
            <div className="form-actions">
              <button type="button" className="btn btn-ghost" onClick={() => setDisableTarget(null)} disabled={toggleEnabled.isPending}>
                Cancel
              </button>
              <button
                type="button"
                className="btn btn-danger"
                onClick={() => {
                  toggleEnabled.mutate(disableTarget, {
                    onSuccess: () => setDisableTarget(null),
                  });
                }}
                disabled={toggleEnabled.isPending}
              >
                {toggleEnabled.isPending ? "Disabling..." : "Disable"}
              </button>
            </div>
          </div>
        </div>
      )}
    </section>
  );
}
