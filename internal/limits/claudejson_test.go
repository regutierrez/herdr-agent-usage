/**
 * Tests for parsing Claude's cachedUsageUtilization.
 */
package limits

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWindowFromUtilization(t *testing.T) {
	u := 63.0
	iso := "2026-07-19T20:00:00.000Z"
	got := WindowFromUtilization(&u, &iso, 10080)
	if got == nil || got.UsedPercentage != 63 || got.WindowMinutes == nil || *got.WindowMinutes != 10080 {
		t.Fatalf("got %+v", got)
	}
	wantSec := time.Date(2026, 7, 19, 20, 0, 0, 0, time.UTC).Unix()
	if got.ResetsAt == nil || *got.ResetsAt != wantSec {
		t.Fatalf("resetsAt=%v want %d", got.ResetsAt, wantSec)
	}
	if WindowFromUtilization(nil, nil, 300) != nil {
		t.Fatal("expected nil")
	}
}

func TestProviderLimitsFromClaudeJSON(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{
		"oauthAccount": map[string]any{
			"billingType": "stripe_subscription", "organizationType": "claude_pro",
		},
		"cachedUsageUtilization": map[string]any{
			"fetchedAtMs": 1_700_000_000_000,
			"utilization": map[string]any{
				"five_hour": map[string]any{"utilization": 10, "resets_at": "2026-07-15T16:00:00.000Z"},
				"seven_day": map[string]any{"utilization": 50, "resets_at": "2026-07-20T00:00:00.000Z"},
			},
		},
	})
	result := ProviderLimitsFromClaudeJSON(string(raw), 1_700_000_000_000)
	if result == nil || result.Primary == nil || result.Primary.UsedPercentage != 10 {
		t.Fatalf("primary %+v", result)
	}
	if result.Secondary == nil || result.Secondary.UsedPercentage != 50 {
		t.Fatalf("secondary %+v", result)
	}
	if result.PlanType == nil || *result.PlanType != "Pro" {
		t.Fatalf("plan %+v", result.PlanType)
	}
	if result.Source == "" || !containsStr(result.Source, "cachedUsageUtilization") {
		t.Fatalf("source %q", result.Source)
	}
	if ProviderLimitsFromClaudeJSON("{}", 0) != nil {
		t.Fatal("expected nil")
	}
}

func TestProviderLimitsFromClaudeJSON_FableWeeklyScoped(t *testing.T) {
	// Observed ~/.claude.json shape: limits[] weekly_scoped names the model
	// in scope.model.display_name and reports percent already on 0-100.
	// scope.model.id is null. A Sonnet-scoped row must not be labeled Fable.
	raw, _ := json.Marshal(map[string]any{
		"cachedUsageUtilization": map[string]any{
			"fetchedAtMs": 1_700_000_000_000,
			"utilization": map[string]any{
				"five_hour": map[string]any{"utilization": 10, "resets_at": "2026-07-15T16:00:00.000Z"},
				"seven_day": map[string]any{"utilization": 50, "resets_at": "2026-07-20T00:00:00.000Z"},
				"limits": []any{
					map[string]any{
						"kind": "weekly_scoped", "percent": 6,
						"resets_at": "2026-09-24T12:00:00.199350+00:00",
						"scope":     map[string]any{"model": map[string]any{"id": nil, "display_name": "Fable"}},
					},
					map[string]any{
						"kind": "weekly_scoped", "percent": 90,
						"resets_at": "2026-09-24T12:00:00.199350+00:00",
						"scope":     map[string]any{"model": map[string]any{"display_name": "Sonnet"}},
					},
				},
			},
		},
	})
	result := ProviderLimitsFromClaudeJSON(string(raw), 1_700_000_000_000)
	if result == nil || result.Fable == nil {
		t.Fatalf("fable window missing: %+v", result)
	}
	if result.Fable.UsedPercentage != 6 {
		t.Fatalf("fable used = %v, want 6", result.Fable.UsedPercentage)
	}
	if result.Fable.WindowMinutes == nil || *result.Fable.WindowMinutes != 10080 {
		t.Fatalf("fable window minutes = %v, want 10080", result.Fable.WindowMinutes)
	}
	wantReset := time.Date(2026, 9, 24, 12, 0, 0, 199350000, time.UTC).Unix()
	if result.Fable.ResetsAt == nil || *result.Fable.ResetsAt != wantReset {
		t.Fatalf("fable resetsAt = %v, want %d", result.Fable.ResetsAt, wantReset)
	}
	if result.Primary == nil || result.Primary.UsedPercentage != 10 || result.Secondary == nil || result.Secondary.UsedPercentage != 50 {
		t.Fatalf("shared windows changed: %+v", result)
	}
	text := FormatProviderBlock(*result, wide, 1_700_000_000_000)
	if !strings.Contains(text, "Fable") || strings.Contains(text, "Sonnet") {
		t.Fatalf("panel = %q, want a Fable row and no Sonnet row", text)
	}
	if !strings.Contains(text, "94% left") {
		t.Fatalf("panel = %q, want 94%% left", text)
	}
}

