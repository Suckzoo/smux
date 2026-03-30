package tui

import "github.com/charmbracelet/bubbles/viewport"

// ClampViewport adjusts vp.YOffset so that the given cursor row is visible
// within the viewport's height, and ensures the offset never exceeds the
// maximum scrollable position for the given number of totalItems. When the
// list is shorter than the viewport height, YOffset is forced to 0 so that
// all items are visible without blank space.
//
// Call this after every cursor movement or list-length change to keep the
// highlighted item on-screen and prevent overflow/blank-space issues.
func ClampViewport(vp *viewport.Model, cursor int, totalItems ...int) {
	// First, ensure cursor is within the visible window.
	if cursor < vp.YOffset {
		vp.YOffset = cursor
	} else if cursor >= vp.YOffset+vp.Height {
		vp.YOffset = cursor - vp.Height + 1
	}

	// If a totalItems count was provided, cap YOffset so the viewport never
	// scrolls past the end of the list. For short lists (fewer items than
	// viewport height) this forces YOffset to 0.
	if len(totalItems) > 0 {
		n := totalItems[0]
		maxOffset := n - vp.Height
		if maxOffset < 0 {
			maxOffset = 0
		}
		if vp.YOffset > maxOffset {
			vp.YOffset = maxOffset
		}
	}

	// Guard against negative offset.
	if vp.YOffset < 0 {
		vp.YOffset = 0
	}
}

// VisibleRange returns the (start, end) slice indices into a list of length
// totalItems that should be rendered given the current viewport offset and
// height. The returned range is safe to use directly as list[start:end].
//
// For lists shorter than the viewport height, start is always 0 so that all
// items are rendered without blank leading space.
func VisibleRange(vp *viewport.Model, totalItems int) (start, end int) {
	start = vp.YOffset

	// For short lists that fit entirely within the viewport, always start
	// from 0 to avoid showing a partial/empty view.
	if totalItems <= vp.Height {
		start = 0
	}

	end = start + vp.Height
	if end > totalItems {
		end = totalItems
	}
	if start > end {
		start = end
	}
	return start, end
}
