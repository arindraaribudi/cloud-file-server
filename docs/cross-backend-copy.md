# Cross-backend copy (note)

Status: **known limitation, deferred.** Written when per-user storage backends
(`cos` / `local`) were introduced.

## Current behaviour

The workflow *Copy* output calls `POST /internal/v1/files/copy`
(`apps/ftp/internal/admin/internal_files_api.go`). The handler asks
`core.UserStorage.CopyNative`, which only performs a **server-side** copy when
both users resolve to the **same** backend *and* that backend implements
`core.Copier`:

| Source user | Target user | Path |
|---|---|---|
| COS | COS | native `Copier` (COS server-side copy), no data through the app |
| local | local | `Copier` (file stream on disk) |
| COS | local | **fallback**: `copyAfero` streams the object through the FTP server process |
| local | COS | **fallback** (same as above) |

The fallback is functional but has the limits below.

## Limitations of the streamed fallback

- **Size cap**: files over `maxFallbackCopyBytes` (256 MiB) are rejected with
  `413` / `file too large for in-process copy`.
- **Not atomic**: the target is written in place. A failure midway can leave a
  partial file at the target path (COS uploads are atomic per object; local
  writes are not).
- **Bandwidth/memory**: the bytes flow COS <-> app <-> disk, so large or many
  concurrent copies load the pod; there is no throttling or concurrency limit.
- **No metadata fidelity**: only content is copied (mtime, content-type etc. are
  not preserved across backends).
- **Audit** records `WORKFLOW_COPY` against the *target* user's backend only;
  the source backend is not in the event (`source_username` is in `detail`).
- Overwrite check is a Stat-then-write, so it is racy under concurrent copies
  to the same target.

## Ideas for a proper implementation

1. Stream with `io.Copy` into a temp object/file and rename/commit on success
   (atomic target), removing the size cap (COS multipart upload for large files).
2. Add a `core.Mover`/`Streamer` capability so backends expose
   `Open(ctx, root, path)` / `Put(ctx, root, path, io.Reader, size)` instead of
   going through `afero.Fs`.
3. Record both `source_backend` and `target_backend` in the audit `detail`.
4. Decide whether the UI should warn when a Copy step's destination user is on
   a different backend than the trigger's source (the picker could show the
   backend next to each user - it already does in the label).
5. Same applies to a future "migrate user to another backend" tool, which is
   what changing `storage_backend` after creation would require.
