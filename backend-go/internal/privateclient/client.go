// Package privateclient is the authenticated, server-to-server boundary to
// the Rust vault process.  It is deliberately unusable from browser code.
package privateclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var ErrUnavailable = errors.New("private vault service unavailable")
var ErrRejected = errors.New("private vault request rejected")

type Client struct {
	base       *url.URL
	token      string
	httpClient *http.Client
}

func New(baseURL, token string, client *http.Client) (*Client, error) {
	u, err := url.ParseRequestURI(strings.TrimSpace(baseURL))
	if err != nil || u.Scheme != "http" || u.Host == "" || strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("invalid private service configuration")
	}
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return &Client{base: u, token: token, httpClient: client}, nil
}

type UnlockRequest struct {
	UserID      string  `json:"user_id"`
	VaultID     string  `json:"vault_id"`
	DatabaseB64 string  `json:"database_b64"`
	Password    *string `json:"password,omitempty"`
	KeyfileB64  *string `json:"keyfile_b64,omitempty"`
}
type UnlockResponse struct {
	Handle           string `json:"handle"`
	ExpiresInSeconds int64  `json:"expires_in_seconds"`
}
type HandleRequest struct {
	Handle  string `json:"handle"`
	UserID  string `json:"user_id"`
	VaultID string `json:"vault_id"`
}
type GroupsResponse struct {
	Groups       json.RawMessage `json:"groups"`
	LastSelected *string         `json:"last_selected"`
}
type Group struct {
	ID       string  `json:"id"`
	Children []Group `json:"children"`
}
type EntrySummary struct {
	ID string `json:"id"`
}
type EntryGroupResponse struct {
	Entries []EntrySummary `json:"entries"`
}
type EntryResponse struct {
	ID        string                     `json:"id"`
	Title     *string                    `json:"title"`
	Username  *string                    `json:"username"`
	Notes     *string                    `json:"notes"`
	URL       *string                    `json:"url"`
	Strings   map[string]*string         `json:"strings"`
	Protected map[string]json.RawMessage `json:"protected"`
}
type RevealResponse struct {
	Value string `json:"value"`
}
type UpdateRequest struct {
	Handle         string  `json:"handle"`
	UserID         string  `json:"user_id"`
	VaultID        string  `json:"vault_id"`
	EntryID        string  `json:"entry_id"`
	Title          string  `json:"title"`
	Username       string  `json:"username"`
	Password       string  `json:"password"`
	URL            string  `json:"url"`
	Notes          string  `json:"notes"`
	MasterPassword *string `json:"master_password,omitempty"`
	KeyfileB64     *string `json:"keyfile_b64,omitempty"`
}
type UpdateResponse struct {
	DatabaseB64 string `json:"database_b64"`
}
type CreateVaultRequest struct {
	Password   *string `json:"password,omitempty"`
	KeyfileB64 *string `json:"keyfile_b64,omitempty"`
}
type CreateVaultResponse struct {
	DatabaseB64 string `json:"database_b64"`
}
type CreateEntryRequest struct {
	Handle         string  `json:"handle"`
	UserID         string  `json:"user_id"`
	VaultID        string  `json:"vault_id"`
	GroupID        *string `json:"group_id,omitempty"`
	Title          string  `json:"title"`
	Username       string  `json:"username"`
	Password       string  `json:"password"`
	URL            string  `json:"url"`
	Notes          string  `json:"notes"`
	MasterPassword *string `json:"master_password,omitempty"`
	KeyfileB64     *string `json:"keyfile_b64,omitempty"`
}
type CreateEntryResponse struct {
	EntryID     string `json:"entry_id"`
	DatabaseB64 string `json:"database_b64"`
}
type DeleteEntryRequest struct {
	HandleRequest
	EntryID        string  `json:"entry_id"`
	MasterPassword *string `json:"master_password,omitempty"`
	KeyfileB64     *string `json:"keyfile_b64,omitempty"`
}
type DeleteEntryResponse struct {
	EntryID     string `json:"entry_id"`
	GroupID     string `json:"group_id"`
	DatabaseB64 string `json:"database_b64"`
}
type RestoreEntryRequest struct {
	HandleRequest
	EntryID          string  `json:"entry_id"`
	PreferredGroupID *string `json:"preferred_group_id,omitempty"`
	MasterPassword   *string `json:"master_password,omitempty"`
	KeyfileB64       *string `json:"keyfile_b64,omitempty"`
}
type RestoreEntryResponse struct {
	EntryID     string `json:"entry_id"`
	DatabaseB64 string `json:"database_b64"`
}

