package supabase

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

// Vault is metadata only; KDBX bytes are always handled through private Storage.
type Vault struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	ObjectPath  string     `json:"object_path"`
	UploadState string     `json:"upload_state"`
	TrashedAt   *time.Time `json:"trashed_at,omitempty"`
	PurgeAfter  *time.Time `json:"purge_after,omitempty"`
}
type DecodedEntry struct {
	VaultID     string            `json:"vault_id"`
	OwnerID     string            `json:"owner_id"`
	EntryID     string            `json:"entry_id"`
	GroupID     string            `json:"group_id"`
	Title       string            `json:"title"`
	Username    string            `json:"username"`
	Password    string            `json:"password"`
	URL         string            `json:"url"`
	Notes       string            `json:"notes"`
	Fields      map[string]string `json:"fields"`
	TrashedAt   *time.Time        `json:"trashed_at,omitempty"`
	PurgeAfter  *time.Time        `json:"purge_after,omitempty"`
}
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}
type VaultClient struct {
	base    *url.URL
	anonKey string
	http    HTTPDoer
}

type statusError struct{ status int }

func (e statusError) Error() string {
	return fmt.Sprintf("Supabase request failed: %d", e.status)
}

func NewVaultClient(projectURL, anonKey string, client HTTPDoer) (*VaultClient, error) {
	u, err := url.ParseRequestURI(strings.TrimSpace(projectURL))
	if err != nil || !validPublicURL(projectURL) || strings.TrimSpace(anonKey) == "" {
		return nil, fmt.Errorf("invalid Supabase public configuration")
	}
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	return &VaultClient{base: u, anonKey: anonKey, http: client}, nil
}
func (c *VaultClient) List(ctx context.Context, accessToken, ownerID string) ([]Vault, error) {
	q := activeVaultQuery(ownerID)
	q.Set("order", "created_at.desc")
	var result []Vault
	err := c.request(ctx, http.MethodGet, "/rest/v1/vaults?"+q.Encode(), accessToken, nil, &result)
	return result, err
}
func (c *VaultClient) Owns(ctx context.Context, accessToken, ownerID, vaultID string) (Vault, bool, error) {
	q := activeVaultQuery(ownerID)
	q.Set("id", "eq."+vaultID)
	q.Set("limit", "1")
	var rows []Vault
	if err := c.request(ctx, http.MethodGet, "/rest/v1/vaults?"+q.Encode(), accessToken, nil, &rows); err != nil {
		return Vault{}, false, err
	}
	if len(rows) != 1 {
		return Vault{}, false, nil
	}
	return rows[0], true, nil
}

// Rename changes only an active vault owned by the authenticated user.
func (c *VaultClient) Rename(ctx context.Context, accessToken, ownerID, vaultID, name string) error {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 255 {
		return fmt.Errorf("invalid vault name")
	}
	q := activeVaultQuery(ownerID)
	q.Set("id", "eq."+vaultID)
	return c.request(ctx, http.MethodPatch, "/rest/v1/vaults?"+q.Encode(), accessToken, map[string]string{"name": name}, nil)
}

// TrashVault is idempotent: the active-row predicate ensures an existing
// tombstone keeps its original retention deadline.
func (c *VaultClient) TrashVault(ctx context.Context, accessToken, ownerID, vaultID string) error {
	q := activeVaultQuery(ownerID)
	q.Set("id", "eq."+vaultID)
	return c.trash(ctx, accessToken, "/rest/v1/vaults?"+q.Encode())
}

func (c *VaultClient) RestoreVault(ctx context.Context, accessToken, ownerID, vaultID string) error {
	q := url.Values{"owner_id": {"eq." + ownerID}, "id": {"eq." + vaultID}, "upload_state": {"eq.uploaded"}, "trashed_at": {"not.is.null"}}
	return c.request(ctx, http.MethodPatch, "/rest/v1/vaults?"+q.Encode(), accessToken, clearLifecyclePayload(), nil)
}

