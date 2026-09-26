/**
 * Throttle for live vendor usage requests made with a SubscriptionToken.
 *
 * The panel re-collects on every refresh tick and the sidebar on every pane
 * status change, so an uncached token path would call the vendor many times
 * a minute. Each outcome, success or failure, suppresses another request for
 * the same key for usageAPICacheTTLMs, matching the five-minute floor other
 * usage dashboards use for the same endpoints. Disk access lives in
 * usageapicache_io.go.
 */
package limits

import "errors"

const usageAPICacheTTLMs = 5 * 60_000

// usageAPICacheEntry is the persisted outcome of the last request for one
// key. Limits is nil exactly when Failure is set.
type usageAPICacheEntry struct {
	FetchedAtMs int64           `json:"fetchedAtMs"`
	Limits      *ProviderLimits `json:"limits,omitempty"`
	Failure     string          `json:"failure,omitempty"`
}

// usageAPICacheFresh reports whether an entry may still be reused. A clock
// that jumped backwards invalidates the entry rather than pinning it.
func usageAPICacheFresh(entry usageAPICacheEntry, nowMs int64) bool {
	if entry.FetchedAtMs <= 0 || nowMs < entry.FetchedAtMs {
		return false
	}
	if entry.Limits == nil && entry.Failure == "" {
		return false
	}
	return nowMs-entry.FetchedAtMs < usageAPICacheTTLMs
}

// cachedUsageFetch returns the cached outcome for key while it is fresh, and
// otherwise runs fetch and records its outcome. The returned limits are a
// copy, so a caller restamping ids cannot alter the cached entry.
func cachedUsageFetch(key string, nowMs int64, fetch func() (*ProviderLimits, error)) (*ProviderLimits, error) {
	entries := loadUsageAPICache()
	if entry, ok := entries[key]; ok && usageAPICacheFresh(entry, nowMs) {
		return entry.outcome()
	}
	limits, err := fetch()
	entry := usageAPICacheEntry{FetchedAtMs: nowMs}
	switch {
	case err != nil:
		entry.Failure = err.Error()
	case limits == nil:
		entry.Failure = "empty usage response"
	default:
		copied := *limits
		entry.Limits = &copied
	}
	entries[key] = entry
	saveUsageAPICache(entries, nowMs)
	return entry.outcome()
}

func (e usageAPICacheEntry) outcome() (*ProviderLimits, error) {
	if e.Limits == nil {
		return nil, errors.New(e.Failure)
	}
	copied := *e.Limits
	return &copied, nil
}
