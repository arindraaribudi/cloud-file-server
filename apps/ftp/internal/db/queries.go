package db

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("not found")

const ftpUserColumns = "id, username, password_hash, root_folder, cos_bucket, COALESCE(cos_region, ''), enabled, allow_active_mode, refuse_overwrite, max_sessions, created_at, updated_at, deleted_at"

type FTPUser struct {
	ID              int64      `json:"id"`
	Username        string     `json:"username"`
	PasswordHash    string     `json:"-"`
	RootFolder      string     `json:"root_folder"`
	COSBucket       string     `json:"cos_bucket"`
	COSRegion       string     `json:"cos_region"`
	Enabled         bool       `json:"enabled"`
	AllowActiveMode bool       `json:"allow_active_mode"`
	RefuseOverwrite bool       `json:"refuse_overwrite"`
	MaxSessions     int        `json:"max_sessions"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	DeletedAt       *time.Time `json:"deleted_at,omitempty"`
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
		INSERT INTO ftp_users (username, password_hash, root_folder, cos_bucket, cos_region, enabled, allow_active_mode, refuse_overwrite, max_sessions)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id`,
		u.Username, u.PasswordHash, u.RootFolder, u.COSBucket, nilIfEmpty(u.COSRegion),
		u.Enabled, u.AllowActiveMode, u.RefuseOverwrite, u.MaxSessions).Scan(&id)
	return id, err
}

func GetFTPUserByUsername(ctx context.Context, pool *pgxpool.Pool, name string) (*FTPUser, error) {
	u := &FTPUser{}
	err := pool.QueryRow(ctx, `
		SELECT `+ftpUserColumns+`
		FROM ftp_users WHERE username=$1 AND deleted_at IS NULL`, name).
		Scan(&u.ID, &u.Username, &u.PasswordHash, &u.RootFolder, &u.COSBucket, &u.COSRegion,
			&u.Enabled, &u.AllowActiveMode, &u.RefuseOverwrite, &u.MaxSessions,
			&u.CreatedAt, &u.UpdatedAt, &u.DeletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return u, err
}

func UpdateFTPUser(ctx context.Context, pool *pgxpool.Pool, u *FTPUser) error {
	_, err := pool.Exec(ctx, `
		UPDATE ftp_users SET root_folder=$2, cos_bucket=$3, cos_region=$4, enabled=$5,
			allow_active_mode=$6, refuse_overwrite=$7, max_sessions=$8, updated_at=now()
		WHERE id=$1 AND deleted_at IS NULL`,
		u.ID, u.RootFolder, u.COSBucket, nilIfEmpty(u.COSRegion), u.Enabled,
		u.AllowActiveMode, u.RefuseOverwrite, u.MaxSessions)
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
			&u.CreatedAt, &u.UpdatedAt, &u.DeletedAt); err != nil {
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