func (c *VaultClient) ListTrashedVaults(ctx context.Context, accessToken, ownerID string) ([]Vault, error) {
	q := trashedQuery(ownerID, time.Now().UTC())
	q.Set("select", "id,name,object_path,upload_state,trashed_at,purge_after")
	q.Set("upload_state", "eq.uploaded")
	q.Set("order", "trashed_at.desc")
	var result []Vault
	err := c.request(ctx, http.MethodGet, "/rest/v1/vaults?"+q.Encode(), accessToken, nil, &result)
	return result, err
}
func (c *VaultClient) CreateAndUpload(ctx context.Context, accessToken, ownerID, name, vaultID string, data []byte) (Vault, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 255 || !uuidLike(ownerID) || !uuidLike(vaultID) {
		return Vault{}, fmt.Errorf("invalid vault metadata")
	}
	v := Vault{ID: vaultID, Name: name, ObjectPath: ownerID + "/" + vaultID + ".kdbx", UploadState: "pending"}
	// The authenticated JWT is authoritative at RLS time; owner_id is sent only
	// to satisfy the table's non-null column and is constrained to auth.uid().
	if err := c.request(ctx, http.MethodPost, "/rest/v1/vaults", accessToken, []map[string]string{{"id": v.ID, "name": v.Name, "object_path": v.ObjectPath, "upload_state": v.UploadState, "owner_id": ownerID}}, nil); err != nil {
		return Vault{}, err
	}
	if err := c.request(ctx, http.MethodPost, "/storage/v1/object/vaults/"+v.ObjectPath, accessToken, data, nil); err != nil {
		c.compensate(ctx, accessToken, v)
		return Vault{}, err
	}
	if err := c.request(ctx, http.MethodPatch, "/rest/v1/vaults?id=eq."+url.QueryEscape(v.ID)+"&owner_id=eq."+url.QueryEscape(ownerID), accessToken, map[string]string{"upload_state": "uploaded"}, nil); err != nil {
		c.compensate(ctx, accessToken, v)
		return Vault{}, err
	}
	return v, nil
}

// compensate makes a failed multi-request upload invisible and attempts to
// remove both resources. Errors are intentionally not returned because the
// original upload failure is the actionable result and RLS prevents exposure.
func (c *VaultClient) compensate(ctx context.Context, accessToken string, v Vault) {
	_ = c.request(ctx, http.MethodDelete, "/storage/v1/object/vaults/"+v.ObjectPath, accessToken, nil, nil)
	_ = c.request(ctx, http.MethodDelete, "/rest/v1/vaults?id=eq."+url.QueryEscape(v.ID), accessToken, nil, nil)
}
func (c *VaultClient) Download(ctx context.Context, accessToken, objectPath string) ([]byte, error) {
	var data []byte
	err := c.request(ctx, http.MethodGet, "/storage/v1/object/vaults/"+cleanObjectPath(objectPath), accessToken, nil, &data)
	return data, err
}

// DownloadActive obtains encrypted bytes only after an active ownership check.
// It never opens or decodes the KDBX object.
func (c *VaultClient) DownloadActive(ctx context.Context, accessToken, ownerID, vaultID string) ([]byte, error) {
	vault, ok, err := c.Owns(ctx, accessToken, ownerID, vaultID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("vault not found")
	}
	return c.Download(ctx, accessToken, vault.ObjectPath)
}
func (c *VaultClient) Replace(ctx context.Context, accessToken, objectPath string, data []byte) error {
	return c.request(ctx, http.MethodPut, "/storage/v1/object/vaults/"+cleanObjectPath(objectPath), accessToken, data, nil)
}

// ReplaceDecodedEntries remains for the existing application seam. It now
// performs an active-only synchronization so it cannot erase tombstones.
func (c *VaultClient) ReplaceDecodedEntries(ctx context.Context, accessToken, ownerID, vaultID string, entries []DecodedEntry) error {
	return c.SyncActiveDecodedEntries(ctx, accessToken, ownerID, vaultID, entries)
}

