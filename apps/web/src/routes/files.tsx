import { useQuery } from "@tanstack/react-query";
import { Link, useParams, useSearch } from "@tanstack/react-router";
import { listFiles, type FileEntry } from "../lib/api";

export function Files() {
  const { userId } = useParams({ from: "/_authed/files/$userId" });
  const { path } = useSearch({ from: "/_authed/files/$userId" });
  const prefix = path ?? "";

  const q = useQuery({
    queryKey: ["files", userId, prefix],
    queryFn: () => listFiles(userId, prefix),
  });

  const segments = prefix.split("/").filter(Boolean);

  return (
    <section>
      <div className="page-head">
        <span className="eyebrow">Browse files</span>
        <h1>Files</h1>
        <div className="rule" />
      </div>

      <nav className="ledger-toolbar">
        <Link to="/files/$userId" params={{ userId }} search={{ path: undefined }}>
          root
        </Link>
        {segments.map((seg, i) => {
          const upto = segments.slice(0, i + 1).join("/");
          return (
            <span key={upto}>
              {" / "}
              <Link to="/files/$userId" params={{ userId }} search={{ path: upto }}>
                {seg}
              </Link>
            </span>
          );
        })}
      </nav>

      {q.isPending && <p>loading...</p>}
      {q.error && <p className="err">{String(q.error)}</p>}

      {q.data && (
        <div className="ledger">
          <table>
            <thead>
              <tr>
                <th>Name</th>
                <th>Size</th>
                <th>Modified</th>
              </tr>
            </thead>
            <tbody>
              {q.data.entries.map((e: FileEntry) => (
                <tr key={e.name}>
                  <td>
                    {e.isDir ? (
                      <Link
                        to="/files/$userId"
                        params={{ userId }}
                        search={{ path: joinPath(prefix, e.name) }}
                      >
                        {e.name}/
                      </Link>
                    ) : (
                      <a
                        className="mono"
                        href={`/api/v1/files/${userId}/download?path=${encodeURIComponent(
                          joinPath(prefix, e.name),
                        )}`}
                        download
                      >
                        {e.name}
                      </a>
                    )}
                  </td>
                  <td className="mono">{e.isDir ? "—" : formatBytes(e.size)}</td>
                  <td className="mono">{new Date(e.modified).toLocaleString()}</td>
                </tr>
              ))}
              {q.data.entries.length === 0 && (
                <tr>
                  <td colSpan={3} className="empty-cell">
                    Empty folder.
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}

function joinPath(prefix: string, name: string): string {
  return prefix ? `${prefix}/${name}` : name;
}

function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  if (n < 1024 * 1024 * 1024) return `${(n / 1024 / 1024).toFixed(1)} MB`;
  return `${(n / 1024 / 1024 / 1024).toFixed(1)} GB`;
}
