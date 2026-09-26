package limits

import (
	"strings"
	"testing"

	"github.com/senna-lang/herdr-agent-usage/internal/providers/antigravity"
)

func TestClearElapsedWindows(t *testing.T) {
	past, future := testNowMs/1000-60, testNowMs/1000+3600
	week, fiveHours := 10080, 300
	used := &LimitWindow{UsedPercentage: 86, ResetsAt: &past, WindowMinutes: &week}
	rows := []ProviderLimits{{
		ProviderID: "claude",
		Primary:    &LimitWindow{UsedPercentage: 40, ResetsAt: &future, WindowMinutes: &fiveHours},
		Secondary:  used,
		Fable:      &LimitWindow{UsedPercentage: 6, ResetsAt: &past, WindowMinutes: &week},
	}}

	got := clearElapsedWindows(rows, testNowMs)[0]
	if got.Primary.UsedPercentage != 40 || got.Primary.ResetsAt == nil {
		t.Fatalf("a live window must be untouched: %+v", got.Primary)
	}
	if got.Secondary.UsedPercentage != 0 || got.Secondary.ResetsAt != nil || *got.Secondary.WindowMinutes != week {
		t.Fatalf("elapsed 7d = %+v, want unused, no countdown, same length", got.Secondary)
	}
	if got.Note == nil || *got.Note != "7d, Fable reset since this reading" {
		t.Fatalf("note = %v", got.Note)
	}
	if used.UsedPercentage != 86 {
		t.Fatal("input window was mutated")
	}
	panel := FormatLimitsPanel([]ProviderLimits{got}, testNowMs, PanelLayout{Columns: 80, Rows: 40})
	if strings.Contains(panel, "soon") {
		t.Fatalf("panel still shows an elapsed countdown:\n%s", panel)
	}
}

func TestCollectAllProviderLimits_ClearsElapsedWindows(t *testing.T) {
	past := testNowMs/1000 - 60
	week := 10080
	opts := CollectOptions{
		Only: map[string]bool{antigravity.Provider.AgentID(): true},
		Antigravity: func(_ *string, _ int64) ProviderLimits {
			return ProviderLimits{ProviderID: "antigravity", Secondary: &LimitWindow{UsedPercentage: 86, ResetsAt: &past, WindowMinutes: &week}}
		},
	}
	got := CollectAllProviderLimits(nil, testNowMs, opts)
	if len(got) != 1 || got[0].Secondary == nil || got[0].Secondary.UsedPercentage != 0 {
		t.Fatalf("got %+v, want the elapsed window cleared by collection", got)
	}
}