// SyncActiveDecodedEntries upserts the decoded KDBX mirror and removes only
// active rows absent from that KDBX. Trashed rows remain recoverable.
func (c *VaultClient) SyncActiveDecodedEntries(ctx context.Context, accessToken, ownerID, vaultID string, entries []DecodedEntry) error {
	if !uuidLike(ownerID) || !uuidLike(vaultID) {
		return fmt.Errorf("invalid decoded entry scope")
	}
	entryIDs := make([]string, len(entries))
	seen := make(map[string]struct{}, len(entries))
	for i := range entries {
		if !uuidLike(entries[i].EntryID) || !uuidLike(entries[i].GroupID) {
			return fmt.Errorf("invalid decoded entry")
		}
		if _, exists := seen[entries[i].EntryID]; exists {
			return fmt.Errorf("duplicate decoded entry")
		}
		seen[entries[i].EntryID] = struct{}{}
		entryIDs[i] = entries[i].EntryID
		entries[i].VaultID, entries[i].OwnerID = vaultID, ownerID
		entries[i].TrashedAt, entries[i].PurgeAfter = nil, nil
		if entries[i].Fields == nil {
			entries[i].Fields = map[string]string{}
		}
	}
	if len(entries) > 0 {
		q := url.Values{"on_conflict": {"vault_id,entry_id"}}
		if err := c.requestWithHeaders(ctx, http.MethodPost, "/rest/v1/decoded_vault_entries?"+q.Encode(), accessToken, entries, nil, http.Header{"Prefer": {"resolution=merge-duplicates,return=minimal"}}); err != nil {
			return err
		}
	}
	q := activeEntryQuery(ownerID, vaultID)
	if len(entryIDs) > 0 {
		q.Set("entry_id", "not.in.("+strings.Join(entryIDs, ",")+")")
	}
	return c.request(ctx, http.MethodDelete, "/rest/v1/decoded_vault_entries?"+q.Encode(), accessToken, nil, nil)
}
func (c *VaultClient) DecodedEntries(ctx context.Context, accessToken, ownerID, vaultID string) ([]DecodedEntry, error) {
	var out []DecodedEntry
	q := activeEntryQuery(ownerID, vaultID)
	q.Set("order", "title.asc")
	err := c.request(ctx, http.MethodGet, "/rest/v1/decoded_vault_entries?"+q.Encode(), accessToken, nil, &out)
	return out, err
}
func (c *VaultClient) UpdateDecodedEntry(ctx context.Context, accessToken, ownerID, vaultID, entryID string, entry DecodedEntry) error {
	q := activeEntryQuery(ownerID, vaultID)
	q.Set("entry_id", "eq."+entryID)
	return c.request(ctx, http.MethodPatch, "/rest/v1/decoded_vault_entries?"+q.Encode(), accessToken, map[string]any{
		"title": entry.Title, "username": entry.Username, "password": entry.Password, "url": entry.URL, "notes": entry.Notes,
	}, nil)
}

func (c *VaultClient) TrashEntry(ctx context.Context, accessToken, ownerID, vaultID, entryID string) error {
	q := activeEntryQuery(ownerID, vaultID)
	q.Set("entry_id", "eq."+entryID)
	return c.trash(ctx, accessToken, "/rest/v1/decoded_vault_entries?"+q.Encode())
}

func (c *VaultClient) RestoreEntry(ctx context.Context, accessToken, ownerID, vaultID, entryID string) error {
	q := url.Values{"owner_id": {"eq." + ownerID}, "vault_id": {"eq." + vaultID}, "entry_id": {"eq." + entryID}, "trashed_at": {"not.is.null"}}
	return c.request(ctx, http.MethodPatch, "/rest/v1/decoded_vault_entries?"+q.Encode(), accessToken, clearLifecyclePayload(), nil)
}

func (c *VaultClient) ListTrashedEntries(ctx context.Context, accessToken, ownerID, vaultID string) ([]DecodedEntry, error) {
	q := trashedQuery(ownerID, time.Now().UTC())
	q.Set("vault_id", "eq."+vaultID)
	q.Set("order", "trashed_at.desc")
	var result []DecodedEntry
	err := c.request(ctx, http.MethodGet, "/rest/v1/decoded_vault_entries?"+q.Encode(), accessToken, nil, &result)
	return result, err
}

// PurgeExpiredTrash is intended for a server-only service-role credential.
// Storage objects are removed only with the Storage API, before their guarded
// vault metadata; no direct storage SQL operation is attempted.
func (c *VaultClient) PurgeExpiredTrash(ctx context.Context, serviceToken string) error {
	now := time.Now().UTC()
	q := url.Values{"select": {"id,object_path"}, "trashed_at": {"not.is.null"}, "purge_after": {"lte." + now.Format(time.RFC3339Nano)}}
	var vaults []Vault
	if err := c.request(ctx, http.MethodGet, "/rest/v1/vaults?"+q.Encode(), serviceToken, nil, &vaults); err != nil {
		return err
	}
	for _, vault := range vaults {
		if err := c.deleteStorageObject(ctx, serviceToken, vault.ObjectPath); err != nil {
			return err
		}
		deleteQuery := url.Values{"id": {"eq." + vault.ID}, "trashed_at": {"not.is.null"}, "purge_after": {"lte." + now.Format(time.RFC3339Nano)}}
		if err := c.request(ctx, http.MethodDelete, "/rest/v1/vaults?"+deleteQuery.Encode(), serviceToken, nil, nil); err != nil {
			return err
		}
	}
	entryQuery := url.Values{"trashed_at": {"not.is.null"}, "purge_after": {"lte." + now.Format(time.RFC3339Nano)}}
	return c.request(ctx, http.MethodDelete, "/rest/v1/decoded_vault_entries?"+entryQuery.Encode(), serviceToken, nil, nil)
}

