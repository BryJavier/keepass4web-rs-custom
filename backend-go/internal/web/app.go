// Package web owns the server-rendered public boundary. Browser cookies carry
// only an opaque session id; access tokens, CSRF tokens and Rust handles stay
// in process memory.
package web

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/lixmal/keepass4web-rs/backend-go/internal/privateclient"
	"github.com/lixmal/keepass4web-rs/backend-go/internal/supabase"
)

const sessionCookieName = "keepass4web_session"
const maxVaultBytes = 50 << 20
const maxKeyFileBytes = 10 << 20

type Vault struct {
	ID         string
	Name       string
	ObjectPath string
	TrashedAt  *time.Time
	PurgeAfter *time.Time
}

// VaultRepository remains the small metadata seam used by existing fakes.
type VaultRepository interface {
	List(ownerID string) ([]Vault, error)
	Owns(ownerID, vaultID string) (bool, error)
}
type SessionVaultRepository interface {
	ListForSession(context.Context, string, string) ([]Vault, error)
	VaultForSession(context.Context, string, string, string) (Vault, bool, error)
	UploadForSession(context.Context, string, string, string, string, []byte) (Vault, error)
	DownloadForSession(context.Context, string, string, Vault) ([]byte, error)
	DownloadActiveForSession(context.Context, string, string, string) ([]byte, error)
	ReplaceForSession(context.Context, string, string, Vault, []byte) error
	ReplaceDecodedEntries(context.Context, string, string, string, []DecodedEntry) error
	DecodedEntries(context.Context, string, string, string) ([]DecodedEntry, error)
	UpdateDecodedEntry(context.Context, string, string, string, string, DecodedEntry) error
	RenameForSession(context.Context, string, string, string, string) error
	TrashVaultForSession(context.Context, string, string, string) error
	RestoreVaultForSession(context.Context, string, string, string) error
	TrashedVaultsForSession(context.Context, string, string) ([]Vault, error)
	TrashEntryForSession(context.Context, string, string, string, string) error
	RestoreEntryForSession(context.Context, string, string, string, string) error
	BeginTrashEntryForSession(context.Context, string, string, string, string) (DecodedEntry, bool, error)
	BeginRestoreEntryForSession(context.Context, string, string, string, string) (DecodedEntry, bool, error)
	AbortEntryTransitionForSession(context.Context, string, string, string, string, string) error
	TrashedEntriesForSession(context.Context, string, string, string) ([]DecodedEntry, error)
}
type RustVaultService interface {
	Unlock(context.Context, privateclient.UnlockRequest) (privateclient.UnlockResponse, error)
	Groups(context.Context, privateclient.HandleRequest) (privateclient.GroupsResponse, error)
	GroupEntries(context.Context, privateclient.HandleRequest, string) (privateclient.EntryGroupResponse, error)
	Entry(context.Context, privateclient.HandleRequest, string) (privateclient.EntryResponse, error)
	Reveal(context.Context, privateclient.HandleRequest, string, string) (privateclient.RevealResponse, error)
	Update(context.Context, privateclient.UpdateRequest) (privateclient.UpdateResponse, error)
	Close(context.Context, privateclient.HandleRequest) error
	CreateVault(context.Context, privateclient.CreateVaultRequest) (privateclient.CreateVaultResponse, error)
	CreateEntry(context.Context, privateclient.CreateEntryRequest) (privateclient.CreateEntryResponse, error)
	DeleteEntry(context.Context, privateclient.DeleteEntryRequest) (privateclient.DeleteEntryResponse, error)
	RestoreEntry(context.Context, privateclient.RestoreEntryRequest) (privateclient.RestoreEntryResponse, error)
}
type DecodedEntry struct {
	EntryID    string
	GroupID    string
	Title      string
	Username   string
	Password   string
	URL        string
	Notes      string
	Fields     map[string]string
	TrashedAt  *time.Time
	PurgeAfter *time.Time
}
type TokenValidator interface {
	Validate(context.Context, string) (supabase.Identity, error)
}

// TrashPurger is the only server-only capability the HTTP app receives. Its
// implementation retains any service-role credential outside this package.
type TrashPurger interface {
	PurgeExpiredTrash(context.Context) error
}
type Dependencies struct {
	Vaults          VaultRepository
	SessionVaults   SessionVaultRepository
	Rust            RustVaultService
	Auth            TokenValidator
	SecureCookies   bool
	SupabaseURL     string
	SupabaseAnonKey string
	// IdleTimeout is how long a session may go without an authenticated
	// request before its active vault handle is auto-closed. Defaults to 60s.
	IdleTimeout time.Duration
	// SweepInterval is how often the idle sweep checks sessions. Defaults to 5s.
	SweepInterval time.Duration
	// TrashPurger is optional outside production, where a service-role key is
	// required by configuration. It is never exposed to browser handlers.
	TrashPurger TrashPurger
	// TrashPurgeInterval is how often expired trash is removed. Defaults to 1h.
	TrashPurgeInterval time.Duration
	// PurgeError receives no error detail so background failures cannot leak
	// credentials, object paths, or user identifiers through logs.
	PurgeError func()
	// PurgeContext is normally context.Background. Tests may cancel it to stop
	// the lifecycle worker deterministically.
	PurgeContext context.Context
}
type session struct {
	userID, accessToken, csrfToken, activeHandle, activeVaultID, email string
	lastActivity                                                       time.Time
}
type App struct {
	vaults          VaultRepository
	sessionVaults   SessionVaultRepository
	rust            RustVaultService
	auth            TokenValidator
	secureCookies   bool
	supabaseURL     string
	supabaseAnonKey string
	idleTimeout     time.Duration
	trashPurger     TrashPurger
	purgeError      func()
	tmpl            *template.Template
	mu              sync.RWMutex
	sessions        map[string]session
}

func NewApp(d Dependencies) *App {
	idleTimeout := d.IdleTimeout
	if idleTimeout <= 0 {
		idleTimeout = 60 * time.Second
	}
	sweepInterval := d.SweepInterval
	if sweepInterval <= 0 {
		sweepInterval = 5 * time.Second
	}
	a := &App{
		vaults:          d.Vaults,
		sessionVaults:   d.SessionVaults,
		rust:            d.Rust,
		auth:            d.Auth,
		secureCookies:   d.SecureCookies,
		supabaseURL:     d.SupabaseURL,
		supabaseAnonKey: d.SupabaseAnonKey,
		idleTimeout:     idleTimeout,
		trashPurger:     d.TrashPurger,
		purgeError:      d.PurgeError,
		tmpl: template.Must(template.New("pages").Funcs(template.FuncMap{
			"lower":      strings.ToLower,
			"formatDate": formatDate,
			"dict":       templateDict,
		}).ParseGlob(templatesGlob())),
		sessions: map[string]session{},
	}
	go a.sweepIdleVaults(sweepInterval)
	if a.trashPurger != nil {
		purgeInterval := d.TrashPurgeInterval
		if purgeInterval <= 0 {
			purgeInterval = time.Hour
		}
		purgeContext := d.PurgeContext
		if purgeContext == nil {
			purgeContext = context.Background()
		}
		go a.purgeExpiredTrashLoop(purgeContext, purgeInterval)
	}
	return a
}

// sweepIdleVaults runs for the lifetime of the process. It never needs an
// explicit stop signal: the app has exactly one long-running instance in
// production, and leaked per-test goroutines exit naturally when the test
// binary does.
func (a *App) sweepIdleVaults(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		a.closeIdleVaults()
	}
}

func (a *App) closeIdleVaults() {
	a.mu.Lock()
	type idleSession struct {
		id string
		s  session
	}
	var toClose []idleSession
	for id, s := range a.sessions {
		if s.activeHandle != "" && time.Since(s.lastActivity) >= a.idleTimeout {
			toClose = append(toClose, idleSession{id, s})
		}
	}
	a.mu.Unlock()

	for _, entry := range toClose {
		if a.rust == nil {
			continue
		}
		if err := a.rust.Close(context.Background(), privateclient.HandleRequest{Handle: entry.s.activeHandle, UserID: entry.s.userID, VaultID: entry.s.activeVaultID}); err != nil {
			continue
		}
		a.mu.Lock()
		if current, ok := a.sessions[entry.id]; ok && current.activeHandle == entry.s.activeHandle {
			delete(a.sessions, entry.id)
		}
		a.mu.Unlock()
	}
}

func (a *App) purgeExpiredTrashLoop(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.purgeExpiredTrash(ctx)
		}
	}
}

