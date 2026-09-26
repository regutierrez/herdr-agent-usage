/**
 * Bearer credentials a quota collector may use to ask its vendor for live
 * subscription usage.
 *
 * A token comes either from the collector's own CLI login (read inside that
 * provider's adapter) or from a routing harness such as Pi that saved an
 * OAuth login for the same subscription. This file owns only the routing
 * half and the shared rules every adapter applies to any token:
 *
 *   - A token is sent only to the vendor that issued it.
 *   - A token is never refreshed. Refresh tokens rotate on use, so
 *     refreshing would sign the owning harness out. An expired token is
 *     reported as expired and skipped.
 *   - A token is never persisted; only the resulting ProviderLimits is cached.
 */
package limits

import (
	"github.com/senna-lang/herdr-agent-usage/internal/providers/omp"
)

// SubscriptionToken is one OAuth bearer credential for a subscription login.
type SubscriptionToken struct {
	// Harness is the agent that saved the login ("pi"), or the collector's
	// own id for a native CLI login.
	Harness     string
	AccessToken string
	// AccountID is the vendor account id when the login records one.
	AccountID string
	// ExpiresAtMs is the access token expiry in epoch ms. 0 means unknown.
	ExpiresAtMs int64
}

// Expired reports whether the token is known to be past its expiry. A token
// with an unknown expiry is tried; the vendor will reject it if needed.
func (t SubscriptionToken) Expired(nowMs int64) bool {
	return t.ExpiresAtMs > 0 && t.ExpiresAtMs <= nowMs
}

// routedSubscriptionToken returns the first routing-harness login whose
// provider routes to collectorID (see SubscriptionRouteForProviderAuth), or
// nil. Adapters call it with their own collector id, the same way they call
// borrowWindows, so shared code never names a provider.
//
// Only Pi stores tokens in a readable format today; OMP keeps secrets in
// agent.db, which this plugin deliberately never reads.
func routedSubscriptionToken(collectorID string) *SubscriptionToken {
	for _, cred := range omp.ListPiCredentials(piAgentDir()) {
		if cred.AccessToken == "" {
			continue
		}
		route, ok := SubscriptionRouteForProviderAuth(cred.Provider, cred.Type)
		if !ok || route.CollectorProviderID != collectorID {
			continue
		}
		return &SubscriptionToken{
			Harness:     routingHarnessPi,
			AccessToken: cred.AccessToken,
			AccountID:   cred.AccountID,
			ExpiresAtMs: cred.ExpiresAtMs,
		}
	}
	return nil
}

// expiredTokenRow is the row an adapter returns when its only token has
// expired. It stays visible so the login is not silently dropped.
func expiredTokenRow(providerID, label string, token SubscriptionToken, nowMs int64) ProviderLimits {
	note := token.Harness + " login expired — open " + token.Harness + " to refresh it"
	return ProviderLimits{
		ProviderID:  providerID,
		Label:       label,
		Source:      token.Harness + " login",
		FetchedAtMs: nowMs,
		Note:        &note,
	}
}

// fetchFailedRow is the row an adapter returns when the vendor refused or
// could not answer a usage request made with a valid token.
func fetchFailedRow(providerID, label string, token SubscriptionToken, err error, nowMs int64) ProviderLimits {
	note := "usage request via " + token.Harness + " login failed: " + err.Error()
	return ProviderLimits{
		ProviderID:  providerID,
		Label:       label,
		Source:      token.Harness + " login",
		FetchedAtMs: nowMs,
		Note:        &note,
	}
}

// hasAnyWindow reports whether a row carries at least one quota window.
func hasAnyWindow(pl ProviderLimits) bool {
	return pl.Primary != nil || pl.Secondary != nil || pl.Tertiary != nil || pl.Fable != nil
}

// preferLiveReading chooses between a collector's own cached reading and a
// live reading made with a token. A live reading with windows is the newest
// observation there is, so it wins. A failed live reading is shown only when
// the collector has nothing of its own, so a vendor outage never hides a
// usable cached snapshot.
func preferLiveReading(cached, live ProviderLimits) ProviderLimits {
	if hasAnyWindow(live) {
		return live
	}
	if cached.Hide || !hasAnyWindow(cached) {
		return live
	}
	return cached
}
