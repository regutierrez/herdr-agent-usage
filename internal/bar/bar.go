/**
 * Renders the remaining-amount gauge (a horizontal bar) and its
 * threshold-based coloring.
 *
 * IMPORTANT: ANSI escapes must be applied last, after layout is computed.
 * A colorized string's length is offset by the escape bytes, so width
 * and alignment calculations always run on the plain output of RenderBar.
 */
package bar

import (
	"math"
	"strings"
)

const full = "█"
const empty = "░"

// RenderBar renders remaining (0-100) into a bar of width columns.
// The visible length always equals width (no alignment drift).
func RenderBar(remaining float64, width int) string {
	clamped := remaining
	if math.IsNaN(clamped) || math.IsInf(clamped, 0) {
		clamped = 0
	}
	if clamped < 0 {
		clamped = 0
	}
	if clamped > 100 {
		clamped = 100
	}
	w := width
	if w < 1 {
		w = 1
	}
	// Whole cells avoid gaps where terminal fonts cannot draw fractional blocks.
	filled := int(math.Round((clamped / 100) * float64(w)))
	return strings.Repeat(full, filled) + strings.Repeat(empty, w-filled)
}

// BarTone is the color tone for remaining amount; higher means safer (green).
type BarTone string

const (
	ToneHigh BarTone = "high"
	ToneMid  BarTone = "mid"
	ToneLow  BarTone = "low"
)

// ToneForRemaining returns tone based on remaining amount.
func ToneForRemaining(remaining float64) BarTone {
	if remaining >= 50 {
		return ToneHigh
	}
	if remaining >= 20 {
		return ToneMid
	}
	return ToneLow
}

var toneANSI = map[BarTone]string{
	ToneHigh: "32", // green
	ToneMid:  "33", // yellow
	ToneLow:  "31", // red
}

// Colorize is a pass-through when enabled is false (tests / non-TTY).
func Colorize(text string, tone BarTone, enabled bool) string {
	if !enabled {
		return text
	}
	return "\x1b[" + toneANSI[tone] + "m" + text + "\x1b[0m"
}

// Dim styling. Used for data-less tracks, etc.
func Dim(text string, enabled bool) string {
	if !enabled {
		return text
	}
	return "\x1b[2m" + text + "\x1b[0m"
}

// Bold styling. Used for headers, etc.
func Bold(text string, enabled bool) string {
	if !enabled {
		return text
	}
	return "\x1b[1m" + text + "\x1b[0m"
}
