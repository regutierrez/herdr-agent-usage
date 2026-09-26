/**
 * Normalizes windows whose reset time has already passed.
 *
 * A cached snapshot keeps its old usage after the window it describes has
 * reset, so without this an 86%-used weekly window read three days ago would
 * still render as 14% left with a "soon" countdown. Once a window has reset,
 * the usage it recorded no longer counts: the window is shown as unused, its
 * countdown is dropped, and the row says the reset happened after the
 * reading so it is not mistaken for a live 0%.
 */
package limits

import "strings"

// clearElapsedWindows returns rows with every elapsed window replaced by an
// unused window of the same length. Input windows are not mutated: they may
// be shared with caches.
func clearElapsedWindows(rows []ProviderLimits, nowMs int64) []ProviderLimits {
	out := make([]ProviderLimits, len(rows))
	for i, row := range rows {
		var reset []string
		// Fable is a named bucket, so its label never comes from its length.
		for _, slot := range []struct {
			window    **LimitWindow
			tag       string
			fixedName bool
		}{
			{&row.Primary, "5h", false},
			{&row.Secondary, "7d", false},
			{&row.Tertiary, "30d", false},
			{&row.Fable, "Fable", true},
		} {
			if !windowElapsed(*slot.window, nowMs) {
				continue
			}
			tag := slot.tag
			if !slot.fixedName {
				tag = windowTag(*slot.window, slot.tag)
			}
			reset = append(reset, tag)
			*slot.window = &LimitWindow{WindowMinutes: (*slot.window).WindowMinutes}
		}
		if len(reset) > 0 {
			note := strings.Join(reset, ", ") + " reset since this reading"
			if row.Note != nil {
				note = *row.Note + "; " + note
			}
			row.Note = &note
		}
		out[i] = row
	}
	return out
}

func windowElapsed(w *LimitWindow, nowMs int64) bool {
	return w != nil && w.ResetsAt != nil && *w.ResetsAt > 0 && *w.ResetsAt*1000 <= nowMs
}
