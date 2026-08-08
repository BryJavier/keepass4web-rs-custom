package supabase

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
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
	requests  []*http.Request
	responses []int
}

func (f *recordingHTTP) Do(request *http.Request) (*http.Response, error) {
	f.requests = append(f.requests, request)
	status := http.StatusOK
	if len(f.responses) > 0 {
		status, f.responses = f.responses[0], f.responses[1:]
	}
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader("[]")), Header: make(http.Header)}, nil
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
}

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
