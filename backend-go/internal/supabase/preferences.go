package supabase

import (
	"context"
	"net/http"
	"net/url"
)

// ThemePreference looks up the signed-in user's stored dark-mode choice. An
// empty result means unset (client falls back to prefers-color-scheme).
func (c *VaultClient) ThemePreference(ctx context.Context, accessToken, userID string) (string, error) {
	q := url.Values{"user_id": {"eq." + userID}, "select": {"theme_preference"}, "limit": {"1"}}
	var rows []struct {
		ThemePreference *string `json:"theme_preference"`
	}
	if err := c.request(ctx, http.MethodGet, "/rest/v1/user_preferences?"+q.Encode(), accessToken, nil, &rows); err != nil {
		return "", err
	}
	if len(rows) != 1 || rows[0].ThemePreference == nil {
		return "", nil
	}
	return *rows[0].ThemePreference, nil
}

// SetThemePreference upserts the signed-in user's dark-mode choice.
func (c *VaultClient) SetThemePreference(ctx context.Context, accessToken, userID, theme string) error {
	payload := map[string]string{"user_id": userID, "theme_preference": theme}
	return c.requestWithHeaders(ctx, http.MethodPost, "/rest/v1/user_preferences", accessToken, payload, nil, http.Header{
		"Prefer": {"resolution=merge-duplicates,return=minimal"},
	})
}
