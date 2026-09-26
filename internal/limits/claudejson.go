/**
 * Reads rate-limit windows from cachedUsageUtilization in ~/.claude.json.
 */
package limits

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/senna-lang/herdr-agent-usage/internal/planlabels"
)

type utilizationWindow struct {
	Utilization *float64 `json:"utilization"`
	ResetsAt    *string  `json:"resets_at"`
}

// scopedLimit is one entry in cachedUsageUtilization.utilization.limits.
// Claude Code records the model-scoped weekly allowance here. percent is
// already 0-100, unlike utilization which is the same scale on five_hour.
type scopedLimit struct {
	Kind     string   `json:"kind"`
	Percent  *float64 `json:"percent"`
	ResetsAt *string  `json:"resets_at"`
	Scope    *struct {
		Model *struct {
			DisplayName *string `json:"display_name"`
		} `json:"model"`
	} `json:"scope"`
}

// fableWindowFromLimits returns the weekly_scoped row whose model display
// name is Fable. Other scoped rows, including a null model id, are ignored.
func fableWindowFromLimits(limits []scopedLimit) *LimitWindow {
	for _, limit := range limits {
		if limit.Kind != "weekly_scoped" || limit.Scope == nil || limit.Scope.Model == nil || limit.Scope.Model.DisplayName == nil {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(*limit.Scope.Model.DisplayName), "Fable") {
			continue
		}
		return WindowFromUtilization(limit.Percent, limit.ResetsAt, 10080)
	}
	return nil
}

// claudeUtilization is Claude's usage payload. Claude Code caches it under
// cachedUsageUtilization.utilization in ~/.claude.json, and the OAuth usage
// endpoint returns the same shape live.
type claudeUtilization struct {
	FiveHour *utilizationWindow `json:"five_hour"`
	SevenDay *utilizationWindow `json:"seven_day"`
	Limits   []scopedLimit      `json:"limits"`
}

// windows maps the payload to the 5h, weekly and Fable slots. Any may be nil.
func (u claudeUtilization) windows() (primary, secondary, fable *LimitWindow) {
	if u.FiveHour != nil {
		primary = WindowFromUtilization(u.FiveHour.Utilization, u.FiveHour.ResetsAt, 300)
	}
	if u.SevenDay != nil {
		secondary = WindowFromUtilization(u.SevenDay.Utilization, u.SevenDay.ResetsAt, 10080)
	}
	return primary, secondary, fableWindowFromLimits(u.Limits)
}

// ResolveClaudeJSONPath returns CLAUDE_CONFIG_JSON or ~/.claude.json.
func ResolveClaudeJSONPath() string {
	if v := os.Getenv("CLAUDE_CONFIG_JSON"); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude.json")
}

func parseResetsAtEpochSeconds(iso string) *int64 {
	if iso == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339Nano, iso)
	if err != nil {
		t, err = time.Parse(time.RFC3339, iso)
	}
	if err != nil {
		return nil
	}
	sec := t.Unix()
	return &sec
}

// WindowFromUtilization maps utilization (used %) to LimitWindow.
func WindowFromUtilization(utilization *float64, resetsAtISO *string, windowMinutes int) *LimitWindow {
	if utilization == nil || math.IsNaN(*utilization) || math.IsInf(*utilization, 0) {
		return nil
	}
	w := &LimitWindow{UsedPercentage: *utilization, WindowMinutes: &windowMinutes}
	if resetsAtISO != nil {
		if sec := parseResetsAtEpochSeconds(*resetsAtISO); sec != nil {
			w.ResetsAt = sec
		}
	}
	return w
}