// purgeExpiredTrash runs with a per-invocation bound so a slow Supabase call
// cannot stall future ticks or any browser request handler.
func (a *App) purgeExpiredTrash(parent context.Context) {
	if a.trashPurger == nil {
		return
	}
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	if err := a.trashPurger.PurgeExpiredTrash(ctx); err != nil && a.purgeError != nil {
		a.purgeError()
	}
}
func (a *App) CreateSession(userID string) *http.Cookie { return a.createSession(userID, "") }
func (a *App) createSession(userID, accessToken string) *http.Cookie {
	id := randomToken()
	a.mu.Lock()
	a.sessions[id] = session{userID: userID, accessToken: accessToken, csrfToken: randomToken(), lastActivity: time.Now()}
	a.mu.Unlock()
	return &http.Cookie{Name: sessionCookieName, Value: id, Path: "/", HttpOnly: true, Secure: a.secureCookies, SameSite: http.SameSiteLaxMode, MaxAge: 8 * 60 * 60}
}
func (a *App) CSRFToken(id string) string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.sessions[id].csrfToken
}
func (a *App) ActiveHandle(id string) string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.sessions[id].activeHandle
}
func (a *App) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == "GET" && r.URL.Path == "/":
		http.Redirect(w, r, "/vaults", 303)
	case r.Method == "GET" && r.URL.Path == "/sign-in":
		a.signInPage(w, r)
	case r.Method == "POST" && r.URL.Path == "/session":
		a.createBrowserSession(w, r)
	case r.Method == "POST" && r.URL.Path == "/sign-out":
		a.signOut(w, r)
	case r.Method == "GET" && r.URL.Path == "/vaults":
		a.vaultsPage(w, r)
	case r.Method == "POST" && r.URL.Path == "/vaults/upload":
		a.uploadVault(w, r)
	case r.Method == "POST" && r.URL.Path == "/vaults/create":
		a.createVault(w, r)
	case r.Method == "POST" && r.URL.Path == "/vaults/rename":
		a.renameVault(w, r)
	case r.Method == "GET" && r.URL.Path == "/vaults/download":
		a.downloadVault(w, r)
	case r.Method == "POST" && r.URL.Path == "/vaults/delete":
		a.trashVault(w, r)
	case r.Method == "GET" && r.URL.Path == "/vaults/trash":
		a.trashedVaults(w, r)
	case r.Method == "POST" && r.URL.Path == "/vaults/restore":
		a.restoreVault(w, r)
	case r.Method == "POST" && r.URL.Path == "/vaults/select":
		a.selectVault(w, r)
	case r.Method == "GET" && r.URL.Path == "/vaults/unlock":
		a.unlockPage(w, r)
	case r.Method == "POST" && r.URL.Path == "/vaults/unlock":
		a.unlock(w, r)
	case r.Method == "GET" && r.URL.Path == "/vaults/browse":
		a.browse(w, r)
	case r.Method == "POST" && r.URL.Path == "/vaults/entries/update":
		a.updateEntry(w, r)
	case r.Method == "POST" && r.URL.Path == "/vaults/entries/create":
		a.createEntry(w, r)
	case r.Method == "POST" && r.URL.Path == "/vaults/entries/delete":
		a.trashEntry(w, r)
	case r.Method == "GET" && r.URL.Path == "/vaults/entries/trash":
		a.trashedEntries(w, r)
	case r.Method == "POST" && r.URL.Path == "/vaults/entries/restore":
		a.restoreEntry(w, r)
	case r.Method == "POST" && r.URL.Path == "/vaults/close":
		a.closeVault(w, r)
	case r.Method == "GET" && r.URL.Path == "/session/status":
		a.sessionStatus(w, r)
	case r.Method == "POST" && r.URL.Path == "/session/heartbeat":
		a.heartbeat(w, r)
	default:
		http.NotFound(w, r)
	}
}
func (a *App) signInPage(w http.ResponseWriter, r *http.Request) {
	c := a.createSession("", "")
	http.SetCookie(w, c)
	a.render(w, "sign-in", vaultPage{CSRFToken: a.CSRFToken(c.Value), SupabaseURL: a.supabaseURL, SupabaseAnonKey: a.supabaseAnonKey})
}
func (a *App) createBrowserSession(w http.ResponseWriter, r *http.Request) {
	if a.auth == nil {
		http.Error(w, "Sign-in is unavailable.", 503)
		return
	}
	_, preauth, ok := a.sessionFromCookie(r)
	if !ok || preauth.userID != "" || !a.validCSRF(r, preauth) {
		http.Error(w, "Your form has expired. Please reload and try again.", 403)
		return
	}
	token := bearer(r)
	if token == "" {
		token = r.Form.Get("access_token")
	}
	identity, err := a.auth.Validate(r.Context(), token)
	if err != nil {
		a.renderStatus(w, http.StatusUnauthorized, "sign-in", vaultPage{CSRFToken: preauth.csrfToken, SupabaseURL: a.supabaseURL, SupabaseAnonKey: a.supabaseAnonKey, Error: "Sign-in failed."})
		return
	}
	cookie := a.createSession(identity.ID, token)
	a.mu.Lock()
	s := a.sessions[cookie.Value]
	s.email = identity.Email
	a.sessions[cookie.Value] = s
	a.mu.Unlock()
	http.SetCookie(w, cookie)
	http.Redirect(w, r, "/vaults", 303)
}
func (a *App) signOut(w http.ResponseWriter, r *http.Request) {
	id, current, ok := a.authenticated(r)
	if !ok {
		http.Redirect(w, r, "/sign-in", 303)
		return
	}
	if !a.validCSRF(r, current) {
		http.Error(w, "Your form has expired. Please reload and try again.", 403)
		return
	}
	if err := a.closeActive(r.Context(), current); err != nil {
		http.Error(w, "Unable to close vault safely. Please try again.", 503)
		return
	}
	a.mu.Lock()
	delete(a.sessions, id)
	a.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: "", Path: "/", HttpOnly: true, Secure: a.secureCookies, MaxAge: -1, SameSite: http.SameSiteLaxMode})
	http.Redirect(w, r, "/sign-in", 303)
}
func (a *App) sessionStatus(w http.ResponseWriter, r *http.Request) {
	_, s, ok := a.sessionFromCookie(r)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if !ok || s.userID == "" {
		json.NewEncoder(w).Encode(map[string]any{"active": false, "vault_active": false, "idle_seconds_remaining": 0})
		return
	}
	remaining := int(a.idleTimeout.Seconds()) - int(time.Since(s.lastActivity).Seconds())
	if remaining < 0 {
		remaining = 0
	}
	json.NewEncoder(w).Encode(map[string]any{
		"active":                 true,
		"vault_active":           s.activeHandle != "",
		"idle_seconds_remaining": remaining,
	})
}