func (c *VaultClient) deleteStorageObject(ctx context.Context, serviceToken, objectPath string) error {
	err := c.request(ctx, http.MethodDelete, "/storage/v1/object/vaults/"+cleanObjectPath(objectPath), serviceToken, nil, nil)
	var status statusError
	if errors.As(err, &status) && status.status == http.StatusNotFound {
		return nil
	}
	return err
}

func activeVaultQuery(ownerID string) url.Values {
	return url.Values{
		"select":       {"id,name,object_path,upload_state,trashed_at,purge_after"},
		"owner_id":     {"eq." + ownerID},
		"upload_state": {"eq.uploaded"},
		"trashed_at":   {"is.null"},
	}
}

func activeEntryQuery(ownerID, vaultID string) url.Values {
	return url.Values{
		"owner_id":   {"eq." + ownerID},
		"vault_id":   {"eq." + vaultID},
		"trashed_at": {"is.null"},
	}
}

func trashedQuery(ownerID string, now time.Time) url.Values {
	return url.Values{
		"owner_id":     {"eq." + ownerID},
		"trashed_at":   {"not.is.null"},
		"purge_after": {"gt." + now.Format(time.RFC3339Nano)},
	}
}

func clearLifecyclePayload() map[string]any {
	return map[string]any{"trashed_at": nil, "purge_after": nil}
}

func (c *VaultClient) trash(ctx context.Context, accessToken, endpoint string) error {
	trashedAt := time.Now().UTC()
	return c.request(ctx, http.MethodPatch, endpoint, accessToken, map[string]time.Time{
		"trashed_at":  trashedAt,
		"purge_after": trashedAt.AddDate(0, 0, 30),
	}, nil)
}

func cleanObjectPath(objectPath string) string {
	return path.Clean("/" + objectPath)[1:]
}

func (c *VaultClient) request(ctx context.Context, method, endpoint, token string, payload any, out any) error {
	return c.requestWithHeaders(ctx, method, endpoint, token, payload, out, nil)
}

func (c *VaultClient) requestWithHeaders(ctx context.Context, method, endpoint, token string, payload any, out any, headers http.Header) error {
	u := *c.base
	parts := strings.SplitN(endpoint, "?", 2)
	u.Path = strings.TrimRight(c.base.Path, "/") + parts[0]
	if len(parts) == 2 {
		u.RawQuery = parts[1]
	}
	var body io.Reader
	if payload != nil {
		if raw, ok := payload.([]byte); ok {
			body = bytes.NewReader(raw)
		} else {
			raw, e := json.Marshal(payload)
			if e != nil {
				return fmt.Errorf("encode Supabase request: %w", e)
			}
			body = bytes.NewReader(raw)
		}
	}
	req, e := http.NewRequestWithContext(ctx, method, u.String(), body)
	if e != nil {
		return e
	}
	req.Header.Set("apikey", c.anonKey)
	req.Header.Set("Authorization", "Bearer "+token)
	for key, values := range headers {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	if payload != nil {
		if _, ok := payload.([]byte); ok {
			req.Header.Set("Content-Type", "application/octet-stream")
		} else {
			req.Header.Set("Content-Type", "application/json")
		}
	}
	resp, e := c.http.Do(req)
	if e != nil {
		return fmt.Errorf("Supabase request: %w", e)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return statusError{status: resp.StatusCode}
	}
	if out != nil {
		raw, e := io.ReadAll(io.LimitReader(resp.Body, 55<<20))
		if e != nil {
			return e
		}
		if p, ok := out.(*[]byte); ok {
			*p = raw
			return nil
		}
		if len(raw) > 0 {
			if e := json.Unmarshal(raw, out); e != nil {
				return e
			}
		}
	}
	return nil
}
func uuidLike(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, r := range s {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if r != '-' {
				return false
			}
		} else if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return true
}
