package limits

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClaudeLimitsCache_WriteAndRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")
	if err := WriteClaudeLimitsCache(fiveHourInput(30), 5_000, path); err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatal(err)
	}
	if got := raw["fiveHour"].(map[string]any)["usedPercentage"]; got != float64(30) {
		t.Fatalf("cached percentage = %v, want 30", got)
	}
	cached := collectFromStatusLineCache(6_000, path)
	if cached == nil || cached.Primary == nil || cached.Primary.UsedPercentage != 30 {
		t.Fatalf("statusLine cache = %+v", cached)
	}
}

func fiveHourInput(pct float64) RateLimitsInput {
	return RateLimitsInput{FiveHour: &struct {
		UsedPercentage float64
		ResetsAt       int64
	}{pct, 1000}}
}

func TestWriteClaudeLimitsCacheGuarded_SkipsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")
	if wrote, err := WriteClaudeLimitsCacheGuarded(fiveHourInput(42), 1_000, path); err != nil || !wrote {
		t.Fatalf("seed: wrote=%v err=%v", wrote, err)
	}
	if wrote, err := WriteClaudeLimitsCacheGuarded(RateLimitsInput{}, 2_000, path); err != nil || wrote {
		t.Fatalf("empty payload: wrote=%v err=%v", wrote, err)
	}
	if got := collectFromStatusLineCache(3_000, path); got == nil || got.Primary == nil || got.Primary.UsedPercentage != 42 {
		t.Fatalf("prior cache was lost: %+v", got)
	}
}

func TestWriteClaudeLimitsCacheGuarded_StoresPromptCacheExpiry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")
	expires := int64(1_700_003_600)
	input := fiveHourInput(42)
	input.PromptCachePresent = true
	input.PromptCacheExpiresAt = &expires
	if wrote, err := WriteClaudeLimitsCacheGuarded(input, 1_000, path); err != nil || !wrote {
		t.Fatalf("wrote=%v err=%v", wrote, err)
	}
	if got := ReadPromptCacheExpiresAt(path); got == nil || *got != expires {
		t.Fatalf("expires=%v, want %d", got, expires)
	}
	if wrote, err := WriteClaudeLimitsCacheGuarded(RateLimitsInput{PromptCachePresent: true}, 2_000, path); err != nil || !wrote {
		t.Fatalf("cold prefix: wrote=%v err=%v", wrote, err)
	}
	if got := ReadPromptCacheExpiresAt(path); got != nil {
		t.Fatalf("cold prefix must clear expiry: %v", got)
	}
	if got := collectFromStatusLineCache(3_000, path); got == nil || got.Primary == nil || got.Primary.UsedPercentage != 42 {
		t.Fatalf("quota cache must survive prompt-cache update: %+v", got)
	}
}

func TestWriteClaudeLimitsCacheGuarded_SeparateProfilePaths(t *testing.T) {
	dir := t.TempDir()
	pathA, pathB := filepath.Join(dir, "a", "cache.json"), filepath.Join(dir, "b", "cache.json")
	for _, tc := range []struct {
		path string
		pct  float64
	}{{pathA, 10}, {pathB, 90}} {
		if err := os.MkdirAll(filepath.Dir(tc.path), 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := WriteClaudeLimitsCacheGuarded(fiveHourInput(tc.pct), 1_000, tc.path); err != nil {
			t.Fatal(err)
		}
		if got := collectFromStatusLineCache(2_000, tc.path); got == nil || got.Primary == nil || got.Primary.UsedPercentage != tc.pct {
			t.Fatalf("profile %s = %+v", tc.path, got)
		}
	}
}

// mustHaveBorrowableObservation verifies that a test's OMP history fixture
// holds usable windows before asserting whether a collector borrows them.
func mustHaveBorrowableObservation(t *testing.T, providerID string) {
	t.Helper()
	if SelectAccountWindows(ObserveAccountWindows(), providerID, "") == nil {
		t.Fatalf("fixture holds no borrowable %s observation", providerID)
	}
}

func TestClaudeLimits_NoLoginRejectsCachedQuota(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
	statusPath := filepath.Join(dir, "cache.json")
	if err := WriteClaudeLimitsCache(fiveHourInput(42), 1_000, statusPath); err != nil {
		t.Fatal(err)
	}
	if got := collectFromStatusLineCache(2_000, statusPath); got == nil || got.Primary == nil {
		t.Fatal("fixture must contain a usable cached window")
	}
	got := CollectClaudeLimits(2_000, CollectClaudeLimitsOptions{CredentialsPath: filepath.Join(dir, "missing-credentials.json")})
	if got.Primary != nil || got.Secondary != nil || got.Fable != nil || got.Note == nil || !strings.Contains(*got.Note, "no readable OAuth login") {
		t.Fatalf("no login must report unavailable without cached bars: %+v", got)
	}
}
