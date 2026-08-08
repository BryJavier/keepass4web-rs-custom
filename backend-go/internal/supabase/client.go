// Package supabase provides small, dependency-free seams for Supabase-backed
// identity and data adapters. It intentionally has no service-role support in
// browser-facing code.
package supabase

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// AuthClient holds only the browser-safe project configuration. A real token
// validator can be plugged in behind this boundary without changing handlers.
type AuthClient struct {
	url     string
	anonKey string
	issuer  string
	http    HTTPDoer
}

func NewAuthClient(projectURL, anonKey, issuer string) (AuthClient, error) {
	return NewAuthClientWithHTTP(projectURL, anonKey, issuer, nil)
}

// NewAuthClientWithHTTP permits hermetic validation tests without real
// Supabase credentials or network access.
func NewAuthClientWithHTTP(projectURL, anonKey, issuer string, client HTTPDoer) (AuthClient, error) {
	for _, candidate := range []string{projectURL, issuer} {
		if !validPublicURL(candidate) {
			return AuthClient{}, fmt.Errorf("invalid Supabase public URL")
		}
	}
	if strings.TrimSpace(anonKey) == "" {
		return AuthClient{}, fmt.Errorf("missing Supabase public configuration")
	}
	if client == nil { client = &http.Client{Timeout: 10 * time.Second} }
	return AuthClient{url: projectURL, anonKey: anonKey, issuer: issuer, http: client}, nil
}

// validPublicURL keeps non-local Supabase traffic on TLS while allowing the
// official local Supabase stack, which is intentionally served over loopback
// HTTP (for example http://127.0.0.1:54321).
func validPublicURL(candidate string) bool {
	parsed, err := url.ParseRequestURI(strings.TrimSpace(candidate))
	if err != nil || parsed.Host == "" {
		return false
	}
	if parsed.Scheme == "https" {
		return true
	}
	if parsed.Scheme != "http" {
		return false
	}
	host := parsed.Hostname()
	return host == "localhost" || host == "host.docker.internal" || (net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback())
}

func (c AuthClient) URL() string    { return c.url }
func (c AuthClient) Issuer() string { return c.issuer }

// Identity is the minimal result the web server needs. Access tokens are
// verified by Supabase's /auth/v1/user endpoint rather than decoded locally:
// this avoids accepting unsigned or stale JWTs and works with asymmetric keys.
type Identity struct { ID string `json:"id"`; Email string `json:"email"` }
type TokenValidator interface { Validate(context.Context, string) (Identity, error) }

func (c AuthClient) Validate(ctx context.Context, token string) (Identity, error) {
	if strings.TrimSpace(token) == "" { return Identity{}, fmt.Errorf("missing access token") }
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.url, "/")+"/auth/v1/user", nil)
	if err != nil { return Identity{}, fmt.Errorf("build authentication request") }
	req.Header.Set("apikey", c.anonKey); req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.http.Do(req); if err != nil { return Identity{}, fmt.Errorf("validate access token") }; defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK { return Identity{}, fmt.Errorf("invalid access token") }
	var identity Identity; if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&identity); err != nil || strings.TrimSpace(identity.ID)=="" { return Identity{}, fmt.Errorf("invalid access token") }
	return identity,nil
}
