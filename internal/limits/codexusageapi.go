/**
 * Live Codex subscription usage from ChatGPT's usage endpoint, read with an
 * OAuth token for the same ChatGPT account.
 *
 * The response carries the same primary/secondary windows a rollout's
 * token_count records, so the result lands in the same ProviderLimits slots
 * as the rollout path.
 */
package limits

import (
	"encoding/json"
	"errors"

	"github.com/senna-lang/herdr-agent-usage/internal/planlabels"
)

const codexUsageURL = "https://chatgpt.com/backend-api/wham/usage"

type codexUsageWindow struct {
	UsedPercent        *float64 `json:"used_percent"`
	LimitWindowSeconds *float64 `json:"limit_window_seconds"`
	ResetAt            *float64 `json:"reset_at"`
}

// ProviderLimitsFromCodexUsage maps a wham/usage body to windows. A window
// whose used_percent is null (the account has no such window) stays nil.
func ProviderLimitsFromCodexUsage(body []byte, providerID, label string, nowMs int64) (*ProviderLimits, error) {
	var parsed struct {
		PlanType  *string `json:"plan_type"`
		RateLimit *struct {
			PrimaryWindow   *codexUsageWindow `json:"primary_window"`
			SecondaryWindow *codexUsageWindow `json:"secondary_window"`
		} `json:"rate_limit"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, errors.New("unreadable usage response")
	}
	if parsed.RateLimit == nil {
		return nil, errors.New("usage response has no rate_limit")
	}
	primary := codexUsageToWindow(parsed.RateLimit.PrimaryWindow)
	secondary := codexUsageToWindow(parsed.RateLimit.SecondaryWindow)
	if primary == nil && secondary == nil {
		return nil, errors.New("usage response has no windows")
	}
	return &ProviderLimits{
		ProviderID:  providerID,
		Label:       label,
		Primary:     primary,
		Secondary:   secondary,
		PlanType:    planlabels.CodexPlanLabel(parsed.PlanType),
		Source:      "chatgpt usage API",
		FetchedAtMs: nowMs,
	}, nil
}

func codexUsageToWindow(raw *codexUsageWindow) *LimitWindow {
	if raw == nil || raw.UsedPercent == nil || !isFiniteF(*raw.UsedPercent) {
		return nil
	}
	w := &LimitWindow{UsedPercentage: *raw.UsedPercent}
	if raw.ResetAt != nil && isFiniteF(*raw.ResetAt) && *raw.ResetAt > 0 {
		r := int64(*raw.ResetAt)
		w.ResetsAt = &r
	}
	if raw.LimitWindowSeconds != nil && isFiniteF(*raw.LimitWindowSeconds) && *raw.LimitWindowSeconds > 0 {
		m := int(*raw.LimitWindowSeconds / 60)
		w.WindowMinutes = &m
	}
	return w
}

// FetchCodexUsage asks ChatGPT for the token's account usage. The headers
// match what the Codex desktop client sends; the endpoint rejects requests
// without the account id header for multi-workspace logins.
func FetchCodexUsage(token SubscriptionToken, providerID, label string, nowMs int64) (*ProviderLimits, error) {
	headers := map[string]string{
		"OpenAI-Beta": "codex-1",
		"Originator":  "Codex Desktop",
	}
	if token.AccountID != "" {
		headers["Chatgpt-Account-Id"] = token.AccountID
	}
	body, err := getUsageJSON(usageAPIEndpoint("USAGEBAR_CODEX_USAGE_URL", codexUsageURL), token.AccessToken, headers)
	if err != nil {
		return nil, err
	}
	return ProviderLimitsFromCodexUsage(body, providerID, label, nowMs)
}

// collectCodexWithToken reads live usage for a token, throttled per account.
// An expired token is reported, never refreshed.
func collectCodexWithToken(token SubscriptionToken, providerID, label string, nowMs int64) ProviderLimits {
	if token.Expired(nowMs) {
		return expiredTokenRow(providerID, label, token, nowMs)
	}
	key := "codex|" + token.Harness + "|" + token.AccountID
	got, err := cachedUsageFetch(key, nowMs, func() (*ProviderLimits, error) {
		return FetchCodexUsage(token, providerID, label, nowMs)
	})
	if err != nil {
		return fetchFailedRow(providerID, label, token, err, nowMs)
	}
	got.ProviderID = providerID
	got.Label = label
	got.Source = "chatgpt usage API via " + token.Harness
	return *got
}
