package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/lixmal/keepass4web-rs/backend-go/internal/privateclient"
	"github.com/lixmal/keepass4web-rs/backend-go/internal/supabase"
)

func TestMultipartUploadCSRFIsReadBeforeUploadValidation(t *testing.T) {
	app := NewApp(Dependencies{})
	cookie := app.CreateSession("owner-1")
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("csrf_token", app.CSRFToken(cookie.Value)); err != nil {
		t.Fatal(err)
	}
	file, err := writer.CreateFormFile("vault", "test.kdbx")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("kdbx")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/vaults/upload", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.AddCookie(cookie)
	recorder := httptest.NewRecorder()
	app.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("multipart upload status = %d, want %d after CSRF passes", recorder.Code, http.StatusServiceUnavailable)
	}
}

func TestMultipartUnlockCSRFIsReadBeforeUnlockValidation(t *testing.T) {
	app := NewApp(Dependencies{Vaults: fakeVaults{}})
	cookie := app.CreateSession("owner-1")
	app.mu.Lock()
	s := app.sessions[cookie.Value]
	s.activeVaultID = "vault-a"
	app.sessions[cookie.Value] = s
	app.mu.Unlock()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("csrf_token", app.CSRFToken(cookie.Value)); err != nil { t.Fatal(err) }
	if err := writer.WriteField("password", "test"); err != nil { t.Fatal(err) }
	if err := writer.Close(); err != nil { t.Fatal(err) }
	request := httptest.NewRequest(http.MethodPost, "/vaults/unlock", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.AddCookie(cookie)
	recorder := httptest.NewRecorder()

	app.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("multipart unlock status = %d, want %d after CSRF passes", recorder.Code, http.StatusServiceUnavailable)
	}
}

type fakeVaults struct{ vaults []Vault }

func (f fakeVaults) List(_ string) ([]Vault, error)         { return f.vaults, nil }
func (f fakeVaults) Owns(_ string, id string) (bool, error) { return id == "vault-a", nil }

func TestVaultPageRequiresAnAuthenticatedSession(t *testing.T) {
	app := NewApp(Dependencies{Vaults: fakeVaults{}})
	recorder := httptest.NewRecorder()
	app.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/vaults", nil))

	if recorder.Code != http.StatusSeeOther {
		t.Fatalf("GET /vaults status = %d, want %d", recorder.Code, http.StatusSeeOther)
	}
	if location := recorder.Header().Get("Location"); location != "/sign-in" {
		t.Fatalf("redirect location = %q, want /sign-in", location)
	}
}

func TestVaultPageRendersOnlyRepositoryVaultsForSessionOwner(t *testing.T) {
	app := NewApp(Dependencies{Vaults: fakeVaults{vaults: []Vault{{ID: "vault-a", Name: "Personal"}}}})
	cookie := app.CreateSession("owner-1")
	request := httptest.NewRequest(http.MethodGet, "/vaults", nil)
	request.AddCookie(cookie)
	recorder := httptest.NewRecorder()

	app.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /vaults status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if body := recorder.Body.String(); !strings.Contains(body, "Personal") || strings.Contains(body, "owner-1") {
		t.Fatalf("vault page rendered unexpected content: %s", body)
	}
}

func TestSelectVaultRejectsMissingCSRFAndDoesNotStoreHandle(t *testing.T) {
	app := NewApp(Dependencies{Vaults: fakeVaults{}})
	cookie := app.CreateSession("owner-1")
	form := url.Values{"vault_id": {"vault-a"}}
	request := httptest.NewRequest(http.MethodPost, "/vaults/select", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(cookie)
	recorder := httptest.NewRecorder()

	app.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("POST /vaults/select status = %d, want %d", recorder.Code, http.StatusForbidden)
	}
	if got := app.ActiveHandle(cookie.Value); got != "" {
		t.Fatalf("active handle = %q, want empty", got)
	}
}

