/**
 * Live Grok subscription usage from the Grok CLI's billing endpoint, read
 * with an xAI OAuth token for the same account.
 *
 * The endpoint reports one credit allowance for the current usage period
 * (weekly today) as a percentage plus the period bounds. The period length
 * is derived from those bounds rather than assumed, so a monthly period
 * still gets the right label.
 */
package limits

import (
	"encoding/json"
	"errors"
	"math"
	"time"
)

const grokCLIBillingURL = "https://cli-chat-proxy.grok.com/v1/billing?format=credits"

// ProviderLimitsFromGrokCLIBilling maps a v1/billing?format=credits body.
func ProviderLimitsFromGrokCLIBilling(body []byte, providerID, label string, nowMs int64) (*ProviderLimits, error) {
	var parsed struct {
		Config *struct {
			CreditUsagePercent *float64 `json:"creditUsagePercent"`
			CurrentPeriod      *struct {
				Start string `json:"start"`
				End   string `json:"end"`
			} `json:"currentPeriod"`
		} `json:"config"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, errors.New("unreadable billing response")
	}
	if parsed.Config == nil || parsed.Config.CreditUsagePercent == nil || !isFiniteF(*parsed.Config.CreditUsagePercent) {
		return nil, errors.New("billing response has no credit usage")
	}
	window := &LimitWindow{UsedPercentage: math.Min(100, math.Max(0, *parsed.Config.CreditUsagePercent))}
	if period := parsed.Config.CurrentPeriod; period != nil {
		window.ResetsAt = parseResetsAtEpochSeconds(period.End)
		window.WindowMinutes = periodMinutes(period.Start, period.End)
	}
	return &ProviderLimits{
		ProviderID:  providerID,
		Label:       label,
		Primary:     window,
		Source:      "grok billing API",
		FetchedAtMs: nowMs,
	}, nil
}

// periodMinutes is the whole-minute length between two RFC 3339 instants,
// or nil when either is missing or the range is empty.
func periodMinutes(startISO, endISO string) *int {
	start := parseResetsAtEpochSeconds(startISO)
	end := parseResetsAtEpochSeconds(endISO)
	if start == nil || end == nil || *end <= *start {
		return nil
	}
	minutes := int(time.Duration(*end-*start) * time.Second / time.Minute)
	return &minutes
}

// FetchGrokCLIBilling asks the Grok CLI billing endpoint for the token's
// current period usage.
func FetchGrokCLIBilling(token SubscriptionToken, providerID, label string, nowMs int64) (*ProviderLimits, error) {
	body, err := getUsageJSON(usageAPIEndpoint("USAGEBAR_GROK_USAGE_URL", grokCLIBillingURL), token.AccessToken, nil)
	if err != nil {
		return nil, err
	}
	return ProviderLimitsFromGrokCLIBilling(body, providerID, label, nowMs)
}

// collectGrokWithToken reads live usage for a token, throttled per harness.
// xAI logins record no account id, so the harness is the cache key. An
// expired token is reported, never refreshed.
func collectGrokWithToken(token SubscriptionToken, providerID, label string, nowMs int64) ProviderLimits {
	if token.Expired(nowMs) {
		return expiredTokenRow(providerID, label, token, nowMs)
	}
	key := "grok|" + token.Harness + "|" + token.AccountID
	got, err := cachedUsageFetch(key, nowMs, func() (*ProviderLimits, error) {
		return FetchGrokCLIBilling(token, providerID, label, nowMs)
	})
	if err != nil {
		return fetchFailedRow(providerID, label, token, err, nowMs)
	}
	got.ProviderID = providerID
	got.Label = label
	got.Source = "grok billing API via " + token.Harness
	return *got
}
