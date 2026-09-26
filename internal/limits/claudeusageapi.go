/**
 * Live Claude subscription usage from Anthropic's OAuth usage endpoint.
 *
 * This is the request Claude Code itself makes for /usage, and its body is
 * the payload Claude Code caches in ~/.claude.json as cachedUsageUtilization.
 * Reading it directly is the only source of a current Fable window: the
 * statusLine rate_limits input carries only the 5h and weekly windows, and
 * the ~/.claude.json copy is as old as the user's last /usage.
 *
 * The token comes from the profile's own .credentials.json (Linux/Windows;
 * macOS keeps it in the Keychain, which this plugin does not read) or, when
 * Claude Code is not signed in, from a routing harness's Anthropic OAuth
 * login.
 */
package limits

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

const claudeUsageURL = "https://api.anthropic.com/api/oauth/usage"

// ProviderLimitsFromClaudeUsage maps an api/oauth/usage body to windows.
func ProviderLimitsFromClaudeUsage(body []byte, nowMs int64) (*ProviderLimits, error) {
	var parsed claudeUtilization
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, errors.New("unreadable usage response")
	}
	primary, secondary, fable := parsed.windows()
	if primary == nil && secondary == nil && fable == nil {
		return nil, errors.New("usage response has no windows")
	}
	return &ProviderLimits{
		ProviderID:  "claude",
		Label:       "Claude",
		Primary:     primary,
		Secondary:   secondary,
		Fable:       fable,
		Source:      "claude usage API",
		FetchedAtMs: nowMs,
	}, nil
}

// FetchClaudeUsage asks Anthropic for the token's account usage.
func FetchClaudeUsage(token SubscriptionToken, nowMs int64) (*ProviderLimits, error) {
	headers := map[string]string{
		"anthropic-beta": "oauth-2025-04-20",
		"User-Agent":     "usagebar",
	}
	body, err := getUsageJSON(usageAPIEndpoint("USAGEBAR_CLAUDE_USAGE_URL", claudeUsageURL), token.AccessToken, headers)
	if err != nil {
		return nil, err
	}
	return ProviderLimitsFromClaudeUsage(body, nowMs)
}

// resolveClaudeCredentialsPath returns the profile's .credentials.json.
// An empty configDir means the default ~/.claude.
func resolveClaudeCredentialsPath(configDir string) string {
	if configDir == "" {
		home, _ := os.UserHomeDir()
		configDir = filepath.Join(home, ".claude")
	}
	return filepath.Join(configDir, ".credentials.json")
}

// claudeCredentialsToken reads Claude Code's own OAuth access token. The
// refresh token is never read: Claude Code rotates it on use.
func claudeCredentialsToken(path string) *SubscriptionToken {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var parsed struct {
		ClaudeAiOauth *struct {
			AccessToken string `json:"accessToken"`
			ExpiresAt   int64  `json:"expiresAt"`
		} `json:"claudeAiOauth"`
	}
	if json.Unmarshal(raw, &parsed) != nil || parsed.ClaudeAiOauth == nil || parsed.ClaudeAiOauth.AccessToken == "" {
		return nil
	}
	return &SubscriptionToken{
		Harness:     "claude",
		AccessToken: parsed.ClaudeAiOauth.AccessToken,
		// AccountID keys the throttle cache, so two profiles never share it.
		AccountID:   path,
		ExpiresAtMs: parsed.ClaudeAiOauth.ExpiresAt,
	}
}

// collectClaudeWithToken reads live usage for a token, throttled per login.
// An expired token is reported, never refreshed.
func collectClaudeWithToken(token SubscriptionToken, nowMs int64) ProviderLimits {
	if token.Expired(nowMs) {
		return expiredTokenRow("claude", "Claude", token, nowMs)
	}
	key := "claude|" + token.Harness + "|" + token.AccountID
	got, err := cachedUsageFetch(key, nowMs, func() (*ProviderLimits, error) {
		return FetchClaudeUsage(token, nowMs)
	})
	if err != nil {
		return fetchFailedRow("claude", "Claude", token, err, nowMs)
	}
	got.Source = "claude usage API"
	if token.Harness != "claude" {
		got.Source += " via " + token.Harness
	}
	return *got
}