func TestSelectVaultAcceptsCSRFAndDefersHandleUntilUnlock(t *testing.T) {
	app := NewApp(Dependencies{Vaults: fakeVaults{}})
	cookie := app.CreateSession("owner-1")
	csrf := app.CSRFToken(cookie.Value)
	form := url.Values{"vault_id": {"vault-a"}, "csrf_token": {csrf}}
	request := httptest.NewRequest(http.MethodPost, "/vaults/select", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(cookie)
	recorder := httptest.NewRecorder()

	app.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusSeeOther {
		t.Fatalf("POST /vaults/select status = %d, want %d", recorder.Code, http.StatusSeeOther)
	}
	if got := app.ActiveHandle(cookie.Value); got != "" {
		t.Fatalf("active handle = %q, want no handle before unlock", got)
	}
}

func TestSelectVaultHTMXRedirectsToUnlockPage(t *testing.T) {
	app := NewApp(Dependencies{Vaults: fakeVaults{}})
	cookie := app.CreateSession("owner-1")
	form := url.Values{"vault_id": {"vault-a"}, "csrf_token": {app.CSRFToken(cookie.Value)}}
	request := httptest.NewRequest(http.MethodPost, "/vaults/select", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("HX-Request", "true")
	request.AddCookie(cookie)
	recorder := httptest.NewRecorder()

	app.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNoContent || recorder.Header().Get("HX-Redirect") != "/vaults/unlock" {
		t.Fatalf("HTMX selection = %d redirect %q, want 204 redirect to unlock", recorder.Code, recorder.Header().Get("HX-Redirect"))
	}
}

type fakeAuth struct {
	identity supabase.Identity
	err      error
	calls    int
}

func (f *fakeAuth) Validate(context.Context, string) (supabase.Identity, error) {
	f.calls++
	return f.identity, f.err
}

type fakeRust struct {
	closeErr       error
	createEntryErr error
	closed         []privateclient.HandleRequest
}

func (f *fakeRust) Unlock(context.Context, privateclient.UnlockRequest) (privateclient.UnlockResponse, error) {
	return privateclient.UnlockResponse{}, errors.New("not implemented")
}
func (f *fakeRust) Groups(context.Context, privateclient.HandleRequest) (privateclient.GroupsResponse, error) {
	return privateclient.GroupsResponse{}, errors.New("not implemented")
}
func (f *fakeRust) GroupEntries(context.Context, privateclient.HandleRequest, string) (privateclient.EntryGroupResponse, error) {
	return privateclient.EntryGroupResponse{}, errors.New("not implemented")
}
func (f *fakeRust) Entry(context.Context, privateclient.HandleRequest, string) (privateclient.EntryResponse, error) {
	return privateclient.EntryResponse{}, errors.New("not implemented")
}
func (f *fakeRust) Reveal(context.Context, privateclient.HandleRequest, string, string) (privateclient.RevealResponse, error) {
	return privateclient.RevealResponse{}, errors.New("not implemented")
}
func (f *fakeRust) Update(context.Context, privateclient.UpdateRequest) (privateclient.UpdateResponse, error) {
	return privateclient.UpdateResponse{}, errors.New("not implemented")
}
func (f *fakeRust) Close(_ context.Context, request privateclient.HandleRequest) error {
	f.closed = append(f.closed, request)
	return f.closeErr
}
func (f *fakeRust) CreateVault(context.Context, privateclient.CreateVaultRequest) (privateclient.CreateVaultResponse, error) {
	return privateclient.CreateVaultResponse{DatabaseB64: "ZmFrZS1rZGJ4"}, nil
}
func (f *fakeRust) CreateEntry(context.Context, privateclient.CreateEntryRequest) (privateclient.CreateEntryResponse, error) {
	if f.createEntryErr != nil {
		return privateclient.CreateEntryResponse{}, f.createEntryErr
	}
	return privateclient.CreateEntryResponse{EntryID: "11111111-1111-1111-1111-111111111111", DatabaseB64: "ZmFrZS1rZGJ4"}, nil
}

type fakeSessionVaultsForCreate struct {
	uploadedName  string
	uploadedBytes []byte
	vault         Vault
}

func (f *fakeSessionVaultsForCreate) ListForSession(context.Context, string, string) ([]Vault, error) {
	return nil, nil
}
func (f *fakeSessionVaultsForCreate) VaultForSession(context.Context, string, string, string) (Vault, bool, error) {
	if f.vault.ID == "" {
		return Vault{}, false, nil
	}
	return f.vault, true, nil
}
func (f *fakeSessionVaultsForCreate) UploadForSession(_ context.Context, _ string, _ string, name string, id string, data []byte) (Vault, error) {
	f.uploadedName = name
	f.uploadedBytes = data
	return Vault{ID: id, Name: name}, nil
}
func (f *fakeSessionVaultsForCreate) DownloadForSession(context.Context, string, string, Vault) ([]byte, error) {
	return nil, nil
}
func (f *fakeSessionVaultsForCreate) ReplaceForSession(context.Context, string, string, Vault, []byte) error {
	return nil
}
func (f *fakeSessionVaultsForCreate) ReplaceDecodedEntries(context.Context, string, string, string, []DecodedEntry) error {
	return nil
}
func (f *fakeSessionVaultsForCreate) DecodedEntries(context.Context, string, string, string) ([]DecodedEntry, error) {
	return nil, nil
}
func (f *fakeSessionVaultsForCreate) UpdateDecodedEntry(context.Context, string, string, string, string, DecodedEntry) error {
	return nil
}

func TestProtectedRequestRejectsRevokedSupabaseToken(t *testing.T) {
	auth := &fakeAuth{err: errors.New("revoked")}
	app := NewApp(Dependencies{Vaults: fakeVaults{}, Auth: auth})
	cookie := app.createSession("owner-1", "revoked-token")
	request := httptest.NewRequest(http.MethodGet, "/vaults", nil)
	request.AddCookie(cookie)
	recorder := httptest.NewRecorder()

	app.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusSeeOther || recorder.Header().Get("Location") != "/sign-in" {
		t.Fatalf("revoked request = %d redirect %q, want sign-in redirect", recorder.Code, recorder.Header().Get("Location"))
	}
	if auth.calls != 1 {
		t.Fatalf("token validations = %d, want 1", auth.calls)
	}
}

func TestProtectedRequestRejectsTokenForDifferentUser(t *testing.T) {
	auth := &fakeAuth{identity: supabase.Identity{ID: "other-user"}}
	app := NewApp(Dependencies{Vaults: fakeVaults{}, Auth: auth})
	cookie := app.createSession("owner-1", "valid-other-user-token")
	request := httptest.NewRequest(http.MethodGet, "/vaults", nil)
	request.AddCookie(cookie)
	recorder := httptest.NewRecorder()

	app.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusSeeOther {
		t.Fatalf("mismatched identity status = %d, want %d", recorder.Code, http.StatusSeeOther)
	}
}

func TestSignInRequiresCSRFTokenFromSignInPage(t *testing.T) {
	auth := &fakeAuth{identity: supabase.Identity{ID: "owner-1"}}
	app := NewApp(Dependencies{Auth: auth})
	page := httptest.NewRecorder()
	app.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/sign-in", nil))
	cookie := page.Result().Cookies()[0]

	request := httptest.NewRequest(http.MethodPost, "/session", strings.NewReader(url.Values{"access_token": {"token"}}.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(cookie)
	recorder := httptest.NewRecorder()
	app.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("sign-in without CSRF = %d, want %d", recorder.Code, http.StatusForbidden)
	}
}

func TestSelectVaultDoesNotDiscardHandleUntilRustAcknowledgesClose(t *testing.T) {
	rust := &fakeRust{closeErr: errors.New("offline")}
	app := NewApp(Dependencies{Vaults: fakeVaults{}, Rust: rust})
	cookie := app.CreateSession("owner-1")
	app.mu.Lock()
	s := app.sessions[cookie.Value]
	s.activeHandle, s.activeVaultID = "opaque-handle", "vault-a"
	app.sessions[cookie.Value] = s
	app.mu.Unlock()
	form := url.Values{"vault_id": {"vault-a"}, "csrf_token": {app.CSRFToken(cookie.Value)}}
	request := httptest.NewRequest(http.MethodPost, "/vaults/select", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(cookie)
	recorder := httptest.NewRecorder()

	app.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusServiceUnavailable || app.ActiveHandle(cookie.Value) != "opaque-handle" {
		t.Fatalf("select after rejected close = %d handle %q, want retained handle", recorder.Code, app.ActiveHandle(cookie.Value))
	}
	if len(rust.closed) != 1 || rust.closed[0].Handle != "opaque-handle" {
		t.Fatalf("close calls = %#v, want active handle close", rust.closed)
	}
}

func TestPagesLoadBundledStyleAndHTMX(t *testing.T) {
	app := NewApp(Dependencies{})
	recorder := httptest.NewRecorder()
	app.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/sign-in", nil))
	for _, asset := range []string{"/static/tailwind.css", "/static/htmx.min.js"} {
		if !strings.Contains(recorder.Body.String(), asset) {
			t.Fatalf("sign-in page is missing %q", asset)
		}
	}
}

func TestCreateVaultRequiresCSRF(t *testing.T) {
	app := NewApp(Dependencies{Vaults: fakeVaults{}, Rust: &fakeRust{}, SessionVaults: &fakeSessionVaultsForCreate{}})
	cookie := app.CreateSession("owner-1")
	form := url.Values{"name": {"New vault"}, "password": {"test"}}
	request := httptest.NewRequest(http.MethodPost, "/vaults/create", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(cookie)
	recorder := httptest.NewRecorder()

	app.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("POST /vaults/create without CSRF = %d, want %d", recorder.Code, http.StatusForbidden)
	}
}

func TestCreateVaultUploadsTheGeneratedDatabase(t *testing.T) {
	sessionVaults := &fakeSessionVaultsForCreate{}
	app := NewApp(Dependencies{Rust: &fakeRust{}, SessionVaults: sessionVaults})
	cookie := app.CreateSession("owner-1")
	form := url.Values{"csrf_token": {app.CSRFToken(cookie.Value)}, "name": {"New vault"}, "password": {"test"}}
	request := httptest.NewRequest(http.MethodPost, "/vaults/create", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(cookie)
	recorder := httptest.NewRecorder()

	app.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusSeeOther {
		t.Fatalf("POST /vaults/create status = %d, want %d", recorder.Code, http.StatusSeeOther)
	}
	if sessionVaults.uploadedName != "New vault" || len(sessionVaults.uploadedBytes) == 0 {
		t.Fatalf("upload not recorded: name=%q bytes=%d", sessionVaults.uploadedName, len(sessionVaults.uploadedBytes))
	}
}

func TestCreateEntryRequiresAnUnlockedVault(t *testing.T) {
	app := NewApp(Dependencies{Vaults: fakeVaults{}, Rust: &fakeRust{}})
	cookie := app.CreateSession("owner-1")
	form := url.Values{"csrf_token": {app.CSRFToken(cookie.Value)}, "title": {"New Site"}}
	request := httptest.NewRequest(http.MethodPost, "/vaults/entries/create", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(cookie)
	recorder := httptest.NewRecorder()

	app.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("POST /vaults/entries/create without an unlocked vault = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
}

func TestCreateEntryRendersBrowseErrorWhenReEncryptionFails(t *testing.T) {
	sessionVaults := &fakeSessionVaultsForCreate{vault: Vault{ID: "vault-a", Name: "Test vault"}}
	app := NewApp(Dependencies{Rust: &fakeRust{createEntryErr: errors.New("wrong master password")}, SessionVaults: sessionVaults})
	cookie := app.CreateSession("owner-1")
	app.mu.Lock()
	s := app.sessions[cookie.Value]
	s.activeVaultID, s.activeHandle = "vault-a", "opaque-handle"
	app.sessions[cookie.Value] = s
	app.mu.Unlock()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, value := range map[string]string{
		"csrf_token":      app.CSRFToken(cookie.Value),
		"title":           "New Site",
		"master_password": "wrong",
	} {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/vaults/entries/create", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.AddCookie(cookie)
	recorder := httptest.NewRecorder()

	app.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("POST /vaults/entries/create failed status = %d, want %d", recorder.Code, http.StatusUnprocessableEntity)
	}
	if !strings.Contains(recorder.Body.String(), `id="browse-error"`) || !strings.Contains(recorder.Body.String(), "Check the master password or key file.") {
		t.Fatalf("failed create entry did not render the browse error banner: %s", recorder.Body.String())
	}
}

func TestIdleSweepClosesAnInactiveVaultHandle(t *testing.T) {
	rust := &fakeRust{}
	app := NewApp(Dependencies{Vaults: fakeVaults{}, Rust: rust, IdleTimeout: 20 * time.Millisecond, SweepInterval: 5 * time.Millisecond})
	cookie := app.CreateSession("owner-1")
	app.mu.Lock()
	s := app.sessions[cookie.Value]
	s.activeHandle, s.activeVaultID = "opaque-handle", "vault-a"
	s.lastActivity = time.Now().Add(-time.Hour)
	app.sessions[cookie.Value] = s
	app.mu.Unlock()

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if app.ActiveHandle(cookie.Value) == "" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	if got := app.ActiveHandle(cookie.Value); got != "" {
		t.Fatalf("active handle after idle sweep = %q, want empty", got)
	}
	if len(rust.closed) != 1 || rust.closed[0].Handle != "opaque-handle" {
		t.Fatalf("close calls = %#v, want one close of opaque-handle", rust.closed)
	}
}

func TestIdleSweepInvalidatesTheBrowserSession(t *testing.T) {
	rust := &fakeRust{}
	app := NewApp(Dependencies{Vaults: fakeVaults{}, Rust: rust, IdleTimeout: 20 * time.Millisecond, SweepInterval: 5 * time.Millisecond})
	cookie := app.CreateSession("owner-1")
	app.mu.Lock()
	s := app.sessions[cookie.Value]
	s.activeHandle, s.activeVaultID = "opaque-handle", "vault-a"
	s.lastActivity = time.Now().Add(-time.Hour)
	app.sessions[cookie.Value] = s
	app.mu.Unlock()

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		request := httptest.NewRequest(http.MethodGet, "/session/status", nil)
		request.AddCookie(cookie)
		recorder := httptest.NewRecorder()
		app.ServeHTTP(recorder, request)
		var status struct { Active bool `json:"active"` }
		if err := json.NewDecoder(recorder.Body).Decode(&status); err != nil {
			t.Fatal(err)
		}
		if !status.Active {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("idle vault close left the browser session active")
}

func TestSessionStatusReportsRemainingIdleSeconds(t *testing.T) {
	app := NewApp(Dependencies{Vaults: fakeVaults{}, IdleTimeout: 60 * time.Second})
	cookie := app.CreateSession("owner-1")
	request := httptest.NewRequest(http.MethodGet, "/session/status", nil)
	request.AddCookie(cookie)
	recorder := httptest.NewRecorder()

	app.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /session/status status = %d, want %d", recorder.Code, http.StatusOK)
	}
	var body struct {
		Active               bool `json:"active"`
		VaultActive          bool `json:"vault_active"`
		IdleSecondsRemaining int  `json:"idle_seconds_remaining"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !body.Active || body.VaultActive || body.IdleSecondsRemaining <= 0 {
		t.Fatalf("unexpected status body: %+v", body)
	}
}

func TestHeartbeatRequiresCSRFAndResetsIdleClock(t *testing.T) {
	app := NewApp(Dependencies{Vaults: fakeVaults{}})
	cookie := app.CreateSession("owner-1")
	app.mu.Lock()
	s := app.sessions[cookie.Value]
	s.lastActivity = time.Now().Add(-time.Hour)
	app.sessions[cookie.Value] = s
	app.mu.Unlock()

	form := url.Values{"csrf_token": {app.CSRFToken(cookie.Value)}}
	request := httptest.NewRequest(http.MethodPost, "/session/heartbeat", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(cookie)
	recorder := httptest.NewRecorder()

	app.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("POST /session/heartbeat status = %d, want %d", recorder.Code, http.StatusNoContent)
	}
	app.mu.RLock()
	updated := app.sessions[cookie.Value].lastActivity
	app.mu.RUnlock()
	if time.Since(updated) > time.Second {
		t.Fatalf("lastActivity not refreshed by heartbeat: %v", updated)
	}
}