func (c *Client) Unlock(ctx context.Context, r UnlockRequest) (UnlockResponse, error) {
	var out UnlockResponse
	return out, c.call(ctx, "/internal/v1/unlock", r, &out)
}
func (c *Client) Groups(ctx context.Context, r HandleRequest) (GroupsResponse, error) {
	var out GroupsResponse
	return out, c.call(ctx, "/internal/v1/groups", r, &out)
}
func (c *Client) GroupEntries(ctx context.Context, r HandleRequest, groupID string) (EntryGroupResponse, error) {
	var out EntryGroupResponse
	return out, c.call(ctx, "/internal/v1/entries", struct {
		HandleRequest
		GroupID string `json:"group_id"`
	}{r, groupID}, &out)
}
func (c *Client) Entry(ctx context.Context, r HandleRequest, entryID string) (EntryResponse, error) {
	var out EntryResponse
	return out, c.call(ctx, "/internal/v1/entry", struct {
		HandleRequest
		EntryID string `json:"entry_id"`
	}{r, entryID}, &out)
}
func (c *Client) Reveal(ctx context.Context, r HandleRequest, entryID, field string) (RevealResponse, error) {
	var out RevealResponse
	return out, c.call(ctx, "/internal/v1/reveal", struct {
		HandleRequest
		EntryID string `json:"entry_id"`
		Field   string `json:"field_name"`
	}{r, entryID, field}, &out)
}
func (c *Client) Update(ctx context.Context, r UpdateRequest) (UpdateResponse, error) {
	var out UpdateResponse
	return out, c.call(ctx, "/internal/v1/update", r, &out)
}
func (c *Client) Close(ctx context.Context, r HandleRequest) error {
	return c.call(ctx, "/internal/v1/close", r, nil)
}
func (c *Client) CreateVault(ctx context.Context, r CreateVaultRequest) (CreateVaultResponse, error) {
	var out CreateVaultResponse
	return out, c.call(ctx, "/internal/v1/create-vault", r, &out)
}
func (c *Client) CreateEntry(ctx context.Context, r CreateEntryRequest) (CreateEntryResponse, error) {
	var out CreateEntryResponse
	return out, c.call(ctx, "/internal/v1/entries/create", r, &out)
}
func (c *Client) DeleteEntry(ctx context.Context, r DeleteEntryRequest) (DeleteEntryResponse, error) {
	var out DeleteEntryResponse
	return out, c.call(ctx, "/internal/v1/entries/delete", r, &out)
}
func (c *Client) RestoreEntry(ctx context.Context, r RestoreEntryRequest) (RestoreEntryResponse, error) {
	var out RestoreEntryResponse
	return out, c.call(ctx, "/internal/v1/entries/restore", r, &out)
}

func (c *Client) call(ctx context.Context, endpoint string, payload any, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return ErrRejected
	}
	u := *c.base
	u.Path = strings.TrimRight(c.base.Path, "/") + endpoint
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return ErrUnavailable
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return ErrUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusBadRequest {
		return ErrRejected
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ErrUnavailable
	}
	if out != nil {
		if err := json.NewDecoder(io.LimitReader(resp.Body, 70<<20)).Decode(out); err != nil {
			return ErrUnavailable
		}
	}
	return nil
}
