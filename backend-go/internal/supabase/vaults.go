package supabase

import (
	"bytes"
	"context"
	"encoding/json"
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
	ID          string `json:"id"`
	Name        string `json:"name"`
	ObjectPath  string `json:"object_path"`
	UploadState string `json:"upload_state"`
}
type DecodedEntry struct {
	VaultID string `json:"vault_id"`
	OwnerID string `json:"owner_id"`
	EntryID string `json:"entry_id"`
	GroupID string `json:"group_id"`
	Title string `json:"title"`
	Username string `json:"username"`
	Password string `json:"password"`
	URL string `json:"url"`
	Notes string `json:"notes"`
	Fields map[string]string `json:"fields"`
}
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}
type VaultClient struct {
	base    *url.URL
	anonKey string
	http    HTTPDoer
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
	q := url.Values{"select": {"id,name,object_path,upload_state"}, "owner_id": {"eq." + ownerID}, "upload_state": {"eq.uploaded"}, "order": {"created_at.desc"}}
	var result []Vault
	err := c.request(ctx, http.MethodGet, "/rest/v1/vaults?"+q.Encode(), accessToken, nil, &result)
	return result, err
}
func (c *VaultClient) Owns(ctx context.Context, accessToken, ownerID, vaultID string) (Vault, bool, error) {
	q := url.Values{"select": {"id,name,object_path,upload_state"}, "owner_id": {"eq." + ownerID}, "id": {"eq." + vaultID}, "upload_state": {"eq.uploaded"}, "limit": {"1"}}
	var rows []Vault
	if err := c.request(ctx, http.MethodGet, "/rest/v1/vaults?"+q.Encode(), accessToken, nil, &rows); err != nil {
		return Vault{}, false, err
	}
	if len(rows) != 1 {
		return Vault{}, false, nil
	}
	return rows[0], true, nil
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
	err := c.request(ctx, http.MethodGet, "/storage/v1/object/vaults/"+path.Clean("/" + objectPath)[1:], accessToken, nil, &data)
	return data, err
}
func (c *VaultClient) Replace(ctx context.Context, accessToken, objectPath string, data []byte) error {
	return c.request(ctx, http.MethodPut, "/storage/v1/object/vaults/"+path.Clean("/"+objectPath)[1:], accessToken, data, nil)
}
func (c *VaultClient) ReplaceDecodedEntries(ctx context.Context, accessToken, ownerID, vaultID string, entries []DecodedEntry) error {
	if err := c.request(ctx, http.MethodDelete, "/rest/v1/decoded_vault_entries?vault_id=eq."+url.QueryEscape(vaultID), accessToken, nil, nil); err != nil { return err }
	if len(entries) == 0 { return nil }
	for i := range entries { entries[i].VaultID, entries[i].OwnerID = vaultID, ownerID }
	return c.request(ctx, http.MethodPost, "/rest/v1/decoded_vault_entries", accessToken, entries, nil)
}
func (c *VaultClient) DecodedEntries(ctx context.Context, accessToken, ownerID, vaultID string) ([]DecodedEntry, error) {
	var out []DecodedEntry
	q := url.Values{"vault_id": {"eq." + vaultID}, "owner_id": {"eq." + ownerID}, "order": {"title.asc"}}
	err := c.request(ctx, http.MethodGet, "/rest/v1/decoded_vault_entries?"+q.Encode(), accessToken, nil, &out)
	return out, err
}
func (c *VaultClient) UpdateDecodedEntry(ctx context.Context, accessToken, ownerID, vaultID, entryID string, entry DecodedEntry) error {
	q := url.Values{"vault_id": {"eq." + vaultID}, "entry_id": {"eq." + entryID}, "owner_id": {"eq." + ownerID}}
	return c.request(ctx, http.MethodPatch, "/rest/v1/decoded_vault_entries?"+q.Encode(), accessToken, map[string]any{
		"title": entry.Title, "username": entry.Username, "password": entry.Password, "url": entry.URL, "notes": entry.Notes,
	}, nil)
}
func (c *VaultClient) request(ctx context.Context, method, endpoint, token string, payload any, out any) error {
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
		return fmt.Errorf("Supabase request failed: %d", resp.StatusCode)
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
