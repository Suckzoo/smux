package tui

// ---------------------------------------------------------------------------
// Phase — typed TUI selection state machine
// ---------------------------------------------------------------------------
//
// The phase state machine models the four distinct interaction phases of the
// host-selection TUI.  The valid transition edges are:
//
//   BrowsingPhase  ──── '/'        ────────────────▶  SelectingPhase
//   BrowsingPhase  ──── Enter (< threshold) ────────▶  LaunchingPhase
//   BrowsingPhase  ──── Enter (≥ threshold) ────────▶  ConfirmingPhase
//   SelectingPhase ──── Esc / Enter ────────────────▶  BrowsingPhase
//   ConfirmingPhase ─── y ──────────────────────────▶  LaunchingPhase
//   ConfirmingPhase ─── n / Esc ────────────────────▶  BrowsingPhase
//
// Phase is a closed interface: only the four concrete types in this file
// satisfy it.  Callers use type-switch or type-assertion to discriminate.

// Phase represents the current interaction phase of the host-selection TUI.
// It is the discriminant of the selection state machine.
type Phase interface {
	isPhase() // unexported marker — keeps the union closed
}

// BrowsingPhase is the default interactive state.
// The user navigates the cluster tree, selects/deselects hosts with Space,
// and can press '/' to enter SelectingPhase or Enter to proceed.
type BrowsingPhase struct{}

// SelectingPhase is the filter-input state.
// The user types a fuzzy search query; the visible host list narrows in
// real time.  Esc clears the filter and returns to BrowsingPhase; Enter
// commits the filter and also returns to BrowsingPhase.
type SelectingPhase struct{}

// ConfirmingPhase is the large-selection confirmation dialog state.
// The user arrived here because the number of selected hosts reached or
// exceeded Threshold.  'y' advances to LaunchingPhase; 'n' or Esc returns
// to BrowsingPhase.
type ConfirmingPhase struct {
	// Threshold is the minimum number of selected hosts that triggers the
	// confirmation prompt.  It is configurable via config.yaml (large_selection_threshold);
	// the runtime default used by the TUI is DefaultConfirmThreshold (50).
	Threshold int
}

// LaunchingPhase is the terminal state of the selection flow.
// The user has confirmed their selection and smux is about to (or has
// already) created SSH panes.  The TUI exits after entering this phase.
type LaunchingPhase struct{}

// QuitConfirmingPhase is the quit-confirmation dialog state.
// Entered when the user presses 'q' in persistent mode. Shows a red bordered
// box warning that WindowCount other tmux windows will be killed.
// 'y' kills all managed windows and exits; 'n' or Esc returns to BrowsingPhase.
type QuitConfirmingPhase struct {
	// WindowCount is the number of non-smux tmux windows that will be killed.
	WindowCount int
}

// Marker implementations keep the Phase union closed.
func (BrowsingPhase) isPhase()       {}
func (SelectingPhase) isPhase()      {}
func (ConfirmingPhase) isPhase()     {}
func (LaunchingPhase) isPhase()      {}
func (QuitConfirmingPhase) isPhase() {}

// DefaultConfirmThreshold is the number of selected hosts at which the TUI
// switches to ConfirmingPhase before launching SSH panes.  It is used when
// no explicit threshold is specified in config.yaml.
const DefaultConfirmThreshold = 50

// ValidTransition reports whether transitioning from src to dst is a legal
// edge in the phase state machine.  Callers may use this function to guard
// state transitions at runtime.
func ValidTransition(src, dst Phase) bool {
	switch src.(type) {
	case BrowsingPhase:
		switch dst.(type) {
		case SelectingPhase:      // '/' pressed
			return true
		case ConfirmingPhase:     // Enter with ≥ Threshold hosts selected
			return true
		case LaunchingPhase:      // Enter with < Threshold hosts selected
			return true
		case QuitConfirmingPhase: // 'q' pressed in persistent mode
			return true
		}
	case SelectingPhase:
		switch dst.(type) {
		case BrowsingPhase: // Esc clears filter  /  Enter commits filter
			return true
		}
	case ConfirmingPhase:
		switch dst.(type) {
		case BrowsingPhase:  // n or Esc pressed
			return true
		case LaunchingPhase: // y pressed
			return true
		}
	case QuitConfirmingPhase:
		switch dst.(type) {
		case BrowsingPhase:  // n or Esc pressed
			return true
		case LaunchingPhase: // y pressed (kills windows then exits)
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// SelectionState — domain state of the host-selection flow
// ---------------------------------------------------------------------------

// SelectionState captures the domain-level state of the TUI selection flow.
// It is intentionally separate from ViewState, which holds purely visual
// concerns (cursor position, terminal dimensions).
//
// Rule: SelectionState must never contain any bubbletea UI component types
// (textinput.Model, viewport.Model, etc.).  Those belong in the Model struct
// alongside ViewState.
type SelectionState struct {
	// Phase is the current phase of the host-selection state machine.
	Phase Phase

	// Selected maps hostKey (cluster/hostName) → true for each currently
	// selected host entry.
	Selected map[string]bool
}

// ---------------------------------------------------------------------------
// ViewState — presentational state of the TUI
// ---------------------------------------------------------------------------

// ViewState captures the purely presentational state of the TUI.
// It is intentionally separate from SelectionState, which holds domain
// concerns (what the user is doing).
//
// Rule: ViewState must never contain domain concepts such as selection state
// or host lists.
type ViewState struct {
	// Cursor is the index of the currently focused row in the flat node list.
	Cursor int

	// Width and Height are the last-known terminal dimensions, updated from
	// tea.WindowSizeMsg events.
	Width  int
	Height int
}