func (a *App) heartbeat(w http.ResponseWriter, r *http.Request) {
	_, s, ok := a.authenticated(r)
	if !ok {
		http.Redirect(w, r, "/sign-in", 303)
		return
	}
	if !a.validCSRF(r, s) {
		http.Error(w, "Your form has expired. Please reload and try again.", 403)
		return
	}
	// a.authenticated already refreshed lastActivity above.
	w.WriteHeader(http.StatusNoContent)
}
func (a *App) vaultsPage(w http.ResponseWriter, r *http.Request) {
	_, s, ok := a.authenticated(r)
	if !ok {
		http.Redirect(w, r, "/sign-in", 303)
		return
	}
	vaults, err := a.list(r.Context(), s)
	if err != nil {
		http.Error(w, "Unable to load vaults. Please try again.", 500)
		return
	}
	a.render(w, "vaults", vaultPage{Vaults: vaults, CSRFToken: s.csrfToken, Fragment: isHTMX(r), Email: s.email})
}
func (a *App) uploadVault(w http.ResponseWriter, r *http.Request) {
	_, s, ok := a.authenticated(r)
	if !ok {
		http.Redirect(w, r, "/sign-in", 303)
		return
	}
	// ParseMultipartForm populates r.Form; doing this before CSRF validation is
	// required because ParseForm alone does not read multipart fields.
	r.Body = http.MaxBytesReader(w, r.Body, maxVaultBytes+1<<20)
	if err := r.ParseMultipartForm(maxVaultBytes + 1<<20); err != nil {
		http.Error(w, "Vault upload is too large or invalid.", 400)
		return
	}
	if !a.validCSRF(r, s) {
		http.Error(w, "Your form has expired. Please reload and try again.", 403)
		return
	}
	file, header, err := r.FormFile("vault")
	if err != nil {
		http.Error(w, "Select a .kdbx vault file.", 400)
		return
	}
	defer file.Close()
	if !strings.HasSuffix(strings.ToLower(header.Filename), ".kdbx") {
		http.Error(w, "Only .kdbx vault files are accepted.", 400)
		return
	}
	data, err := io.ReadAll(io.LimitReader(file, maxVaultBytes+1))
	if err != nil || len(data) > maxVaultBytes {
		http.Error(w, "Vault upload is too large.", 400)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		name = strings.TrimSuffix(header.Filename, ".kdbx")
	}
	if a.sessionVaults == nil {
		http.Error(w, "Vault upload is unavailable.", 503)
		return
	}
	if _, err = a.sessionVaults.UploadForSession(r.Context(), s.userID, s.accessToken, name, newUUID(), data); err != nil {
		http.Error(w, "Unable to upload vault. Please try again.", 500)
		return
	}
	if isHTMX(r) {
		a.vaultsPage(w, r)
		return
	}
	http.Redirect(w, r, "/vaults", 303)
}
func (a *App) createVault(w http.ResponseWriter, r *http.Request) {
	_, s, ok := a.authenticated(r)
	if !ok {
		http.Redirect(w, r, "/sign-in", 303)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form.", http.StatusBadRequest)
		return
	}
	if !a.validCSRF(r, s) {
		http.Error(w, "Your form has expired. Please reload and try again.", 403)
		return
	}
	name := strings.TrimSpace(r.Form.Get("name"))
	if name == "" {
		http.Error(w, "Name the new vault.", 400)
		return
	}
	password := strings.TrimSpace(r.Form.Get("password"))
	if password == "" {
		http.Error(w, "Set a master password for the new vault.", 400)
		return
	}
	if a.sessionVaults == nil || a.rust == nil {
		http.Error(w, "Vault creation is unavailable.", 503)
		return
	}
	created, err := a.rust.CreateVault(r.Context(), privateclient.CreateVaultRequest{Password: &password})
	if err != nil {
		http.Error(w, "Unable to create a new vault.", 500)
		return
	}
	data, err := base64.StdEncoding.DecodeString(created.DatabaseB64)
	if err != nil || len(data) == 0 || len(data) > maxVaultBytes {
		http.Error(w, "Unable to create a new vault.", 500)
		return
	}
	if _, err = a.sessionVaults.UploadForSession(r.Context(), s.userID, s.accessToken, name, newUUID(), data); err != nil {
		http.Error(w, "Unable to save the new vault. Please try again.", 500)
		return
	}
	if isHTMX(r) {
		a.vaultsPage(w, r)
		return
	}
	http.Redirect(w, r, "/vaults", 303)
}
func (a *App) renameVault(w http.ResponseWriter, r *http.Request) {
	_, s, ok := a.authenticated(r)
	if !ok {
		http.Redirect(w, r, "/sign-in", http.StatusSeeOther)
		return
	}
	if !a.validCSRF(r, s) {
		http.Error(w, "Your form has expired. Please reload and try again.", http.StatusForbidden)
		return
	}
	vaultID, name := strings.TrimSpace(r.Form.Get("vault_id")), strings.TrimSpace(r.Form.Get("name"))
	if vaultID == "" || name == "" || len(name) > 255 {
		http.Error(w, "Enter a vault name.", http.StatusBadRequest)
		return
	}
	if _, owned, err := a.vault(r.Context(), s, vaultID); err != nil {
		http.Error(w, "Unable to rename that vault. Please try again.", http.StatusInternalServerError)
		return
	} else if !owned {
		http.Error(w, "Vault not found.", http.StatusNotFound)
		return
	}
	if a.sessionVaults == nil {
		http.Error(w, "Vault rename is unavailable.", http.StatusServiceUnavailable)
		return
	}
	if err := a.sessionVaults.RenameForSession(r.Context(), s.userID, s.accessToken, vaultID, name); err != nil {
		http.Error(w, "Unable to rename that vault. Please try again.", http.StatusInternalServerError)
		return
	}
	if isHTMX(r) {
		a.vaultsPage(w, r)
		return
	}
	http.Redirect(w, r, "/vaults", http.StatusSeeOther)
}
func (a *App) downloadVault(w http.ResponseWriter, r *http.Request) {
	_, s, ok := a.authenticated(r)
	if !ok {
		http.Redirect(w, r, "/sign-in", http.StatusSeeOther)
		return
	}
	vaultID := strings.TrimSpace(r.URL.Query().Get("vault_id"))
	if vaultID == "" {
		http.Error(w, "Vault not found.", http.StatusNotFound)
		return
	}
	v, owned, err := a.vault(r.Context(), s, vaultID)
	if err != nil {
		http.Error(w, "Unable to download that vault. Please try again.", http.StatusInternalServerError)
		return
	}
	if !owned {
		http.Error(w, "Vault not found.", http.StatusNotFound)
		return
	}
	if a.sessionVaults == nil {
		http.Error(w, "Vault download is unavailable.", http.StatusServiceUnavailable)
		return
	}
	data, err := a.sessionVaults.DownloadActiveForSession(r.Context(), s.userID, s.accessToken, vaultID)
	if err != nil {
		http.Error(w, "Unable to download that vault. Please try again.", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+downloadFilename(v.Name)+`"`)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}
func (a *App) trashVault(w http.ResponseWriter, r *http.Request) {
	id, s, ok := a.authenticated(r)
	if !ok {
		http.Redirect(w, r, "/sign-in", http.StatusSeeOther)
		return
	}
	if !a.validCSRF(r, s) {
		http.Error(w, "Your form has expired. Please reload and try again.", http.StatusForbidden)
		return
	}
	vaultID := strings.TrimSpace(r.Form.Get("vault_id"))
	if vaultID == "" {
		http.Error(w, "Vault not found.", http.StatusNotFound)
		return
	}
	if a.sessionVaults == nil {
		http.Error(w, "Vault deletion is unavailable.", http.StatusServiceUnavailable)
		return
	}
	if _, owned, err := a.vault(r.Context(), s, vaultID); err != nil {
		http.Error(w, "Unable to delete that vault. Please try again.", http.StatusInternalServerError)
		return
	} else if !owned {
		trashed, listErr := a.sessionVaults.TrashedVaultsForSession(r.Context(), s.userID, s.accessToken)
		if listErr != nil {
			http.Error(w, "Unable to delete that vault. Please try again.", http.StatusInternalServerError)
			return
		}
		if containsVault(trashed, vaultID) {
			http.Redirect(w, r, "/vaults", http.StatusSeeOther)
			return
		}
		http.Error(w, "Vault not found.", http.StatusNotFound)
		return
	}
	if err := a.sessionVaults.TrashVaultForSession(r.Context(), s.userID, s.accessToken, vaultID); err != nil {
		http.Error(w, "Unable to delete that vault. Please try again.", http.StatusInternalServerError)
		return
	}
	if s.activeVaultID == vaultID {
		if err := a.closeActive(r.Context(), s); err != nil {
			_ = a.sessionVaults.RestoreVaultForSession(r.Context(), s.userID, s.accessToken, vaultID)
			http.Error(w, "Unable to close vault safely. Please try again.", http.StatusServiceUnavailable)
			return
		}
		s.activeVaultID, s.activeHandle = "", ""
		a.mu.Lock()
		a.sessions[id] = s
		a.mu.Unlock()
	}
	if isHTMX(r) {
		a.vaultsPage(w, r)
		return
	}
	http.Redirect(w, r, "/vaults", http.StatusSeeOther)
}
func (a *App) trashedVaults(w http.ResponseWriter, r *http.Request) {
	_, s, ok := a.authenticated(r)
	if !ok {
		http.Redirect(w, r, "/sign-in", http.StatusSeeOther)
		return
	}
	if a.sessionVaults == nil {
		http.Error(w, "Vault trash is unavailable.", http.StatusServiceUnavailable)
		return
	}
	vaults, err := a.sessionVaults.TrashedVaultsForSession(r.Context(), s.userID, s.accessToken)
	if err != nil {
		http.Error(w, "Unable to load vault trash. Please try again.", http.StatusInternalServerError)
		return
	}
	if wantsHTML(r) {
		a.render(w, "vault-trash", vaultTrashPage{
			vaultPage:     vaultPage{CSRFToken: s.csrfToken, Email: s.email},
			TrashedVaults: vaults,
		})
		return
	}
	type vaultTombstone struct {
		ID         string     `json:"id"`
		Name       string     `json:"name"`
		TrashedAt  *time.Time `json:"trashed_at"`
		PurgeAfter *time.Time `json:"purge_after"`
	}
	out := make([]vaultTombstone, len(vaults))
	for i, v := range vaults {
		out[i] = vaultTombstone{ID: v.ID, Name: v.Name, TrashedAt: v.TrashedAt, PurgeAfter: v.PurgeAfter}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(out)
}
func (a *App) restoreVault(w http.ResponseWriter, r *http.Request) {
	_, s, ok := a.authenticated(r)
	if !ok {
		http.Redirect(w, r, "/sign-in", http.StatusSeeOther)
		return
	}
	if !a.validCSRF(r, s) {
		http.Error(w, "Your form has expired. Please reload and try again.", http.StatusForbidden)
		return
	}
	vaultID := strings.TrimSpace(r.Form.Get("vault_id"))
	if vaultID == "" {
		http.Error(w, "Vault not found.", http.StatusNotFound)
		return
	}
	if a.sessionVaults == nil {
		http.Error(w, "Vault restore is unavailable.", http.StatusServiceUnavailable)
		return
	}
	trashed, err := a.sessionVaults.TrashedVaultsForSession(r.Context(), s.userID, s.accessToken)
	if err != nil {
		http.Error(w, "Unable to restore that vault. Please try again.", http.StatusInternalServerError)
		return
	}
	if !containsVault(trashed, vaultID) {
		if _, owned, activeErr := a.vault(r.Context(), s, vaultID); activeErr != nil {
			http.Error(w, "Unable to restore that vault. Please try again.", http.StatusInternalServerError)
			return
		} else if owned {
			http.Redirect(w, r, "/vaults", http.StatusSeeOther)
			return
		}
		http.Error(w, "Vault not found.", http.StatusNotFound)
		return
	}
	if err := a.sessionVaults.RestoreVaultForSession(r.Context(), s.userID, s.accessToken, vaultID); err != nil {
		http.Error(w, "Unable to restore that vault. Please try again.", http.StatusInternalServerError)
		return
	}
	if isHTMX(r) {
		a.vaultsPage(w, r)
		return
	}
	http.Redirect(w, r, "/vaults", http.StatusSeeOther)
}
func (a *App) selectVault(w http.ResponseWriter, r *http.Request) {
	id, s, ok := a.authenticated(r)
	if !ok {
		http.Redirect(w, r, "/sign-in", 303)
		return
	}
	if !a.validCSRF(r, s) {
		http.Error(w, "Your form has expired. Please reload and try again.", 403)
		return
	}
	vaultID := strings.TrimSpace(r.Form.Get("vault_id"))
	v, owned, err := a.vault(r.Context(), s, vaultID)
	if err != nil {
		http.Error(w, "Unable to select that vault. Please try again.", 500)
		return
	}
	if !owned {
		http.Error(w, "Vault not found.", 404)
		return
	}
	if err := a.closeActive(r.Context(), s); err != nil {
		http.Error(w, "Unable to close vault safely. Please try again.", 503)
		return
	}
	s.activeVaultID = v.ID
	s.activeHandle = ""
	a.mu.Lock()
	a.sessions[id] = s
	a.mu.Unlock()
	if isHTMX(r) {
		w.Header().Set("HX-Redirect", "/vaults/unlock")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, "/vaults/unlock", 303)
}
func (a *App) unlockPage(w http.ResponseWriter, r *http.Request) {
	id, s, ok := a.authenticated(r)
	if !ok {
		http.Redirect(w, r, "/sign-in", 303)
		return
	}
	if s.activeVaultID == "" {
		http.Redirect(w, r, "/vaults", 303)
		return
	}
	v, owned, err := a.vault(r.Context(), s, s.activeVaultID)
	if err != nil {
		http.Error(w, "Unable to load that vault. Please try again.", 500)
		return
	}
	if !owned {
		a.invalidateActiveHandle(r.Context(), id, s)
		http.Redirect(w, r, "/vaults", http.StatusSeeOther)
		return
	}
	a.render(w, "unlock", vaultPage{CSRFToken: s.csrfToken, Selected: v})
}
func (a *App) unlock(w http.ResponseWriter, r *http.Request) {
	id, s, ok := a.authenticated(r)
	if !ok {
		http.Redirect(w, r, "/sign-in", 303)
		return
	}
	// The unlock form is multipart because it may include a key file. Parse it
	// before looking up csrf_token; otherwise Form is still empty and every
	// valid submission appears to have expired.
	r.Body = http.MaxBytesReader(w, r.Body, maxKeyFileBytes+(1<<20))
	if err := r.ParseMultipartForm(maxKeyFileBytes + 1); err != nil {
		http.Error(w, "Invalid unlock form.", http.StatusBadRequest)
		return
	}
	if !a.validCSRF(r, s) || s.activeVaultID == "" {
		http.Error(w, "Your form has expired. Please reload and try again.", 403)
		return
	}
	v, owned, err := a.vault(r.Context(), s, s.activeVaultID)
	if err != nil || !owned || a.sessionVaults == nil || a.rust == nil {
		http.Error(w, "Unable to unlock that vault.", 503)
		return
	}
	data, err := a.sessionVaults.DownloadForSession(r.Context(), s.userID, s.accessToken, v)
	if err != nil {
		http.Error(w, "Unable to unlock that vault.", 500)
		return
	}
	var keyfile *string
	if file, _, e := r.FormFile("key_file"); e == nil {
		defer file.Close()
		raw, readErr := io.ReadAll(io.LimitReader(file, maxKeyFileBytes+1))
		if readErr != nil || len(raw) > maxKeyFileBytes {
			http.Error(w, "Key file is too large.", 400)
			return
		}
		encoded := base64.StdEncoding.EncodeToString(raw)
		keyfile = &encoded
	}
	password := r.Form.Get("password")
	res, err := a.rust.Unlock(r.Context(), privateclient.UnlockRequest{UserID: s.userID, VaultID: v.ID, DatabaseB64: base64.StdEncoding.EncodeToString(data), Password: &password, KeyfileB64: keyfile})
	if err != nil {
		// This is a decoder failure, not a Supabase authentication failure.
		a.renderStatus(w, http.StatusUnprocessableEntity, "unlock", vaultPage{CSRFToken: s.csrfToken, Selected: v, Error: "The vault password or key file is incorrect, or the vault could not be decoded."})
		return
	}
	if err := a.syncDecodedEntries(r.Context(), s, res.Handle); err != nil {
		http.Error(w, "Vault decoded but could not be saved.", 500)
		return
	}
	s.activeHandle = res.Handle
	a.mu.Lock()
	a.sessions[id] = s
	a.mu.Unlock()
	http.Redirect(w, r, "/vaults/browse", 303)
}
func (a *App) browse(w http.ResponseWriter, r *http.Request) {
	a.browseWithErrorStatus(w, r, http.StatusOK, "")
}

// browseWithError renders the active vault while preserving an expected write
// error for the user. The template owns the error presentation so failed entry
// creation returns the user to the same usable browse screen.
func (a *App) browseWithErrorStatus(w http.ResponseWriter, r *http.Request, status int, pageError string) {
	id, s, ok := a.authenticated(r)
	if !ok {
		http.Redirect(w, r, "/sign-in", 303)
		return
	}
	if s.activeHandle == "" || a.rust == nil {
		http.Redirect(w, r, "/vaults/unlock", 303)
		return
	}
	v, owned, err := a.vault(r.Context(), s, s.activeVaultID)
	if err != nil {
		http.Error(w, "Unable to load that vault. Please try again.", 500)
		return
	}
	if !owned {
		a.invalidateActiveHandle(r.Context(), id, s)
		http.Redirect(w, r, "/vaults", http.StatusSeeOther)
		return
	}
	entries, err := a.sessionVaults.DecodedEntries(r.Context(), s.userID, s.accessToken, s.activeVaultID)
	if err != nil {
		http.Error(w, "Unable to load decoded vault entries.", 500)
		return
	}
	a.renderStatus(w, status, "browse", struct {
		vaultPage
		Entries []DecodedEntry
	}{vaultPage: vaultPage{CSRFToken: s.csrfToken, Selected: v, Email: s.email, Error: pageError}, Entries: entries})
}
func (a *App) syncDecodedEntries(ctx context.Context, s session, handle string) error {
	request := privateclient.HandleRequest{Handle: handle, UserID: s.userID, VaultID: s.activeVaultID}
	groups, err := a.rust.Groups(ctx, request)
	if err != nil {
		return err
	}
	var root privateclient.Group
	if err := json.Unmarshal(groups.Groups, &root); err != nil {
		return err
	}
	var out []DecodedEntry
	var walk func(privateclient.Group) error
	walk = func(g privateclient.Group) error {
		batch, err := a.rust.GroupEntries(ctx, request, g.ID)
		if err != nil {
			return err
		}
		for _, summary := range batch.Entries {
			detail, err := a.rust.Entry(ctx, request, summary.ID)
			if err != nil {
				return err
			}
			e := DecodedEntry{EntryID: detail.ID, GroupID: g.ID, Fields: map[string]string{}}
			if detail.Title != nil {
				e.Title = *detail.Title
			}
			if detail.Username != nil {
				e.Username = *detail.Username
			}
			if detail.URL != nil {
				e.URL = *detail.URL
			}
			if detail.Notes != nil {
				e.Notes = *detail.Notes
			}
			for k, v := range detail.Strings {
				if v != nil {
					e.Fields[k] = *v
				}
			}
			for k := range detail.Protected {
				value, err := a.rust.Reveal(ctx, request, detail.ID, k)
				if err != nil {
					return err
				}
				if k == "Password" {
					e.Password = value.Value
				} else {
					e.Fields[k] = value.Value
				}
			}
			out = append(out, e)
		}
		for _, child := range g.Children {
			if err := walk(child); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(root); err != nil {
		return err
	}
	return a.sessionVaults.ReplaceDecodedEntries(ctx, s.userID, s.accessToken, s.activeVaultID, out)
}
func (a *App) updateEntry(w http.ResponseWriter, r *http.Request) {
	id, s, ok := a.authenticated(r)
	if !ok {
		http.Redirect(w, r, "/sign-in", 303)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxKeyFileBytes+(1<<20))
	if err := r.ParseMultipartForm(maxKeyFileBytes + 1); err != nil {
		http.Error(w, "Invalid save form.", http.StatusBadRequest)
		return
	}
	if !a.validCSRF(r, s) {
		http.Error(w, "Your form has expired. Please reload and try again.", 403)
		return
	}
	if s.activeVaultID == "" || strings.TrimSpace(r.Form.Get("entry_id")) == "" {
		http.Error(w, "Select and unlock a vault before editing an entry.", http.StatusBadRequest)
		return
	}
	e := DecodedEntry{EntryID: r.Form.Get("entry_id"), Title: r.Form.Get("title"), Username: r.Form.Get("username"), Password: r.Form.Get("password"), URL: r.Form.Get("url"), Notes: r.Form.Get("notes"), Fields: map[string]string{}}
	v, owned, err := a.vault(r.Context(), s, s.activeVaultID)
	if err != nil || !owned || a.sessionVaults == nil || a.rust == nil {
		http.Error(w, "Unable to save entry.", 503)
		return
	}
	var masterPassword *string
	if value := r.Form.Get("master_password"); value != "" {
		masterPassword = &value
	}
	var keyfileB64 *string
	if file, _, err := r.FormFile("key_file"); err == nil {
		defer file.Close()
		raw, readErr := io.ReadAll(io.LimitReader(file, maxKeyFileBytes+1))
		if readErr != nil || len(raw) > maxKeyFileBytes {
			http.Error(w, "Key file is too large.", http.StatusBadRequest)
			return
		}
		encoded := base64.StdEncoding.EncodeToString(raw)
		keyfileB64 = &encoded
	}
	updated, err := a.rust.Update(r.Context(), privateclient.UpdateRequest{Handle: s.activeHandle, UserID: s.userID, VaultID: s.activeVaultID, EntryID: e.EntryID, Title: e.Title, Username: e.Username, Password: e.Password, URL: e.URL, Notes: e.Notes, MasterPassword: masterPassword, KeyfileB64: keyfileB64})
	if err != nil {
		a.recoverMutationHandle(r.Context(), id, s, v, masterPassword, keyfileB64)
		http.Error(w, "Unable to save the vault. Check the master password or key file.", http.StatusUnprocessableEntity)
		return
	}
	vaultBytes, err := base64.StdEncoding.DecodeString(updated.DatabaseB64)
	if err != nil || len(vaultBytes) == 0 || len(vaultBytes) > maxVaultBytes {
		a.recoverMutationHandle(r.Context(), id, s, v, masterPassword, keyfileB64)
		http.Error(w, "Unable to save the vault.", 500)
		return
	}
	if err := a.sessionVaults.ReplaceForSession(r.Context(), s.userID, s.accessToken, v, vaultBytes); err != nil {
		a.recoverMutationHandle(r.Context(), id, s, v, masterPassword, keyfileB64)
		http.Error(w, "Vault changed but could not be stored. Please try saving again.", 502)
		return
	}
	if err := a.syncDecodedEntries(r.Context(), s, s.activeHandle); err != nil {
		a.recoverMutationHandle(r.Context(), id, s, v, masterPassword, keyfileB64)
		http.Error(w, "Vault saved but the decoded mirror could not be refreshed.", 502)
		return
	}
	http.Redirect(w, r, "/vaults/browse", 303)
}
func (a *App) createEntry(w http.ResponseWriter, r *http.Request) {
	id, s, ok := a.authenticated(r)
	if !ok {
		http.Redirect(w, r, "/sign-in", 303)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxKeyFileBytes+(1<<20))
	if err := r.ParseMultipartForm(maxKeyFileBytes + 1); err != nil {
		http.Error(w, "Invalid save form.", http.StatusBadRequest)
		return
	}
	if !a.validCSRF(r, s) {
		http.Error(w, "Your form has expired. Please reload and try again.", 403)
		return
	}
	if s.activeVaultID == "" || s.activeHandle == "" {
		http.Error(w, "Unlock a vault before adding an entry.", http.StatusBadRequest)
		return
	}
	title := r.Form.Get("title")
	if strings.TrimSpace(title) == "" {
		http.Error(w, "Give the new entry a title.", http.StatusBadRequest)
		return
	}
	v, owned, err := a.vault(r.Context(), s, s.activeVaultID)
	if err != nil || !owned || a.sessionVaults == nil || a.rust == nil {
		http.Error(w, "Unable to save entry.", 503)
		return
	}
	var groupID *string
	if value := strings.TrimSpace(r.Form.Get("group_id")); value != "" {
		groupID = &value
	}
	var masterPassword *string
	if value := r.Form.Get("master_password"); value != "" {
		masterPassword = &value
	}
	var keyfileB64 *string
	if file, _, err := r.FormFile("key_file"); err == nil {
		defer file.Close()
		raw, readErr := io.ReadAll(io.LimitReader(file, maxKeyFileBytes+1))
		if readErr != nil || len(raw) > maxKeyFileBytes {
			http.Error(w, "Key file is too large.", http.StatusBadRequest)
			return
		}
		encoded := base64.StdEncoding.EncodeToString(raw)
		keyfileB64 = &encoded
	}
	created, err := a.rust.CreateEntry(r.Context(), privateclient.CreateEntryRequest{
		Handle: s.activeHandle, UserID: s.userID, VaultID: s.activeVaultID, GroupID: groupID,
		Title: title, Username: r.Form.Get("username"), Password: r.Form.Get("password"), URL: r.Form.Get("url"), Notes: r.Form.Get("notes"),
		MasterPassword: masterPassword, KeyfileB64: keyfileB64,
	})
	if err != nil {
		a.recoverMutationHandle(r.Context(), id, s, v, masterPassword, keyfileB64)
		a.browseWithErrorStatus(w, r, http.StatusUnprocessableEntity, "Unable to save the vault. Check the master password or key file.")
		return
	}
	vaultBytes, err := base64.StdEncoding.DecodeString(created.DatabaseB64)
	if err != nil || len(vaultBytes) == 0 || len(vaultBytes) > maxVaultBytes {
		a.recoverMutationHandle(r.Context(), id, s, v, masterPassword, keyfileB64)
		http.Error(w, "Unable to save the vault.", 500)
		return
	}
	if err := a.sessionVaults.ReplaceForSession(r.Context(), s.userID, s.accessToken, v, vaultBytes); err != nil {
		a.recoverMutationHandle(r.Context(), id, s, v, masterPassword, keyfileB64)
		http.Error(w, "Vault changed but could not be stored. Please try saving again.", 502)
		return
	}
	if err := a.syncDecodedEntries(r.Context(), s, s.activeHandle); err != nil {
		a.recoverMutationHandle(r.Context(), id, s, v, masterPassword, keyfileB64)
		http.Error(w, "Vault saved but the decoded mirror could not be refreshed.", 502)
		return
	}
	http.Redirect(w, r, "/vaults/browse", 303)
}
func (a *App) trashEntry(w http.ResponseWriter, r *http.Request) {
	id, s, ok := a.authenticated(r)
	if !ok {
		http.Redirect(w, r, "/sign-in", http.StatusSeeOther)
		return
	}
	if !a.parseEntryMultipart(w, r, s) {
		return
	}
	entryID := strings.TrimSpace(r.Form.Get("entry_id"))
	if s.activeVaultID == "" || s.activeHandle == "" || entryID == "" {
		http.Error(w, "Unlock a vault and select an entry before deleting it.", http.StatusBadRequest)
		return
	}
	v, owned, err := a.vault(r.Context(), s, s.activeVaultID)
	if err != nil {
		http.Error(w, "Unable to delete entry.", http.StatusInternalServerError)
		return
	}
	if !owned {
		http.Error(w, "Vault not found.", http.StatusNotFound)
		return
	}
	if a.sessionVaults == nil || a.rust == nil {
		http.Error(w, "Entry deletion is unavailable.", http.StatusServiceUnavailable)
		return
	}
	masterPassword, keyfileB64, ok := entryCredentials(w, r)
	if !ok {
		return
	}
	_, found, err := a.sessionVaults.BeginTrashEntryForSession(r.Context(), s.userID, s.accessToken, s.activeVaultID, entryID)
	if err != nil {
		http.Error(w, "Unable to delete entry.", http.StatusInternalServerError)
		return
	}
	if !found {
		trashedEntries, listErr := a.sessionVaults.TrashedEntriesForSession(r.Context(), s.userID, s.accessToken, s.activeVaultID)
		if listErr != nil {
			http.Error(w, "Unable to delete entry.", http.StatusInternalServerError)
			return
		}
		if _, trashed := findEntry(trashedEntries, entryID); trashed {
			http.Redirect(w, r, "/vaults/browse", http.StatusSeeOther)
			return
		}
		http.Error(w, "Entry not found.", http.StatusNotFound)
		return
	}
	deleted, err := a.rust.DeleteEntry(r.Context(), privateclient.DeleteEntryRequest{HandleRequest: privateclient.HandleRequest{Handle: s.activeHandle, UserID: s.userID, VaultID: s.activeVaultID}, EntryID: entryID, MasterPassword: masterPassword, KeyfileB64: keyfileB64})
	if err != nil {
		a.recoverMutationHandle(r.Context(), id, s, v, masterPassword, keyfileB64)
		_ = a.sessionVaults.AbortEntryTransitionForSession(r.Context(), s.userID, s.accessToken, s.activeVaultID, entryID, "trashing")
		http.Error(w, "Unable to save the vault. Check the master password or key file.", http.StatusUnprocessableEntity)
		return
	}
	vaultBytes, ok := decodedVaultBytes(w, deleted.DatabaseB64)
	if !ok {
		a.recoverMutationHandle(r.Context(), id, s, v, masterPassword, keyfileB64)
		_ = a.sessionVaults.AbortEntryTransitionForSession(r.Context(), s.userID, s.accessToken, s.activeVaultID, entryID, "trashing")
		return
	}
	if err := a.sessionVaults.ReplaceForSession(r.Context(), s.userID, s.accessToken, v, vaultBytes); err != nil {
		a.recoverMutationHandle(r.Context(), id, s, v, masterPassword, keyfileB64)
		_ = a.sessionVaults.AbortEntryTransitionForSession(r.Context(), s.userID, s.accessToken, s.activeVaultID, entryID, "trashing")
		http.Error(w, "Vault changed but could not be stored. Please try saving again.", http.StatusBadGateway)
		return
	}
	if err := a.sessionVaults.TrashEntryForSession(r.Context(), s.userID, s.accessToken, s.activeVaultID, entryID); err != nil {
		a.recoverMutationHandle(r.Context(), id, s, v, masterPassword, keyfileB64)
		http.Error(w, "Vault saved but the entry could not be moved to trash. Please retry the deletion.", http.StatusBadGateway)
		return
	}
	http.Redirect(w, r, "/vaults/browse", http.StatusSeeOther)
}
func (a *App) trashedEntries(w http.ResponseWriter, r *http.Request) {
	id, s, ok := a.authenticated(r)
	if !ok {
		http.Redirect(w, r, "/sign-in", http.StatusSeeOther)
		return
	}
	if s.activeVaultID == "" {
		http.Error(w, "Select a vault before viewing entry trash.", http.StatusBadRequest)
		return
	}
	if a.sessionVaults == nil {
		http.Error(w, "Entry trash is unavailable.", http.StatusServiceUnavailable)
		return
	}
	selected, owned, err := a.vault(r.Context(), s, s.activeVaultID)
	if err != nil {
		http.Error(w, "Unable to load entry trash. Please try again.", http.StatusInternalServerError)
		return
	}
	if !owned {
		a.invalidateActiveHandle(r.Context(), id, s)
		http.Error(w, "Vault not found.", http.StatusNotFound)
		return
	}
	entries, err := a.sessionVaults.TrashedEntriesForSession(r.Context(), s.userID, s.accessToken, s.activeVaultID)
	if err != nil {
		http.Error(w, "Unable to load entry trash. Please try again.", http.StatusInternalServerError)
		return
	}
	if wantsHTML(r) {
		a.render(w, "entry-trash", entryTrashPage{
			vaultPage: vaultPage{CSRFToken: s.csrfToken, Selected: selected, Email: s.email},
			Entries:   entries,
		})
		return
	}
	type entryTombstone struct {
		EntryID    string     `json:"entry_id"`
		Title      string     `json:"title"`
		Username   string     `json:"username"`
		TrashedAt  *time.Time `json:"trashed_at"`
		PurgeAfter *time.Time `json:"purge_after"`
	}
	out := make([]entryTombstone, len(entries))
	for i, e := range entries {
		out[i] = entryTombstone{EntryID: e.EntryID, Title: e.Title, Username: e.Username, TrashedAt: e.TrashedAt, PurgeAfter: e.PurgeAfter}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(out)
}
func (a *App) restoreEntry(w http.ResponseWriter, r *http.Request) {
	id, s, ok := a.authenticated(r)
	if !ok {
		http.Redirect(w, r, "/sign-in", http.StatusSeeOther)
		return
	}
	if !a.parseEntryMultipart(w, r, s) {
		return
	}
	entryID := strings.TrimSpace(r.Form.Get("entry_id"))
	if s.activeVaultID == "" || s.activeHandle == "" || entryID == "" {
		http.Error(w, "Unlock a vault and select an entry before restoring it.", http.StatusBadRequest)
		return
	}
	v, owned, err := a.vault(r.Context(), s, s.activeVaultID)
	if err != nil {
		http.Error(w, "Unable to restore entry.", http.StatusInternalServerError)
		return
	}
	if !owned {
		http.Error(w, "Vault not found.", http.StatusNotFound)
		return
	}
	if a.sessionVaults == nil || a.rust == nil {
		http.Error(w, "Entry restore is unavailable.", http.StatusServiceUnavailable)
		return
	}
	masterPassword, keyfileB64, ok := entryCredentials(w, r)
	if !ok {
		return
	}
	entry, found, err := a.sessionVaults.BeginRestoreEntryForSession(r.Context(), s.userID, s.accessToken, s.activeVaultID, entryID)
	if err != nil {
		http.Error(w, "Unable to restore entry.", http.StatusInternalServerError)
		return
	}
	if !found {
		activeEntries, listErr := a.sessionVaults.DecodedEntries(r.Context(), s.userID, s.accessToken, s.activeVaultID)
		if listErr != nil {
			http.Error(w, "Unable to restore entry.", http.StatusInternalServerError)
			return
		}
		if _, active := findEntry(activeEntries, entryID); active {
			http.Redirect(w, r, "/vaults/browse", http.StatusSeeOther)
			return
		}
		http.Error(w, "Entry not found.", http.StatusNotFound)
		return
	}
	// The Rust service restores the KDBX tombstone itself. The browser-facing
	// service must not send a decoded tombstone's secret fields back over this
	// private request; only the original group preference and credentials are needed.
	var groupID *string
	if entry.GroupID != "" {
		groupID = &entry.GroupID
	}
	restored, err := a.rust.RestoreEntry(r.Context(), privateclient.RestoreEntryRequest{HandleRequest: privateclient.HandleRequest{Handle: s.activeHandle, UserID: s.userID, VaultID: s.activeVaultID}, EntryID: entryID, PreferredGroupID: groupID, MasterPassword: masterPassword, KeyfileB64: keyfileB64})
	if err != nil {
		a.recoverMutationHandle(r.Context(), id, s, v, masterPassword, keyfileB64)
		_ = a.sessionVaults.AbortEntryTransitionForSession(r.Context(), s.userID, s.accessToken, s.activeVaultID, entryID, "restoring")
		http.Error(w, "Unable to save the vault. Check the master password or key file.", http.StatusUnprocessableEntity)
		return
	}
	vaultBytes, ok := decodedVaultBytes(w, restored.DatabaseB64)
	if !ok {
		a.recoverMutationHandle(r.Context(), id, s, v, masterPassword, keyfileB64)
		_ = a.sessionVaults.AbortEntryTransitionForSession(r.Context(), s.userID, s.accessToken, s.activeVaultID, entryID, "restoring")
		return
	}
	if err := a.sessionVaults.ReplaceForSession(r.Context(), s.userID, s.accessToken, v, vaultBytes); err != nil {
		a.recoverMutationHandle(r.Context(), id, s, v, masterPassword, keyfileB64)
		_ = a.sessionVaults.AbortEntryTransitionForSession(r.Context(), s.userID, s.accessToken, s.activeVaultID, entryID, "restoring")
		http.Error(w, "Vault changed but could not be stored. Please try saving again.", http.StatusBadGateway)
		return
	}
	if err := a.sessionVaults.RestoreEntryForSession(r.Context(), s.userID, s.accessToken, s.activeVaultID, entryID); err != nil {
		a.recoverMutationHandle(r.Context(), id, s, v, masterPassword, keyfileB64)
		http.Error(w, "Vault saved but the entry could not be restored from trash. Please retry the restore.", http.StatusBadGateway)
		return
	}
	http.Redirect(w, r, "/vaults/browse", http.StatusSeeOther)
}
func (a *App) parseEntryMultipart(w http.ResponseWriter, r *http.Request, s session) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxKeyFileBytes+(1<<20))
	if err := r.ParseMultipartForm(maxKeyFileBytes + 1); err != nil {
		http.Error(w, "Invalid save form.", http.StatusBadRequest)
		return false
	}
	if !a.validCSRF(r, s) {
		http.Error(w, "Your form has expired. Please reload and try again.", http.StatusForbidden)
		return false
	}
	return true
}
func entryCredentials(w http.ResponseWriter, r *http.Request) (*string, *string, bool) {
	password := r.Form.Get("master_password")
	if password == "" {
		http.Error(w, "Enter the master password to change this entry.", http.StatusBadRequest)
		return nil, nil, false
	}
	var keyfileB64 *string
	if file, _, err := r.FormFile("key_file"); err == nil {
		defer file.Close()
		raw, readErr := io.ReadAll(io.LimitReader(file, maxKeyFileBytes+1))
		if readErr != nil || len(raw) > maxKeyFileBytes {
			http.Error(w, "Key file is too large.", http.StatusBadRequest)
			return nil, nil, false
		}
		encoded := base64.StdEncoding.EncodeToString(raw)
		keyfileB64 = &encoded
	}
	return &password, keyfileB64, true
}
func decodedVaultBytes(w http.ResponseWriter, encoded string) ([]byte, bool) {
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(data) == 0 || len(data) > maxVaultBytes {
		http.Error(w, "Unable to save the vault.", http.StatusInternalServerError)
		return nil, false
	}
	return data, true
}

func (a *App) recoverMutationHandle(ctx context.Context, sessionID string, s session, v Vault, password, keyfileB64 *string) {
	if a.rust == nil || a.sessionVaults == nil {
		a.invalidateActiveHandle(ctx, sessionID, s)
		return
	}
	_ = a.rust.Close(ctx, privateclient.HandleRequest{Handle: s.activeHandle, UserID: s.userID, VaultID: s.activeVaultID})
	data, err := a.sessionVaults.DownloadForSession(ctx, s.userID, s.accessToken, v)
	if err != nil {
		a.invalidateActiveHandle(ctx, sessionID, s)
		return
	}
	unlocked, err := a.rust.Unlock(ctx, privateclient.UnlockRequest{
		UserID: s.userID, VaultID: s.activeVaultID, DatabaseB64: base64.StdEncoding.EncodeToString(data),
		Password: password, KeyfileB64: keyfileB64,
	})
	a.mu.Lock()
	defer a.mu.Unlock()
	current, exists := a.sessions[sessionID]
	if !exists || current.activeHandle != s.activeHandle {
		return
	}
	if err != nil {
		current.activeHandle = ""
	} else {
		current.activeHandle = unlocked.Handle
	}
	a.sessions[sessionID] = current
}

func (a *App) invalidateActiveHandle(ctx context.Context, sessionID string, s session) {
	if a.rust != nil && s.activeHandle != "" {
		_ = a.rust.Close(ctx, privateclient.HandleRequest{Handle: s.activeHandle, UserID: s.userID, VaultID: s.activeVaultID})
	}
	a.mu.Lock()
	if current, ok := a.sessions[sessionID]; ok && current.activeHandle == s.activeHandle {
		current.activeHandle, current.activeVaultID = "", ""
		a.sessions[sessionID] = current
	}
	a.mu.Unlock()
}
func containsVault(vaults []Vault, id string) bool {
	for _, vault := range vaults {
		if vault.ID == id {
			return true
		}
	}
	return false
}
func findEntry(entries []DecodedEntry, id string) (DecodedEntry, bool) {
	for _, entry := range entries {
		if entry.EntryID == id {
			return entry, true
		}
	}
	return DecodedEntry{}, false
}
func downloadFilename(name string) string {
	name = strings.TrimSpace(name)
	if strings.HasSuffix(strings.ToLower(name), ".kdbx") {
		name = name[:len(name)-len(".kdbx")]
	}
	var safe strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == ' ', r == '.', r == '-', r == '_':
			safe.WriteRune(r)
		case r == '/', r == '\\':
			safe.WriteByte('_')
		}
	}
	name = strings.Trim(safe.String(), " .")
	if name == "" {
		name = "vault"
	}
	return name + ".kdbx"
}
func (a *App) closeVault(w http.ResponseWriter, r *http.Request) {
	id, s, ok := a.authenticated(r)
	if !ok {
		http.Redirect(w, r, "/sign-in", 303)
		return
	}
	if !a.validCSRF(r, s) {
		http.Error(w, "Your form has expired. Please reload and try again.", 403)
		return
	}
	if err := a.closeActive(r.Context(), s); err != nil {
		http.Error(w, "Unable to close vault safely. Please try again.", 503)
		return
	}
	s.activeHandle = ""
	s.activeVaultID = ""
	a.mu.Lock()
	a.sessions[id] = s
	a.mu.Unlock()
	http.Redirect(w, r, "/vaults", 303)
}
func (a *App) list(ctx context.Context, s session) ([]Vault, error) {
	if a.sessionVaults != nil {
		return a.sessionVaults.ListForSession(ctx, s.userID, s.accessToken)
	}
	if a.vaults == nil {
		return nil, fmt.Errorf("no vault repository")
	}
	return a.vaults.List(s.userID)
}
func (a *App) vault(ctx context.Context, s session, id string) (Vault, bool, error) {
	if a.sessionVaults != nil {
		return a.sessionVaults.VaultForSession(ctx, s.userID, s.accessToken, id)
	}
	if a.vaults == nil {
		return Vault{}, false, nil
	}
	owned, e := a.vaults.Owns(s.userID, id)
	return Vault{ID: id}, owned, e
}
func (a *App) validCSRF(r *http.Request, s session) bool {
	if err := r.ParseForm(); err != nil {
		return false
	}
	return s.csrfToken != "" && r.Form.Get("csrf_token") == s.csrfToken
}
func (a *App) closeActive(ctx context.Context, s session) error {
	if s.activeHandle == "" {
		return nil
	}
	if a.rust == nil {
		return fmt.Errorf("private service unavailable")
	}
	return a.rust.Close(ctx, privateclient.HandleRequest{Handle: s.activeHandle, UserID: s.userID, VaultID: s.activeVaultID})
}
func (a *App) sessionFromCookie(r *http.Request) (string, session, bool) {
	c, e := r.Cookie(sessionCookieName)
	if e != nil || c.Value == "" {
		return "", session{}, false
	}
	a.mu.RLock()
	s, ok := a.sessions[c.Value]
	a.mu.RUnlock()
	return c.Value, s, ok
}
func (a *App) authenticated(r *http.Request) (string, session, bool) {
	id, s, ok := a.sessionFromCookie(r)
	if !ok || s.userID == "" {
		return "", session{}, false
	}
	if a.auth != nil {
		identity, err := a.auth.Validate(r.Context(), s.accessToken)
		if err != nil || identity.ID != s.userID {
			a.mu.Lock()
			delete(a.sessions, id)
			a.mu.Unlock()
			return "", session{}, false
		}
	}
	s.lastActivity = time.Now()
	a.mu.Lock()
	a.sessions[id] = s
	a.mu.Unlock()
	return id, s, true
}
func (a *App) render(w http.ResponseWriter, name string, data any) {
	a.renderStatus(w, http.StatusOK, name, data)
}
func (a *App) renderStatus(w http.ResponseWriter, status int, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := a.tmpl.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, "Unable to render this page.", 500)
	}
}

