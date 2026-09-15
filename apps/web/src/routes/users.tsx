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
      <circle cx="9" cy="9" r="5" />
      <path d="m13 13 7 7m-3-7 3-3-2-2" />
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

  const q = useQuery({
    queryKey: ["users"],
    queryFn: () => api<FTPUser[]>("/api/v1/users"),
  });

  const toggleEnabled = useMutation({
    mutationFn: (u: FTPUser) => updateUser(u.username, { root_folder: u.root_folder, enabled: !u.enabled }),
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
        cell: (info) => <Stamp label={info.getValue() ? "Enabled" : "Disabled"} tone={info.getValue() ? "blue" : "red"} />,
      }),
      columnHelper.accessor("created_at", {
        header: "Created",
        cell: (info) => <span className="mono">{info.getValue()}</span>,
      }),
      columnHelper.display({
        id: "actions",
        header: "",
        cell: (info) => {
          const u = info.row.original;
          return (
            <div className="row-actions">
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
                aria-label={u.enabled ? `Disable ${u.username}` : `Enable ${u.username}`}
                title={u.enabled ? "Disable" : "Enable"}
                className={u.enabled ? "danger" : ""}
                onClick={() => toggleEnabled.mutate(u)}
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
    [nav, toggleEnabled],
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
    </section>
  );
}
