package supabase

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestNewAuthClientRetainsOnlyPublicSupabaseConfiguration(t *testing.T) {
	client, err := NewAuthClient("https://project.supabase.co", "public-anon-key", "https://project.supabase.co/auth/v1")
	if err != nil {
		t.Fatalf("NewAuthClient() error = %v", err)
	}
	if client.URL() != "https://project.supabase.co" || client.Issuer() != "https://project.supabase.co/auth/v1" {
		t.Fatalf("client did not retain supplied public configuration")
	}
}

func TestNewAuthClientRejectsInvalidURLs(t *testing.T) {
	if _, err := NewAuthClient("not a URL", "public-anon-key", "https://project.supabase.co/auth/v1"); err == nil {
		t.Fatal("NewAuthClient accepted invalid service URL")
	}
}

func TestNewAuthClientAcceptsLoopbackHTTPForLocalSupabase(t *testing.T) {
	client, err := NewAuthClient("http://127.0.0.1:54321", "public-anon-key", "http://127.0.0.1:54321/auth/v1")
	if err != nil {
		t.Fatalf("NewAuthClient() error = %v, want local HTTP configuration accepted", err)
	}
	if client.URL() != "http://127.0.0.1:54321" {
		t.Fatalf("client URL = %q", client.URL())
	}
}

func TestNewAuthClientAcceptsDockerDesktopHTTPForLocalSupabase(t *testing.T) {
	client, err := NewAuthClient("http://host.docker.internal:54321", "public-anon-key", "http://127.0.0.1:54321/auth/v1")
	if err != nil {
		t.Fatalf("NewAuthClient() error = %v, want Docker Desktop local configuration accepted", err)
	}
	if client.URL() != "http://host.docker.internal:54321" {
		t.Fatalf("client URL = %q", client.URL())
	}
}

type recordingHTTP struct {
	requests       []*http.Request
	bodies         []string
	responses      []int
	responseBodies []string
}

func (f *recordingHTTP) Do(request *http.Request) (*http.Response, error) {
	f.requests = append(f.requests, request)
	if request.Body != nil {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			return nil, err
		}
		f.bodies = append(f.bodies, string(body))
	} else {
		f.bodies = append(f.bodies, "")
	}
	status := http.StatusOK
	if len(f.responses) > 0 {
		status, f.responses = f.responses[0], f.responses[1:]
	}
	body := "[]"
	if len(f.responseBodies) > 0 {
		body, f.responseBodies = f.responseBodies[0], f.responseBodies[1:]
	}
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
}

func TestListOnlyRequestsUploadedVaults(t *testing.T) {
	httpClient := &recordingHTTP{}
	client, err := NewVaultClient("https://project.supabase.co", "anon", httpClient)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.List(context.Background(), "access", "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	if got := httpClient.requests[0].URL.Query().Get("upload_state"); got != "eq.uploaded" {
		t.Fatalf("upload state filter = %q, want eq.uploaded", got)
	}
	if got := httpClient.requests[0].URL.Query().Get("trashed_at"); got != "is.null" {
		t.Fatalf("trashed_at filter = %q, want is.null", got)
	}
}

func TestTrashVaultSetsRetentionOnlyForActiveVault(t *testing.T) {
	httpClient := &recordingHTTP{}
	client, err := NewVaultClient("https://project.supabase.co", "anon", httpClient)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.TrashVault(context.Background(), "access", ownerID, vaultID); err != nil {
		t.Fatal(err)
	}
	request := httpClient.requests[0]
	if got := request.URL.Query().Get("trashed_at"); got != "is.null" {
		t.Fatalf("trashed_at filter = %q, want is.null", got)
	}
	if got := request.URL.Query().Get("owner_id"); got != "eq."+ownerID {
		t.Fatalf("owner filter = %q", got)
	}
	var lifecycle struct {
		TrashedAt  time.Time `json:"trashed_at"`
		PurgeAfter time.Time `json:"purge_after"`
	}
	if err := json.Unmarshal([]byte(httpClient.bodies[0]), &lifecycle); err != nil {
		t.Fatal(err)
	}
	if lifecycle.TrashedAt.IsZero() || !lifecycle.PurgeAfter.Equal(lifecycle.TrashedAt.AddDate(0, 0, 30)) {
		t.Fatalf("trash lifecycle = %+v, want exactly 30 days", lifecycle)
	}
}

