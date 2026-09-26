/**
 * Persists live usage request outcomes next to usage-history.json. Only the
 * resolved ProviderLimits or a failure message is stored — never a token — so
 * the cache file cannot leak a credential.
 */
package limits

import (
	"encoding/json"
	"os"
	"path/filepath"
)

func usageAPICachePath() string {
	if v := os.Getenv("USAGEBAR_USAGE_API_CACHE_PATH"); v != "" {
		return v
	}
	return filepath.Join(historyBaseDir(), "usage-api-cache.json")
}

// loadUsageAPICache never returns nil, so callers can assign into it.
func loadUsageAPICache() map[string]usageAPICacheEntry {
	entries := map[string]usageAPICacheEntry{}
	raw, err := os.ReadFile(usageAPICachePath())
	if err != nil {
		return entries
	}
	if json.Unmarshal(raw, &entries) != nil {
		return map[string]usageAPICacheEntry{}
	}
	return entries
}

// saveUsageAPICache drops entries that can no longer be reused before
// writing, so keys for signed-out accounts do not accumulate.
func saveUsageAPICache(entries map[string]usageAPICacheEntry, nowMs int64) {
	for key, entry := range entries {
		if !usageAPICacheFresh(entry, nowMs) {
			delete(entries, key)
		}
	}
	raw, err := json.Marshal(entries)
	if err != nil {
		return
	}
	path := usageAPICachePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	_ = os.WriteFile(path, raw, 0o600)
}
