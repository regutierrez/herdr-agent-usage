/**
 * Rate-limit collection for Claude: the statusLine cache writer, and the
 * live OAuth collector for account usage (see CollectClaudeLimits).
 */
package limits

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
)

// RateLimitsInput is the statusLine rate_limits shape.
type RateLimitsInput struct {
	FiveHour *struct {
		UsedPercentage float64
		ResetsAt       int64
	}
	SevenDay *struct {
		UsedPercentage float64
		ResetsAt       int64
	}
	PromptCachePresent   bool
	PromptCacheExpiresAt *int64
}

// ClaudeLimitsCacheFile is the on-disk statusLine cache payload.
type ClaudeLimitsCacheFile struct {
	FiveHour             *LimitWindow `json:"fiveHour,omitempty"`
	SevenDay             *LimitWindow `json:"sevenDay,omitempty"`
	FetchedAtMs          int64        `json:"fetchedAtMs"`
	PromptCacheExpiresAt *int64       `json:"promptCacheExpiresAt,omitempty"`
}

// CollectClaudeLimitsOptions overrides paths for tests.
type CollectClaudeLimitsOptions struct {
	// CredentialsPath is the profile's .credentials.json. Empty means
	// ~/.claude/.credentials.json.
	CredentialsPath string
	// CollectorID is the collector id a routing harness's Anthropic login
	// must route to for this collector to use it. Empty means "claude".
	CollectorID string
}

// ResolveClaudeLimitsCachePath returns the single-default statusLine cache path.
// Multi-account isolation comes from explicit [[claude.profiles]] config, whose
// paths are computed per config_dir; the synthesized default must stay
// byte-identical to the historical location so it resolves the same on the write
// side (statusLine) and the read side (panel/sidebar), which cannot see
// CLAUDE_CONFIG_DIR.
func ResolveClaudeLimitsCachePath() string {
	if v := os.Getenv("USAGEBAR_CLAUDE_LIMITS_PATH"); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "herdr-usagebar", "claude-limits-latest.json")
}

// WriteClaudeLimitsCache writes statusLine RateLimitsInput to the cache file.
func WriteClaudeLimitsCache(rateLimits RateLimitsInput, nowMs int64, path string) error {
	if path == "" {
		path = ResolveClaudeLimitsCachePath()
	}
	existing := readClaudeLimitsCacheFile(path)
	payload := ClaudeLimitsCacheFile{}
	if existing != nil {
		payload = *existing
	}
	if rateLimits.FiveHour != nil || rateLimits.SevenDay != nil {
		payload.FetchedAtMs = nowMs
		payload.FiveHour = nil
		payload.SevenDay = nil
		if rateLimits.FiveHour != nil {
			wm := 300
			r := rateLimits.FiveHour.ResetsAt
			payload.FiveHour = &LimitWindow{
				UsedPercentage: rateLimits.FiveHour.UsedPercentage,
				ResetsAt:       &r,
				WindowMinutes:  &wm,
			}
		}
		if rateLimits.SevenDay != nil {
			wm := 10080
			r := rateLimits.SevenDay.ResetsAt
			payload.SevenDay = &LimitWindow{
				UsedPercentage: rateLimits.SevenDay.UsedPercentage,
				ResetsAt:       &r,
				WindowMinutes:  &wm,
			}
		}
	}
	if rateLimits.PromptCachePresent {
		payload.PromptCacheExpiresAt = rateLimits.PromptCacheExpiresAt
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// WriteClaudeLimitsCacheGuarded writes the cache when at least one window or a
// prompt_cache object is present, so an empty statusLine payload (e.g. `{}`)
// cannot overwrite a previously valid cache. Returns whether a write happened.
func WriteClaudeLimitsCacheGuarded(rateLimits RateLimitsInput, nowMs int64, path string) (bool, error) {
	if rateLimits.FiveHour == nil && rateLimits.SevenDay == nil && !rateLimits.PromptCachePresent {
		return false, nil
	}
	return true, WriteClaudeLimitsCache(rateLimits, nowMs, path)
}

func readClaudeLimitsCacheFile(path string) *ClaudeLimitsCacheFile {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var parsed ClaudeLimitsCacheFile
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil
	}
	return &parsed
}

// ReadPromptCacheExpiresAt returns the recorded Claude prompt_cache expiry.
func ReadPromptCacheExpiresAt(path string) *int64 {
	parsed := readClaudeLimitsCacheFile(path)
	if parsed == nil {
		return nil
	}
	return parsed.PromptCacheExpiresAt
}

func collectFromStatusLineCache(nowMs int64, path string) *ProviderLimits {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var parsed ClaudeLimitsCacheFile
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil
	}
	if parsed.FiveHour == nil && parsed.SevenDay == nil {
		return nil
	}
	fetched := parsed.FetchedAtMs
	if fetched == 0 {
		fetched = nowMs
	}
	ageMin := int(math.Max(0, math.Round(float64(nowMs-fetched)/60_000)))
	out := ProviderLimits{
		ProviderID:  "claude",
		Label:       "Claude",
		Primary:     parsed.FiveHour,
		Secondary:   parsed.SevenDay,
		Source:      "claude statusLine cache",
		FetchedAtMs: fetched,
	}
	if ageMin > 30 {
		note := "stale ~" + itoa(ageMin) + "m ago"
		out.Note = &note
	}
	return &out
}

// CollectClaudeLimits displays only a successful OAuth usage reading. A failed
// request or missing login must not turn an old statusLine/JSON observation
// into a plausible current quota. Successful responses are throttled by the
// shared usage API cache (at most one request per account every five minutes).
func CollectClaudeLimits(nowMs int64, options CollectClaudeLimitsOptions) ProviderLimits {
	credentialsPath := options.CredentialsPath
	if credentialsPath == "" {
		credentialsPath = resolveClaudeCredentialsPath("")
	}
	if token := claudeUsageToken(credentialsPath, options.CollectorID); token != nil {
		return collectClaudeWithToken(*token, nowMs)
	}
	note := "live Claude usage unavailable: no readable OAuth login (Claude Code or Pi)"
	return ProviderLimits{
		ProviderID:  "claude",
		Label:       "Claude",
		Source:      "none",
		Unavailable: true,
		FetchedAtMs: nowMs,
		Note:        &note,
	}
}

// claudeUsageToken prefers Claude Code's own login; a routing harness's
// Anthropic OAuth login is used only when Claude Code has none, since only
// the native login is known to be this profile's account.
func claudeUsageToken(credentialsPath, collectorID string) *SubscriptionToken {
	if token := claudeCredentialsToken(credentialsPath); token != nil {
		return token
	}
	if collectorID == "" {
		collectorID = "claude"
	}
	return routedSubscriptionToken(collectorID)
}