func TestRestoreVaultClearsLifecycleFields(t *testing.T) {
	httpClient := &recordingHTTP{}
	client, _ := NewVaultClient("https://project.supabase.co", "anon", httpClient)
	if err := client.RestoreVault(context.Background(), "access", ownerID, vaultID); err != nil {
		t.Fatal(err)
	}
	request := httpClient.requests[0]
	if got := request.URL.Query().Get("trashed_at"); got != "not.is.null" {
		t.Fatalf("trashed_at filter = %q, want not.is.null", got)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(httpClient.bodies[0]), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["trashed_at"] != nil || payload["purge_after"] != nil {
		t.Fatalf("restore payload = %s, want both lifecycle fields null", httpClient.bodies[0])
	}
}

func TestListTrashedVaultsRequestsOnlyUnexpiredTrash(t *testing.T) {
	httpClient := &recordingHTTP{}
	client, _ := NewVaultClient("https://project.supabase.co", "anon", httpClient)
	if _, err := client.ListTrashedVaults(context.Background(), "access", ownerID); err != nil {
		t.Fatal(err)
	}
	query := httpClient.requests[0].URL.Query()
	if got := query.Get("trashed_at"); got != "not.is.null" {
		t.Fatalf("trashed_at filter = %q", got)
	}
	if got := query.Get("purge_after"); !strings.HasPrefix(got, "gt.") {
		t.Fatalf("purge_after filter = %q, want future-only predicate", got)
	}
}

func TestActiveDecodedEntriesExcludeTombstones(t *testing.T) {
	httpClient := &recordingHTTP{}
	client, _ := NewVaultClient("https://project.supabase.co", "anon", httpClient)
	if _, err := client.DecodedEntries(context.Background(), "access", ownerID, vaultID); err != nil {
		t.Fatal(err)
	}
	if got := httpClient.requests[0].URL.Query().Get("trashed_at"); got != "is.null" {
		t.Fatalf("decoded-entry active filter = %q, want is.null", got)
	}
}

func TestTrashAndRestoreEntryUseLifecyclePredicates(t *testing.T) {
	httpClient := &recordingHTTP{}
	client, _ := NewVaultClient("https://project.supabase.co", "anon", httpClient)
	if err := client.TrashEntry(context.Background(), "access", ownerID, vaultID, entryID); err != nil {
		t.Fatal(err)
	}
	if err := client.RestoreEntry(context.Background(), "access", ownerID, vaultID, entryID); err != nil {
		t.Fatal(err)
	}
	if got := httpClient.requests[0].URL.Query().Get("trashed_at"); got != "is.null" {
		t.Fatalf("entry trash filter = %q, want is.null", got)
	}
	if got := httpClient.requests[1].URL.Query().Get("trashed_at"); got != "not.is.null" {
		t.Fatalf("entry restore filter = %q, want not.is.null", got)
	}
}

func TestListTrashedEntriesRequestsOnlyUnexpiredTrash(t *testing.T) {
	httpClient := &recordingHTTP{}
	client, _ := NewVaultClient("https://project.supabase.co", "anon", httpClient)
	if _, err := client.ListTrashedEntries(context.Background(), "access", ownerID, vaultID); err != nil {
		t.Fatal(err)
	}
	query := httpClient.requests[0].URL.Query()
	if got := query.Get("trashed_at"); got != "not.is.null" {
		t.Fatalf("trashed_at filter = %q", got)
	}
	if got := query.Get("purge_after"); !strings.HasPrefix(got, "gt.") {
		t.Fatalf("purge_after filter = %q, want future-only predicate", got)
	}
}

func TestSyncActiveDecodedEntriesDoesNotDeleteTombstones(t *testing.T) {
	httpClient := &recordingHTTP{}
	client, _ := NewVaultClient("https://project.supabase.co", "anon", httpClient)
	entries := []DecodedEntry{{EntryID: entryID, GroupID: groupID, Title: "Active"}}
	if err := client.SyncActiveDecodedEntries(context.Background(), "access", ownerID, vaultID, entries); err != nil {
		t.Fatal(err)
	}
	if len(httpClient.requests) != 2 {
		t.Fatalf("requests = %d, want upsert and active-only delete", len(httpClient.requests))
	}
	if got := httpClient.requests[0].Header.Get("Prefer"); !strings.Contains(got, "resolution=merge-duplicates") {
		t.Fatalf("upsert Prefer = %q", got)
	}
	if got := httpClient.requests[1].URL.Query().Get("trashed_at"); got != "is.null" {
		t.Fatalf("sync delete trashed_at filter = %q, want is.null", got)
	}
	if got := httpClient.requests[1].URL.Query().Get("entry_id"); got != "not.in.("+entryID+")" {
		t.Fatalf("sync delete entry filter = %q", got)
	}
}

func TestDownloadActiveReturnsEncryptedStorageBytes(t *testing.T) {
	httpClient := &recordingHTTP{responseBodies: []string{"[{\"id\":\"" + vaultID + "\",\"name\":\"Personal\",\"object_path\":\"" + ownerID + "/" + vaultID + ".kdbx\",\"upload_state\":\"uploaded\"}]", "encrypted-kdbx"}}
	client, _ := NewVaultClient("https://project.supabase.co", "anon", httpClient)
	data, err := client.DownloadActive(context.Background(), "access", ownerID, vaultID)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != "encrypted-kdbx" {
		t.Fatalf("downloaded bytes = %q", got)
	}
	if got := httpClient.requests[0].URL.Query().Get("trashed_at"); got != "is.null" {
		t.Fatalf("active ownership filter = %q", got)
	}
	if got := httpClient.requests[1].URL.Path; got != "/storage/v1/object/vaults/"+ownerID+"/"+vaultID+".kdbx" {
		t.Fatalf("storage request path = %q", got)
	}
}

func TestPurgeExpiredTrashUsesStorageAPIThenGuardedMetadata(t *testing.T) {
	httpClient := &recordingHTTP{responseBodies: []string{"[{\"id\":\"" + vaultID + "\",\"object_path\":\"" + ownerID + "/" + vaultID + ".kdbx\"}]", "", "", ""}}
	client, _ := NewVaultClient("https://project.supabase.co", "anon", httpClient)
	if err := client.PurgeExpiredTrash(context.Background(), "service-role"); err != nil {
		t.Fatal(err)
	}
	if len(httpClient.requests) != 4 {
		t.Fatalf("requests = %d, want expired vault list, object delete, metadata delete, entry delete", len(httpClient.requests))
	}
	if got := httpClient.requests[0].URL.Query().Get("purge_after"); !strings.HasPrefix(got, "lte.") {
		t.Fatalf("expired vault predicate = %q", got)
	}
	if got := httpClient.requests[0].Header.Get("Authorization"); got != "Bearer service-role" {
		t.Fatalf("purge authorization = %q", got)
	}
	if got := httpClient.requests[1].Method + " " + httpClient.requests[1].URL.Path; got != "DELETE /storage/v1/object/vaults/"+ownerID+"/"+vaultID+".kdbx" {
		t.Fatalf("object purge request = %q", got)
	}
	metadataQuery := httpClient.requests[2].URL.Query()
	if got := metadataQuery.Get("purge_after"); !strings.HasPrefix(got, "lte.") || metadataQuery.Get("trashed_at") != "not.is.null" {
		t.Fatalf("metadata purge lifecycle query = %q", httpClient.requests[2].URL.RawQuery)
	}
	if got := httpClient.requests[3].URL.Path; got != "/rest/v1/decoded_vault_entries" {
		t.Fatalf("entry purge endpoint = %q", got)
	}
}

func TestPurgeExpiredTrashContinuesWhenObjectWasAlreadyRemoved(t *testing.T) {
	httpClient := &recordingHTTP{
		responses:      []int{http.StatusOK, http.StatusNotFound, http.StatusNoContent, http.StatusNoContent},
		responseBodies: []string{"[{\"id\":\"" + vaultID + "\",\"object_path\":\"" + ownerID + "/" + vaultID + ".kdbx\"}]"},
	}
	client, _ := NewVaultClient("https://project.supabase.co", "anon", httpClient)
	if err := client.PurgeExpiredTrash(context.Background(), "service-role"); err != nil {
		t.Fatalf("PurgeExpiredTrash() error = %v, want missing Storage object tolerated", err)
	}
	if got := httpClient.requests[2].URL.Path; got != "/rest/v1/vaults" {
		t.Fatalf("metadata delete after missing object = %q", got)
	}
}

const (
	ownerID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	vaultID = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	entryID = "cccccccc-cccc-cccc-cccc-cccccccccccc"
	groupID = "dddddddd-dddd-dddd-dddd-dddddddddddd"
)

func TestFailedStorageUploadCompensatesPendingMetadata(t *testing.T) {
	httpClient := &recordingHTTP{responses: []int{http.StatusCreated, http.StatusInternalServerError, http.StatusNoContent, http.StatusNoContent}}
	client, err := NewVaultClient("https://project.supabase.co", "anon", httpClient)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.CreateAndUpload(context.Background(), "access", "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", "Personal", "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb", []byte("vault"))
	if err == nil {
		t.Fatal("CreateAndUpload() error = nil, want storage error")
	}
	if len(httpClient.requests) != 4 {
		t.Fatalf("requests = %d, want metadata create, storage upload, storage delete, metadata delete", len(httpClient.requests))
	}
	if got := httpClient.requests[2].Method + " " + httpClient.requests[2].URL.Path; got != "DELETE /storage/v1/object/vaults/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa/bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb.kdbx" {
		t.Fatalf("compensation object request = %q", got)
	}
	if got := httpClient.requests[3].Method; got != http.MethodDelete {
		t.Fatalf("metadata compensation method = %q, want DELETE", got)
	}
}
