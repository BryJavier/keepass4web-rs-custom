package privateclient

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

type recordingTransport struct {
	request  *http.Request
	body     string
	response string
}

func (t *recordingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	t.request = request
	body, err := io.ReadAll(request.Body)
	if err != nil {
		return nil, err
	}
	t.body = string(body)
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(t.response)),
	}, nil
}

func TestDeleteEntryPostsPrivateContract(t *testing.T) {
	transport := &recordingTransport{response: `{"entry_id":"entry-1","group_id":"group-1","database_b64":"database"}`}
	client := newContractTestClient(t, transport)
	masterPassword := "master"
	keyfileB64 := "keyfile"

	response, err := client.DeleteEntry(context.Background(), DeleteEntryRequest{
		HandleRequest:  HandleRequest{Handle: "handle", UserID: "user-1", VaultID: "vault-1"},
		EntryID:        "entry-1",
		MasterPassword: &masterPassword,
		KeyfileB64:     &keyfileB64,
	})
	if err != nil {
		t.Fatalf("DeleteEntry() error = %v", err)
	}
	if transport.request.Method != http.MethodPost || transport.request.URL.Path != "/internal/v1/entries/delete" {
		t.Fatalf("request = %s %s, want POST /internal/v1/entries/delete", transport.request.Method, transport.request.URL.Path)
	}
	assertJSONEqual(t, transport.body, map[string]any{
		"handle": "handle", "user_id": "user-1", "vault_id": "vault-1", "entry_id": "entry-1",
		"master_password": "master", "keyfile_b64": "keyfile",
	})
	if response != (DeleteEntryResponse{EntryID: "entry-1", GroupID: "group-1", DatabaseB64: "database"}) {
		t.Fatalf("DeleteEntry() response = %+v", response)
	}
}

func TestRestoreEntryPostsPrivateContract(t *testing.T) {
	transport := &recordingTransport{response: `{"entry_id":"entry-1","database_b64":"database"}`}
	client := newContractTestClient(t, transport)
	preferredGroupID := "group-1"

	response, err := client.RestoreEntry(context.Background(), RestoreEntryRequest{
		HandleRequest:    HandleRequest{Handle: "handle", UserID: "user-1", VaultID: "vault-1"},
		EntryID:          "entry-1",
		PreferredGroupID: &preferredGroupID,
	})
	if err != nil {
		t.Fatalf("RestoreEntry() error = %v", err)
	}
	if transport.request.Method != http.MethodPost || transport.request.URL.Path != "/internal/v1/entries/restore" {
		t.Fatalf("request = %s %s, want POST /internal/v1/entries/restore", transport.request.Method, transport.request.URL.Path)
	}
	assertJSONEqual(t, transport.body, map[string]any{
		"handle": "handle", "user_id": "user-1", "vault_id": "vault-1", "entry_id": "entry-1",
		"preferred_group_id": "group-1",
	})
	if response != (RestoreEntryResponse{EntryID: "entry-1", DatabaseB64: "database"}) {
		t.Fatalf("RestoreEntry() response = %+v", response)
	}
}

func newContractTestClient(t *testing.T, transport http.RoundTripper) *Client {
	t.Helper()
	base, err := url.Parse("http://private-service")
	if err != nil {
		t.Fatal(err)
	}
	return &Client{base: base, token: "test-token", httpClient: &http.Client{Transport: transport}}
}

func assertJSONEqual(t *testing.T, actual string, expected map[string]any) {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal([]byte(actual), &decoded); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	if !reflect.DeepEqual(decoded, expected) {
		t.Fatalf("request body = %#v, want %#v", decoded, expected)
	}
}
