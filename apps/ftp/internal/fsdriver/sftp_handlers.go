package fsdriver

import (
	"io"
	"os"

	"github.com/pkg/sftp"
	"github.com/spf13/afero"
)

// SFTPHandlers adapts an afero.Fs — the same per-user mount FTP uses (COS,
// local, or the AuditFS wrapper around either) — to github.com/pkg/sftp's
// request-server handler interfaces, so one mount can serve both protocols.
type SFTPHandlers struct {
	fs afero.Fs
}

var (
	_ sftp.FileReader = (*SFTPHandlers)(nil)
	_ sftp.FileWriter = (*SFTPHandlers)(nil)
	_ sftp.FileCmder  = (*SFTPHandlers)(nil)
	_ sftp.FileLister = (*SFTPHandlers)(nil)
)

// NewSFTPHandlers returns an sftp.Handlers bundle backed by fs, for
// sftp.NewRequestServer.
func NewSFTPHandlers(fs afero.Fs) sftp.Handlers {
	h := &SFTPHandlers{fs: fs}
	return sftp.Handlers{
		FileGet:  h,
		FilePut:  h,
		FileCmd:  h,
		FileList: h,
	}
}

func (h *SFTPHandlers) Fileread(r *sftp.Request) (io.ReaderAt, error) {
	f, err := h.fs.Open(r.Filepath)
	if err != nil {
		return nil, err
	}
	return f, nil
}

func (h *SFTPHandlers) Filewrite(r *sftp.Request) (io.WriterAt, error) {
	f, err := h.fs.OpenFile(r.Filepath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, err
	}
	return f, nil
}

func (h *SFTPHandlers) Filecmd(r *sftp.Request) error {
	switch r.Method {
	case "Setstat":
		return nil
	case "Rename":
		return h.fs.Rename(r.Filepath, r.Target)
	case "Rmdir":
		return h.fs.RemoveAll(r.Filepath)
	case "Mkdir":
		return h.fs.MkdirAll(r.Filepath, 0o755)
	case "Remove":
		return h.fs.Remove(r.Filepath)
	default:
		return sftp.ErrSSHFxOpUnsupported
	}
}

func (h *SFTPHandlers) Filelist(r *sftp.Request) (sftp.ListerAt, error) {
	switch r.Method {
	case "List":
		infos, err := afero.ReadDir(h.fs, r.Filepath)
		if err != nil {
			return nil, err
		}
		return listerAt(infos), nil
	case "Stat", "Lstat":
		info, err := h.fs.Stat(r.Filepath)
		if err != nil {
			return nil, err
		}
		return listerAt([]os.FileInfo{info}), nil
	default:
		return nil, sftp.ErrSSHFxOpUnsupported
	}
}

// listerAt implements sftp.ListerAt over an already-fetched slice.
type listerAt []os.FileInfo

func (l listerAt) ListAt(dst []os.FileInfo, offset int64) (int, error) {
	if offset >= int64(len(l)) {
		return 0, io.EOF
	}
	n := copy(dst, l[offset:])
	if n < len(dst) {
		return n, io.EOF
	}
	return n, nil
}
