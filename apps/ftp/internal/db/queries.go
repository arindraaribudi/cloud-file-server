package db

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("not found")

const ftpUserColumns = "id, username, password_hash, root_folder, cos_bucket, COALESCE(cos_region, ''), enabled, allow_active_mode, refuse_overwrite, max_sessions, ftp_enabled, sftp_enabled, created_at, updated_at, deleted_at, last_login"

type FTPUser struct {
	ID              int64  `json:"id"`
	Username        string `json:"username"`
	PasswordHash    string `json:"-"`
	RootFolder      string `json:"root_folder"`
	COSBucket       string `json:"cos_bucket"`
	COSRegion       string `json:"cos_region"`
	Enabled         bool   `json:"enabled"`
	AllowActiveMode bool   `json:"allow_active_mode"`
	RefuseOverwrite bool   `json:"refuse_overwrite"`
	MaxSessions     int    `json:"max_sessions"`
	FTPEnabled      bool   `json:"ftp_enabled"`
	SFTPEnabled     bool   `json:"sftp_enabled"`
	// SFTPPublicKey is never included in ftpUserColumns/the general JSON
	// response — fetched only via GetFTPUserPublicKey, which the SFTP
	// touchpoint uses for its pubkey-auth lookup.
	SFTPPublicKey string     `json:"-"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	DeletedAt     *time.Time `json:"deleted_at,omitempty"`
	LastLogin     *time.Time `json:"last_login"`
}

type AdminUser struct {
	ID           int64     `json:"id"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"-"`
	Role         string    `json:"role"`
	CreatedAt    time.Time `json:"created_at"`
}

func CreateFTPUser(ctx context.Context, pool *pgxpool.Pool, u *FTPUser) (int64, error) {
	var id int64
	err := pool.QueryRow(ctx, `
		INSERT INTO ftp_users (username, password_hash, root_folder, cos_bucket, cos_region, enabled, allow_active_mode, refuse_overwrite, max_sessions, ftp_enabled, sftp_enabled)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING id`,
		u.Username, u.PasswordHash, u.RootFolder, u.COSBucket, nilIfEmpty(u.COSRegion),
		u.Enabled, u.AllowActiveMode, u.RefuseOverwrite, u.MaxSessions, u.FTPEnabled, u.SFTPEnabled).Scan(&id)
	return id, err
}

func GetFTPUserByUsername(ctx context.Context, pool *pgxpool.Pool, name string) (*FTPUser, error) {
	u := &FTPUser{}
	err := pool.QueryRow(ctx, `
		SELECT `+ftpUserColumns+`
		FROM ftp_users WHERE username=$1 AND deleted_at IS NULL`, name).
		Scan(&u.ID, &u.Username, &u.PasswordHash, &u.RootFolder, &u.COSBucket, &u.COSRegion,
			&u.Enabled, &u.AllowActiveMode, &u.RefuseOverwrite, &u.MaxSessions,
			&u.FTPEnabled, &u.SFTPEnabled,
			&u.CreatedAt, &u.UpdatedAt, &u.DeletedAt, &u.LastLogin)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return u, err
}

// GetFTPUserByID fetches an FTP user by primary key. Returns ErrNotFound
// if no row matches (including soft-deleted users).
func GetFTPUserByID(ctx context.Context, pool *pgxpool.Pool, id int64) (*FTPUser, error) {
	u := &FTPUser{}
	err := pool.QueryRow(ctx,
		`SELECT `+ftpUserColumns+` FROM ftp_users WHERE id=$1 AND deleted_at IS NULL`, id).
		Scan(&u.ID, &u.Username, &u.PasswordHash, &u.RootFolder, &u.COSBucket,
			&u.COSRegion, &u.Enabled, &u.AllowActiveMode, &u.RefuseOverwrite,
			&u.MaxSessions, &u.FTPEnabled, &u.SFTPEnabled,
			&u.CreatedAt, &u.UpdatedAt, &u.DeletedAt, &u.LastLogin)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return u, nil
}

// GetFTPUserPublicKey fetches the minimal set of fields the SFTP touchpoint's
// public-key auth path needs: identity, access flags, mount root, and the
// stored key. Does not require a password. Returns ErrNotFound if the user
// doesn't exist or is soft-deleted.
func GetFTPUserPublicKey(ctx context.Context, pool *pgxpool.Pool, username string) (*FTPUser, error) {
	u := &FTPUser{}
	err := pool.QueryRow(ctx, `
		SELECT id, username, root_folder, enabled, sftp_enabled, COALESCE(sftp_public_key, '')
		FROM ftp_users WHERE username=$1 AND deleted_at IS NULL`, username).
		Scan(&u.ID, &u.Username, &u.RootFolder, &u.Enabled, &u.SFTPEnabled, &u.SFTPPublicKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return u, err
}

// SetFTPUserSFTPKey stores u's SFTP public key. An empty key clears it
// (falls back to password-only auth for that user).
func SetFTPUserSFTPKey(ctx context.Context, pool *pgxpool.Pool, id int64, key string) error {
	_, err := pool.Exec(ctx, `UPDATE ftp_users SET sftp_public_key=$2, updated_at=now() WHERE id=$1`, id, nilIfEmpty(key))
	return err
}

func UpdateFTPUser(ctx context.Context, pool *pgxpool.Pool, u *FTPUser) error {
	_, err := pool.Exec(ctx, `
		UPDATE ftp_users SET root_folder=$2, cos_bucket=$3, cos_region=$4, enabled=$5,
			allow_active_mode=$6, refuse_overwrite=$7, max_sessions=$8,
			ftp_enabled=$9, sftp_enabled=$10, updated_at=now()
		WHERE id=$1 AND deleted_at IS NULL`,
		u.ID, u.RootFolder, u.COSBucket, nilIfEmpty(u.COSRegion), u.Enabled,
		u.AllowActiveMode, u.RefuseOverwrite, u.MaxSessions, u.FTPEnabled, u.SFTPEnabled)
	return err
}

func SoftDeleteFTPUser(ctx context.Context, pool *pgxpool.Pool, id int64) error {
	_, err := pool.Exec(ctx, `UPDATE ftp_users SET deleted_at=now(), enabled=FALSE WHERE id=$1`, id)
	return err
}

func SetFTPUserPassword(ctx context.Context, pool *pgxpool.Pool, id int64, hash string) error {
	_, err := pool.Exec(ctx, `UPDATE ftp_users SET password_hash=$2, updated_at=now() WHERE id=$1`, id, hash)
	return err
}

func SetFTPUserLastLogin(ctx context.Context, pool *pgxpool.Pool, id int64) error {
	_, err := pool.Exec(ctx, `UPDATE ftp_users SET last_login=now() WHERE id=$1`, id)
	return err
}

func ListFTPUsers(ctx context.Context, pool *pgxpool.Pool, search string, limit, offset int) ([]*FTPUser, error) {
	rows, err := pool.Query(ctx, `
		SELECT `+ftpUserColumns+`
		FROM ftp_users WHERE deleted_at IS NULL AND ($1='' OR username ILIKE '%'||$1||'%')
		ORDER BY id LIMIT $2 OFFSET $3`, search, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*FTPUser{}
	for rows.Next() {
		u := &FTPUser{}
		if err := rows.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.RootFolder, &u.COSBucket, &u.COSRegion,
			&u.Enabled, &u.AllowActiveMode, &u.RefuseOverwrite, &u.MaxSessions,
			&u.FTPEnabled, &u.SFTPEnabled,
			&u.CreatedAt, &u.UpdatedAt, &u.DeletedAt, &u.LastLogin); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func CreateAdminUser(ctx context.Context, pool *pgxpool.Pool, username, hash, role string) (int64, error) {
	var id int64
	err := pool.QueryRow(ctx,
		`INSERT INTO admin_users (username, password_hash, role) VALUES ($1,$2,$3) RETURNING id`,
		username, hash, role).Scan(&id)
	return id, err
}

func GetAdminUserByUsername(ctx context.Context, pool *pgxpool.Pool, name string) (*AdminUser, error) {
	u := &AdminUser{}
	err := pool.QueryRow(ctx,
		`SELECT id, username, password_hash, role, created_at FROM admin_users WHERE username=$1`, name).
		Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return u, err
}

func UpdateAdminUserPassword(ctx context.Context, pool *pgxpool.Pool, username, hash string) error {
	_, err := pool.Exec(ctx, `UPDATE admin_users SET password_hash=$2 WHERE username=$1`, username, hash)
	return err
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
