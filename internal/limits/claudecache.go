/**
 * Rate-limit collection for Claude: the statusLine cache writer, and the
 * collector that merges every source of this account's windows (see
 * CollectClaudeLimits).
 */
package limits

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
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
	StatusLineCachePath string
	ClaudeJSONPath      string
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

// CollectClaudeLimits returns the freshest observation of each of this
// account's windows. Sources, newest first by their own timestamps:
//
//   - a live read of Anthropic's usage endpoint with the profile's OAuth
//     token (or a routing harness's Anthropic login when Claude Code is not
//     signed in), throttled to one request per five minutes;
//   - the statusLine cache (5h and weekly only);
//   - ~/.claude.json cachedUsageUtilization (5h, weekly and Fable, as of the
//     user's last /usage);
//   - another agent's observation of the same account (windowpool.go).
//
// Windows are merged per slot rather than taking one source whole: the
// statusLine never carries Fable, so picking it whole would drop a Fable
// window the older ~/.claude.json still has.
func CollectClaudeLimits(nowMs int64, options CollectClaudeLimitsOptions) ProviderLimits {
	statusPath := options.StatusLineCachePath
	if statusPath == "" {
		statusPath = ResolveClaudeLimitsCachePath()
	}
	jsonPath := options.ClaudeJSONPath
	if jsonPath == "" {
		jsonPath = ResolveClaudeJSONPath()
	}
	credentialsPath := options.CredentialsPath
	if credentialsPath == "" {
		credentialsPath = resolveClaudeCredentialsPath("")
	}

	// The windows belong to the account, so any agent's reading of them
	// counts — including when Claude Code wrote nothing at all.
	account, _ := AccountEmailFromJSONPath(jsonPath)
	observations := []*ProviderLimits{
		CollectClaudeLimitsFromJSON(nowMs, jsonPath),
		collectFromStatusLineCache(nowMs, statusPath),
		borrowWindows("claude", "Claude", account, nowMs),
	}
	var live *ProviderLimits
	if token := claudeUsageToken(credentialsPath, options.CollectorID); token != nil {
		reading := collectClaudeWithToken(*token, nowMs)
		live = &reading
		observations = append(observations, live)
	}
	if merged := mergeClaudeObservations(nowMs, observations); merged != nil {
		return *merged
	}
	if live != nil {
		return *live
	}
	note := "no ~/.claude.json utilization and no statusLine cache"
	return ProviderLimits{
		ProviderID:  "claude",
		Label:       "Claude",
		Source:      "none",
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

// claudeSlotStaleMinutes is how much older than the row's newest source a
// window filled from another source may be before it is labeled with its
// own age. It matches the statusLine staleness tolerance.
const claudeSlotStaleMinutes = 30

// mergeClaudeObservations builds one row from every observation that has
// at least one window. The newest observation supplies the row's source,
// timestamp and note; each window it lacks is taken from the next newest
// observation that has it, and labeled with that source's age when it is
// meaningfully older. Returns nil when no observation has a window.
func mergeClaudeObservations(nowMs int64, observations []*ProviderLimits) *ProviderLimits {
	var usable []*ProviderLimits
	for _, obs := range observations {
		if obs != nil && hasAnyWindow(*obs) {
			usable = append(usable, obs)
		}
	}
	if len(usable) == 0 {
		return nil
	}
	sort.SliceStable(usable, func(i, j int) bool { return usable[i].FetchedAtMs > usable[j].FetchedAtMs })
	merged := *usable[0]
	var olderSlots []string
	for _, older := range usable[1:] {
		filled := fillMissingClaudeSlots(&merged, *older)
		if len(filled) > 0 && minutesBetween(older.FetchedAtMs, merged.FetchedAtMs) > claudeSlotStaleMinutes {
			age := minutesBetween(older.FetchedAtMs, nowMs)
			olderSlots = append(olderSlots, strings.Join(filled, ", ")+" as of ~"+itoa(age)+"m ago ("+older.Source+")")
		}
	}
	if len(olderSlots) > 0 {
		note := strings.Join(olderSlots, "; ")
		if merged.Note != nil {
			note = *merged.Note + "; " + note
		}
		merged.Note = &note
	}
	return &merged
}

// fillMissingClaudeSlots copies each window (and the plan) that merged lacks
// from older, and returns the display names of the windows it copied.
func fillMissingClaudeSlots(merged *ProviderLimits, older ProviderLimits) []string {
	var filled []string
	if merged.Primary == nil && older.Primary != nil {
		merged.Primary = older.Primary
		filled = append(filled, "5h")
	}
	if merged.Secondary == nil && older.Secondary != nil {
		merged.Secondary = older.Secondary
		filled = append(filled, "7d")
	}
	if merged.Fable == nil && older.Fable != nil {
		merged.Fable = older.Fable
		filled = append(filled, "Fable")
	}
	if merged.PlanType == nil {
		merged.PlanType = older.PlanType
	}
	return filled
}