func TestProviderLimitsFromClaudeJSON_TeamOrg(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{
		"oauthAccount": map[string]any{
			"organizationType": "claude_team",
		},
		"cachedUsageUtilization": map[string]any{
			"fetchedAtMs": 1_700_000_000_000,
			"utilization": map[string]any{
				"five_hour": map[string]any{"utilization": 10, "resets_at": "2026-07-15T16:00:00.000Z"},
			},
		},
	})
	result := ProviderLimitsFromClaudeJSON(string(raw), 1_700_000_000_000)
	if result == nil || result.PlanType == nil || *result.PlanType != "Team" {
		t.Fatalf("plan %+v", result)
	}
}

func TestProviderLimitsFromClaudeJSON_RateLimitTierSingularKey(t *testing.T) {
	// The real ~/.claude.json key is organizationRateLimitTier (singular).
	// organizationType is absent here so the label must come from the tier.
	raw, _ := json.Marshal(map[string]any{
		"oauthAccount": map[string]any{
			"organizationRateLimitTier": "team_tier",
		},
		"cachedUsageUtilization": map[string]any{
			"fetchedAtMs": 1_700_000_000_000,
			"utilization": map[string]any{
				"five_hour": map[string]any{"utilization": 10, "resets_at": "2026-07-15T16:00:00.000Z"},
			},
		},
	})
	result := ProviderLimitsFromClaudeJSON(string(raw), 1_700_000_000_000)
	if result == nil || result.PlanType == nil || *result.PlanType != "Team" {
		t.Fatalf("plan %+v", result)
	}
}

func TestProviderLimitsFromClaudeJSON_UserRateLimitTierFallback(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{
		"oauthAccount": map[string]any{
			"userRateLimitTier": "max_5x",
		},
		"cachedUsageUtilization": map[string]any{
			"fetchedAtMs": 1_700_000_000_000,
			"utilization": map[string]any{
				"five_hour": map[string]any{"utilization": 10, "resets_at": "2026-07-15T16:00:00.000Z"},
			},
		},
	})
	result := ProviderLimitsFromClaudeJSON(string(raw), 1_700_000_000_000)
	if result == nil || result.PlanType == nil || *result.PlanType != "Max 5x" {
		t.Fatalf("plan %+v", result)
	}
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		func() bool {
			for i := 0; i+len(sub) <= len(s); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		}())
}

func TestAccountEmailFromJSON_Found(t *testing.T) {
	email, ok := AccountEmailFromJSON(`{
		"oauthAccount": {"emailAddress": "user@example.com", "organizationType": "claude_pro"}
	}`)
	if !ok || email != "user@example.com" {
		t.Fatalf("ok=%v email=%q", ok, email)
	}
}

func TestAccountEmailFromJSON_MissingOAuthAccount(t *testing.T) {
	_, ok := AccountEmailFromJSON(`{"cachedUsageUtilization": {}}`)
	if ok {
		t.Fatal("expected no email without oauthAccount")
	}
}

func TestAccountEmailFromJSON_EmptyEmail(t *testing.T) {
	_, ok := AccountEmailFromJSON(`{"oauthAccount": {"emailAddress": ""}}`)
	if ok {
		t.Fatal("expected no email for empty string")
	}
}

func TestAccountEmailFromJSON_InvalidJSON(t *testing.T) {
	_, ok := AccountEmailFromJSON(`not json`)
	if ok {
		t.Fatal("expected no email for invalid JSON")
	}
}

func TestAccountEmailFromJSONPath_MissingFile(t *testing.T) {
	_, ok := AccountEmailFromJSONPath(filepath.Join(t.TempDir(), "missing.json"))
	if ok {
		t.Fatal("expected no email for missing file")
	}
}
