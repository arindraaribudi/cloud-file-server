package admin

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/example/cos-ftp-server/internal/audit"
	"github.com/example/cos-ftp-server/internal/cos"
	"github.com/example/cos-ftp-server/internal/db"
	"github.com/example/cos-ftp-server/internal/telemetry"
)

type API struct {
	Pool               *pgxpool.Pool
	Sessions           *SessionManager
	Audit              *audit.Logger
	COSBucket          string
	COSRegion          string
	COSClient          *cos.Client
	DefaultRootPrefix  string
	FTPAddress         string
	FTPPublicAddress   string
	oidc               *oidcAuth
}

// New wires the admin API. cosBucket/cosRegion are the storage location
// assigned to newly provisioned FTP users (per-user bucket selection is not
// yet implemented — see main.go's M2 wiring note). cosClient is used to create
// each new user's root folder placeholder; pass nil to skip (e.g. in tests).
// defaultRootPrefix is the bucket-relative prefix the folder-suggestion
// endpoint lists from (e.g. "/t/t/"). Call ConfigureOIDC afterwards to enable SSO login.
func New(pool *pgxpool.Pool, cookieSecure bool, cosBucket, cosRegion string, cosClient *cos.Client, defaultRootPrefix string, auditLog *audit.Logger) *API {
	if defaultRootPrefix == "" {
		defaultRootPrefix = "/t/t/"
	}
	return &API{
		Pool:              pool,
		Sessions:          NewSessionManager(8*time.Hour, cookieSecure),
		Audit:             auditLog,
		COSBucket:         cosBucket,
		COSRegion:         cosRegion,
		COSClient:         cosClient,
		DefaultRootPrefix: defaultRootPrefix,
	}
}

func (a *API) Routes() http.Handler {
	r := chi.NewRouter()
	r.Method("GET", "/metrics", telemetry.Handler())
	r.Post("/api/v1/auth/logout", a.logout)
	r.Get("/api/v1/auth/oidc/authorize", a.oidcAuthorize)
	r.Get(oidcCallbackPath, a.oidcCallback)

	r.Group(func(r chi.Router) {
		r.Use(a.Sessions.Authenticate)
		r.Get("/api/v1/auth/session", a.session)
		r.Get("/api/v1/users", a.listUsers)
		r.Get("/api/v1/folders", a.listFolders)
		r.Get("/api/v1/audit", a.queryAudit)
		r.Get("/api/v1/audit/export", a.exportAuditCSV)
		r.Get("/api/v1/files/{userId}", a.listFilesRoute)
		r.Get("/api/v1/files/{userId}/download", a.downloadFileRoute)

		r.Group(func(r chi.Router) {
			r.Use(a.RequireRole("admin"))
			r.Post("/api/v1/users", a.createUser)
			r.Patch("/api/v1/users/{username}", a.updateUser)
			r.Post("/api/v1/users/{username}/password", a.resetUserPassword)
			r.Delete("/api/v1/users/{username}", a.deleteUser)
		})
	})
	return r
}

func (a *API) listUsers(w http.ResponseWriter, r *http.Request) {
	search := r.URL.Query().Get("search")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	users, err := db.ListFTPUsers(r.Context(), a.Pool, search, limit, offset)
	if err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(users)
}