// ProviderLimitsFromClaudeJSON builds ProviderLimits from a ~/.claude.json body.
func ProviderLimitsFromClaudeJSON(rawJSON string, nowMs int64) *ProviderLimits {
	var parsed struct {
		CachedUsageUtilization *struct {
			FetchedAtMs *float64           `json:"fetchedAtMs"`
			Utilization *claudeUtilization `json:"utilization"`
		} `json:"cachedUsageUtilization"`
		OAuthAccount *struct {
			OrganizationType *string `json:"organizationType"`
			// The real key is singular; the plural stays as a legacy fallback.
			OrganizationRateLimitTier  *string `json:"organizationRateLimitTier"`
			OrganizationRateLimitTiers *string `json:"organizationRateLimitTiers"`
			UserRateLimitTier          *string `json:"userRateLimitTier"`
			SeatTier                   *string `json:"seatTier"`
		} `json:"oauthAccount"`
	}
	if err := json.Unmarshal([]byte(rawJSON), &parsed); err != nil {
		return nil
	}
	cache := parsed.CachedUsageUtilization
	if cache == nil || cache.Utilization == nil {
		return nil
	}
	primary, secondary, fable := cache.Utilization.windows()
	if primary == nil && secondary == nil && fable == nil {
		return nil
	}

	var org, tier *string
	if parsed.OAuthAccount != nil {
		if parsed.OAuthAccount.OrganizationType != nil {
			org = parsed.OAuthAccount.OrganizationType
		} else {
			org = parsed.OAuthAccount.SeatTier
		}
		// Most specific first: per-user tier, then org tier (singular is the
		// real key), then the legacy plural spelling.
		switch {
		case parsed.OAuthAccount.UserRateLimitTier != nil:
			tier = parsed.OAuthAccount.UserRateLimitTier
		case parsed.OAuthAccount.OrganizationRateLimitTier != nil:
			tier = parsed.OAuthAccount.OrganizationRateLimitTier
		default:
			tier = parsed.OAuthAccount.OrganizationRateLimitTiers
		}
	}
	plan := planlabels.ClaudePlanLabel(org, tier)

	fetchedAtMs := nowMs
	if cache.FetchedAtMs != nil && !math.IsNaN(*cache.FetchedAtMs) {
		fetchedAtMs = int64(*cache.FetchedAtMs)
	}
	ageMin := int(math.Max(0, math.Round(float64(nowMs-fetchedAtMs)/60_000)))
	out := ProviderLimits{
		ProviderID:  "claude",
		Label:       "Claude",
		Primary:     primary,
		Secondary:   secondary,
		Fable:       fable,
		PlanType:    plan,
		Source:      "claude.json cachedUsageUtilization",
		FetchedAtMs: fetchedAtMs,
	}
	if ageMin > 120 {
		note := "stale ~" + itoa(ageMin) + "m ago"
		if ageMin >= 24*60 {
			note += "; Claude has not refreshed this cache. Enable the optional statusLine hook (usagebar setup) to capture rate limits when Claude sends them."
		}
		out.Note = &note
	}
	return &out
}

// CollectClaudeLimitsFromJSON reads path and parses limits.
func CollectClaudeLimitsFromJSON(nowMs int64, path string) *ProviderLimits {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return ProviderLimitsFromClaudeJSON(string(raw), nowMs)
}

// AccountEmailFromJSON extracts oauthAccount.emailAddress from a
// ~/.claude.json body. Used for display only: distinguishing two configured
// Claude profiles that share no explicit label, by the account actually
// logged into each one.
func AccountEmailFromJSON(rawJSON string) (string, bool) {
	var parsed struct {
		OAuthAccount *struct {
			EmailAddress *string `json:"emailAddress"`
		} `json:"oauthAccount"`
	}
	if err := json.Unmarshal([]byte(rawJSON), &parsed); err != nil {
		return "", false
	}
	if parsed.OAuthAccount == nil || parsed.OAuthAccount.EmailAddress == nil || *parsed.OAuthAccount.EmailAddress == "" {
		return "", false
	}
	return *parsed.OAuthAccount.EmailAddress, true
}

// AccountEmailFromJSONPath reads path and extracts the account email.
func AccountEmailFromJSONPath(path string) (string, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return AccountEmailFromJSON(string(raw))
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [16]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