// templatesGlob locates frontend/templates relative to the process's working
// directory, walking up from cwd when needed (e.g. under `go test`, which
// runs with cwd set to the package directory rather than the repo root). In
// the runtime container, cwd is the app's WORKDIR and frontend/templates/
// lives directly beneath it, so the walk terminates immediately.
func templatesGlob() string {
	dir, err := os.Getwd()
	if err != nil {
		return filepath.Join("frontend", "templates", "*.html")
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "frontend", "templates", "head.html")); err == nil {
			return filepath.Join(dir, "frontend", "templates", "*.html")
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return filepath.Join("frontend", "templates", "*.html")
		}
		dir = parent
	}
}
func isHTMX(r *http.Request) bool { return r.Header.Get("HX-Request") == "true" }
func wantsHTML(r *http.Request) bool {
	return strings.Contains(strings.ToLower(r.Header.Get("Accept")), "text/html")
}
func bearer(r *http.Request) string {
	return strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
}
func randomToken() string {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		panic("secure random source unavailable")
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func newUUID() string {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		panic("secure random source unavailable")
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	raw := fmt.Sprintf("%x", b)
	return raw[:8] + "-" + raw[8:12] + "-" + raw[12:16] + "-" + raw[16:20] + "-" + raw[20:]
}

type vaultPage struct {
	Vaults          []Vault
	CSRFToken       string
	Fragment        bool
	Selected        Vault
	Email           string
	SupabaseURL     string
	SupabaseAnonKey string
	Error           string
}

type vaultTrashPage struct {
	vaultPage
	TrashedVaults []Vault
}

type entryTrashPage struct {
	vaultPage
	Entries []DecodedEntry
}

func formatDate(value *time.Time) string {
	if value == nil {
		return "—"
	}
	return value.UTC().Format("Jan 2, 2006")
}

func templateDict(values ...any) map[string]any {
	result := make(map[string]any, len(values)/2)
	for i := 0; i+1 < len(values); i += 2 {
		key, ok := values[i].(string)
		if ok {
			result[key] = values[i+1]
		}
	}
	return result
}

// SupabaseVaults adapts the REST/storage client without leaking its types into handlers.
type SupabaseVaults struct{ Client *supabase.VaultClient }

func (s SupabaseVaults) ListForSession(c context.Context, u, t string) ([]Vault, error) {
	vs, e := s.Client.List(c, t, u)
	return fromSupabase(vs), e
}
func (s SupabaseVaults) VaultForSession(c context.Context, u, t, id string) (Vault, bool, error) {
	v, ok, e := s.Client.Owns(c, t, u, id)
	return fromOne(v), ok, e
}
func (s SupabaseVaults) UploadForSession(c context.Context, u, t, n, id string, data []byte) (Vault, error) {
	v, e := s.Client.CreateAndUpload(c, t, u, n, id, data)
	return fromOne(v), e
}
func (s SupabaseVaults) DownloadForSession(c context.Context, u, t string, v Vault) ([]byte, error) {
	return s.Client.Download(c, t, v.ObjectPath)
}
func (s SupabaseVaults) DownloadActiveForSession(c context.Context, u, t, vaultID string) ([]byte, error) {
	return s.Client.DownloadActive(c, t, u, vaultID)
}
func (s SupabaseVaults) ReplaceForSession(c context.Context, u, t string, v Vault, data []byte) error {
	return s.Client.Replace(c, t, v.ObjectPath, data)
}
func (s SupabaseVaults) ReplaceDecodedEntries(c context.Context, u, t, vaultID string, entries []DecodedEntry) error {
	rows := make([]supabase.DecodedEntry, len(entries))
	for i, entry := range entries {
		rows[i] = supabase.DecodedEntry{EntryID: entry.EntryID, GroupID: entry.GroupID, Title: entry.Title, Username: entry.Username, Password: entry.Password, URL: entry.URL, Notes: entry.Notes, Fields: entry.Fields, TrashedAt: entry.TrashedAt, PurgeAfter: entry.PurgeAfter}
	}
	return s.Client.ReplaceDecodedEntries(c, t, u, vaultID, rows)
}
func (s SupabaseVaults) DecodedEntries(c context.Context, u, t, vaultID string) ([]DecodedEntry, error) {
	rows, err := s.Client.DecodedEntries(c, t, u, vaultID)
	if err != nil {
		return nil, err
	}
	entries := make([]DecodedEntry, len(rows))
	for i, row := range rows {
		entries[i] = DecodedEntry{EntryID: row.EntryID, GroupID: row.GroupID, Title: row.Title, Username: row.Username, Password: row.Password, URL: row.URL, Notes: row.Notes, Fields: row.Fields, TrashedAt: row.TrashedAt, PurgeAfter: row.PurgeAfter}
	}
	return entries, nil
}
func (s SupabaseVaults) UpdateDecodedEntry(c context.Context, u, t, vaultID, entryID string, entry DecodedEntry) error {
	return s.Client.UpdateDecodedEntry(c, t, u, vaultID, entryID, supabase.DecodedEntry{GroupID: entry.GroupID, Title: entry.Title, Username: entry.Username, Password: entry.Password, URL: entry.URL, Notes: entry.Notes, Fields: entry.Fields})
}
func (s SupabaseVaults) RenameForSession(c context.Context, u, t, vaultID, name string) error {
	return s.Client.Rename(c, t, u, vaultID, name)
}
func (s SupabaseVaults) TrashVaultForSession(c context.Context, u, t, vaultID string) error {
	return s.Client.TrashVault(c, t, u, vaultID)
}
func (s SupabaseVaults) RestoreVaultForSession(c context.Context, u, t, vaultID string) error {
	return s.Client.RestoreVault(c, t, u, vaultID)
}
func (s SupabaseVaults) TrashedVaultsForSession(c context.Context, u, t string) ([]Vault, error) {
	vaults, err := s.Client.ListTrashedVaults(c, t, u)
	return fromSupabase(vaults), err
}
func (s SupabaseVaults) TrashEntryForSession(c context.Context, u, t, vaultID, entryID string) error {
	return s.Client.TrashEntry(c, t, u, vaultID, entryID)
}
func (s SupabaseVaults) RestoreEntryForSession(c context.Context, u, t, vaultID, entryID string) error {
	return s.Client.RestoreEntry(c, t, u, vaultID, entryID)
}
func (s SupabaseVaults) BeginTrashEntryForSession(c context.Context, u, t, vaultID, entryID string) (DecodedEntry, bool, error) {
	row, ok, err := s.Client.BeginTrashEntry(c, t, u, vaultID, entryID)
	return fromDecodedEntry(row), ok, err
}
func (s SupabaseVaults) BeginRestoreEntryForSession(c context.Context, u, t, vaultID, entryID string) (DecodedEntry, bool, error) {
	row, ok, err := s.Client.BeginRestoreEntry(c, t, u, vaultID, entryID)
	return fromDecodedEntry(row), ok, err
}
func (s SupabaseVaults) AbortEntryTransitionForSession(c context.Context, u, t, vaultID, entryID, transition string) error {
	return s.Client.AbortEntryTransition(c, t, u, vaultID, entryID, transition)
}
func (s SupabaseVaults) TrashedEntriesForSession(c context.Context, u, t, vaultID string) ([]DecodedEntry, error) {
	rows, err := s.Client.ListTrashedEntries(c, t, u, vaultID)
	if err != nil {
		return nil, err
	}
	entries := make([]DecodedEntry, len(rows))
	for i, row := range rows {
		entries[i] = fromDecodedEntry(row)
	}
	return entries, nil
}
func fromDecodedEntry(row supabase.DecodedEntry) DecodedEntry {
	return DecodedEntry{EntryID: row.EntryID, GroupID: row.GroupID, Title: row.Title, Username: row.Username, Password: row.Password, URL: row.URL, Notes: row.Notes, Fields: row.Fields, TrashedAt: row.TrashedAt, PurgeAfter: row.PurgeAfter}
}
func fromSupabase(vs []supabase.Vault) []Vault {
	out := make([]Vault, len(vs))
	for i, v := range vs {
		out[i] = fromOne(v)
	}
	return out
}
func fromOne(v supabase.Vault) Vault {
	return Vault{ID: v.ID, Name: v.Name, ObjectPath: v.ObjectPath, TrashedAt: v.TrashedAt, PurgeAfter: v.PurgeAfter}
}

var _ = json.RawMessage{}
