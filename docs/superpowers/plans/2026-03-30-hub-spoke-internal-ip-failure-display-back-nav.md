# Hub-Spoke Internal IP, Failure Reason Display, Back Navigation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `internal_ip` config field used for all spoke SSH connections in hub-and-spoke mode; show per-host failure reasons inline (truncated) with an overlay for full details; fix Esc after execution to return to the main host list.

**Architecture:** Three independent changes: (1) a new `InternalIP` field on `ResolvedHost` with an `EffectiveAddress()` accessor replaces hard-coded `host.Host` in all SSH/SCP target construction; (2) `DistributeModel` gains `hostErrors`, `progressCursor`, and `errorOverlay` fields driving a cursor-navigable progress view with a full-error overlay; (3) the global Esc handler gets two new early-exit cases: dismiss overlay, or return to main when execution is complete.

**Tech Stack:** Go, bubbletea (TUI), lipgloss (styling), gopkg.in/yaml.v3

---

## File Map

| File | Change |
|------|--------|
| `internal/config/config.go` | Add `InternalIP` to `HostEntry`, `ResolvedHost`, `UnmarshalYAML`, `Resolve()`; add `EffectiveAddress()`; update `ExampleConfig` |
| `internal/config/config_test.go` | Tests for `InternalIP` YAML parsing, `Resolve()` propagation, `EffectiveAddress()` |
| `internal/filetree/remote.go` | Use `EffectiveAddress()` in `buildSSHArgs` (line 175) and SFTP dest (lines 117-119) |
| `internal/executor/executor.go` | Use `EffectiveAddress()` in `buildSCPArgs` destination (lines 177-181) |
| `internal/executor/hubspoke.go` | Use `EffectiveAddress()` in `buildHubSCPCommand` spoke address (lines 187-191) |
| `internal/tui/execute.go` | Use `EffectiveAddress()` in `mkdirOnHost` (line 478); add `hostErrors` population in `handleProgressUpdate`; update `renderHostProgressRows` (cursor + truncated error); add `renderErrorOverlay`; update hint text in `renderExecuteStepWithProgress` |
| `internal/tui/distribute.go` | Add `progressCursor int`, `hostErrors map[string]string`, `errorOverlay *string` to `DistributeModel`; update `handleKey` Esc for overlay-dismiss and exit-to-main cases; add j/k/enter handling in `handleExecuteKey` |

---

## Task 1: Add `InternalIP` to config types and `EffectiveAddress()` accessor

**Files:**
- Modify: `internal/config/config.go`
- Test: `internal/config/config_test.go`

- [ ] **Step 1: Write the failing tests**

Add to `internal/config/config_test.go`:

```go
// TestInternalIPUnmarshalYAML verifies that the internal_ip field is decoded
// from the object form of a host entry.
func TestInternalIPUnmarshalYAML(t *testing.T) {
	type clusterHosts struct {
		Hosts []HostEntry `yaml:"hosts"`
	}
	input := `
hosts:
  - name: spoke-01.example.com
    internal_ip: 10.0.0.1
  - web-01.example.com
`
	var ch clusterHosts
	if err := yaml.Unmarshal([]byte(input), &ch); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(ch.Hosts) != 2 {
		t.Fatalf("expected 2 hosts, got %d", len(ch.Hosts))
	}
	if ch.Hosts[0].InternalIP != "10.0.0.1" {
		t.Errorf("InternalIP: got %q, want %q", ch.Hosts[0].InternalIP, "10.0.0.1")
	}
	if ch.Hosts[1].InternalIP != "" {
		t.Errorf("alias host InternalIP: got %q, want empty", ch.Hosts[1].InternalIP)
	}
}

// TestInternalIPResolve verifies that Resolve() copies InternalIP into ResolvedHost.
func TestInternalIPResolve(t *testing.T) {
	entry := HostEntry{
		Name:       "spoke-01.example.com",
		InternalIP: "10.0.0.1",
		Provenance: ProvenanceFull,
	}
	resolved := entry.Resolve("prod", HostDefaults{User: "ubuntu"})
	if resolved.InternalIP != "10.0.0.1" {
		t.Errorf("InternalIP: got %q, want %q", resolved.InternalIP, "10.0.0.1")
	}
}

// TestEffectiveAddress_WithInternalIP verifies that EffectiveAddress returns
// InternalIP when it is set.
func TestEffectiveAddress_WithInternalIP(t *testing.T) {
	h := ResolvedHost{Host: "spoke-01.example.com", InternalIP: "10.0.0.1"}
	if got := h.EffectiveAddress(); got != "10.0.0.1" {
		t.Errorf("EffectiveAddress: got %q, want %q", got, "10.0.0.1")
	}
}

// TestEffectiveAddress_WithoutInternalIP verifies that EffectiveAddress falls
// back to Host when InternalIP is empty.
func TestEffectiveAddress_WithoutInternalIP(t *testing.T) {
	h := ResolvedHost{Host: "web-01.example.com"}
	if got := h.EffectiveAddress(); got != "web-01.example.com" {
		t.Errorf("EffectiveAddress: got %q, want %q", got, "web-01.example.com")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
make test 2>&1 | grep -E "FAIL|undefined|InternalIP|EffectiveAddress"
```

Expected: compile errors about undefined `InternalIP` and `EffectiveAddress`.

- [ ] **Step 3: Add `InternalIP` to `HostEntry`, `ResolvedHost`, and `EffectiveAddress()`**

In `internal/config/config.go`:

1. Add `InternalIP string` to `HostEntry` (after `JumpHost`):

```go
type HostEntry struct {
	Name       string
	User       string
	Port       int
	Key        string
	JumpHost   string
	InternalIP string
	Provenance Provenance
}
```

2. Update `UnmarshalYAML` inner struct (add `InternalIP string \`yaml:"internal_ip"\``):

```go
var obj struct {
    Name       string `yaml:"name"`
    User       string `yaml:"user"`
    Port       int    `yaml:"port"`
    Key        string `yaml:"key"`
    JumpHost   string `yaml:"jump_host"`
    InternalIP string `yaml:"internal_ip"`
}
if err := value.Decode(&obj); err != nil {
    return err
}
h.Name = obj.Name
h.User = obj.User
h.Port = obj.Port
h.Key = obj.Key
h.JumpHost = obj.JumpHost
h.InternalIP = obj.InternalIP
h.Provenance = ProvenanceFull
return nil
```

3. Add `InternalIP string` to `ResolvedHost` (after `JumpHost`):

```go
type ResolvedHost struct {
	DisplayName  string
	Host         string
	User         string
	Port         int
	Key          string
	JumpHost     string
	InternalIP   string
	ClusterNames []string
	Provenance   Provenance
}
```

4. Update `Resolve()` to copy `InternalIP`:

```go
return ResolvedHost{
    DisplayName:  h.Name,
    Host:         h.Name,
    User:         user,
    Port:         port,
    Key:          key,
    JumpHost:     jumpHost,
    InternalIP:   h.InternalIP,
    ClusterNames: []string{clusterName},
    Provenance:   h.Provenance,
}
```

5. Add `EffectiveAddress()` method after `EffectiveConfirmThreshold()`:

```go
// EffectiveAddress returns InternalIP if set, otherwise Host.
// Use this instead of Host for SSH/SCP target address construction
// so that hosts with an internal_ip config field are reached via
// their internal address.
func (h ResolvedHost) EffectiveAddress() string {
	if h.InternalIP != "" {
		return h.InternalIP
	}
	return h.Host
}
```

6. Update `ExampleConfig` — add a commented `internal_ip` example inside the hosts block:

```go
const ExampleConfig = `clusters:
  production:
    defaults:
      user: ubuntu
      key: ~/.ssh/id_rsa
    hosts:
      - web-01.example.com
      - web-02.example.com
      - name: db-01.example.com
        user: postgres
      # internal_ip is optional. When set, all SSH/SCP connections to this
      # host use the internal IP instead of the name/public address.
      # Useful for hub-and-spoke mode where spokes are on a private network.
      # - name: spoke-01.example.com
      #   internal_ip: 10.0.0.11
  staging:
    defaults:
      user: ubuntu
    hosts:
      - staging-01.example.com
...
```

(Keep the rest of ExampleConfig unchanged.)

- [ ] **Step 4: Run tests to verify they pass**

```bash
make test 2>&1 | grep -E "FAIL|PASS|config"
```

Expected: all config tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): add internal_ip field and EffectiveAddress() to ResolvedHost"
```

---

## Task 2: Use `EffectiveAddress()` in all SSH/SCP target construction

**Files:**
- Modify: `internal/filetree/remote.go`
- Modify: `internal/executor/executor.go`
- Modify: `internal/executor/hubspoke.go`
- Modify: `internal/tui/execute.go`

- [ ] **Step 1: Write failing tests for buildSCPArgs with InternalIP**

Add to `internal/executor/executor_test.go`:

```go
// TestBuildSCPArgs_DestUsesInternalIP verifies that buildSCPArgs uses
// dst.InternalIP as the destination address when InternalIP is set.
func TestBuildSCPArgs_DestUsesInternalIP(t *testing.T) {
	kp := &sshkeys.TempKeyPair{PrivateKeyPath: "/tmp/key"}
	dst := config.ResolvedHost{
		Host:       "spoke-01.example.com",
		InternalIP: "10.0.0.1",
		User:       "ubuntu",
	}
	args := buildSCPArgs(config.ResolvedHost{}, "/src/file.txt", dst, "/dst/file.txt", kp)
	argsStr := strings.Join(args, " ")
	if !strings.Contains(argsStr, "ubuntu@10.0.0.1:/dst/file.txt") {
		t.Errorf("expected internal IP in args, got: %s", argsStr)
	}
	if strings.Contains(argsStr, "spoke-01.example.com") {
		t.Errorf("public hostname should not appear when InternalIP is set, got: %s", argsStr)
	}
}
```

Add to `internal/executor/hubspoke_test.go`:

```go
// TestBuildHubSCPCommand_SpokeUsesInternalIP verifies that buildHubSCPCommand
// uses spoke.InternalIP as the destination address when InternalIP is set.
func TestBuildHubSCPCommand_SpokeUsesInternalIP(t *testing.T) {
	hubKP := &sshkeys.HubKeyPair{RemotePrivateKeyPath: "/hub/.smux/id_rsa"}
	spoke := config.ResolvedHost{
		Host:       "spoke-01.example.com",
		InternalIP: "10.0.0.1",
		User:       "ubuntu",
	}
	cmd := buildHubSCPCommand("/hub/file.txt", spoke, "/dst/file.txt", hubKP)
	if !strings.Contains(cmd, "ubuntu@10.0.0.1:") {
		t.Errorf("expected internal IP in hub scp command, got: %s", cmd)
	}
	if strings.Contains(cmd, "spoke-01.example.com") {
		t.Errorf("public hostname should not appear when InternalIP is set, got: %s", cmd)
	}
}
```

Add to `internal/filetree/remote_test.go` (the existing test file):

```go
// TestBuildSSHArgsForHost_UsesInternalIP verifies that BuildSSHArgsForHost
// uses host.InternalIP as the target address when InternalIP is set.
func TestBuildSSHArgsForHost_UsesInternalIP(t *testing.T) {
	host := config.ResolvedHost{
		Host:       "spoke-01.example.com",
		InternalIP: "10.0.0.1",
	}
	args := BuildSSHArgsForHost(host)
	last := args[len(args)-1]
	if last != "10.0.0.1" {
		t.Errorf("BuildSSHArgsForHost: last arg (host) = %q, want %q", last, "10.0.0.1")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
make test 2>&1 | grep -E "FAIL|InternalIP|EffectiveAddress"
```

Expected: test failures because `EffectiveAddress()` is not yet called.

- [ ] **Step 3: Update `internal/filetree/remote.go`**

In `buildSSHArgs` (the unexported function ending around line 177):
- Change `args = append(args, host.Host)` → `args = append(args, host.EffectiveAddress())`

In the SFTP path (around lines 117-120), change:
```go
dest := host.Host
if host.User != "" {
    dest = host.User + "@" + host.Host
}
```
to:
```go
dest := host.EffectiveAddress()
if host.User != "" {
    dest = host.User + "@" + host.EffectiveAddress()
}
```

- [ ] **Step 4: Update `internal/executor/executor.go`**

In `buildSCPArgs` (around lines 177-181), change:
```go
dstAddr := dst.Host
if dst.User != "" {
    dstAddr = dst.User + "@" + dst.Host
}
args = append(args, dstAddr+":"+dstPath)
```
to:
```go
dstAddr := dst.EffectiveAddress()
if dst.User != "" {
    dstAddr = dst.User + "@" + dst.EffectiveAddress()
}
args = append(args, dstAddr+":"+dstPath)
```

- [ ] **Step 5: Update `internal/executor/hubspoke.go`**

In `buildHubSCPCommand` (around lines 187-191), change:
```go
destAddr := spoke.Host
if spoke.User != "" {
    destAddr = spoke.User + "@" + spoke.Host
}
parts = append(parts, hubShellEscape(destAddr+":"+destPath))
```
to:
```go
destAddr := spoke.EffectiveAddress()
if spoke.User != "" {
    destAddr = spoke.User + "@" + spoke.EffectiveAddress()
}
parts = append(parts, hubShellEscape(destAddr+":"+destPath))
```

- [ ] **Step 6: Update `internal/tui/execute.go` `mkdirOnHost`**

Around line 478, change:
```go
args = append(args, host.Host, "--", "mkdir -p "+shellEscapePath(dir))
```
to:
```go
args = append(args, host.EffectiveAddress(), "--", "mkdir -p "+shellEscapePath(dir))
```

- [ ] **Step 7: Run tests to verify they pass**

```bash
make test 2>&1 | grep -E "FAIL|PASS"
```

Expected: all tests PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/filetree/remote.go internal/executor/executor.go internal/executor/hubspoke.go internal/tui/execute.go internal/executor/executor_test.go internal/executor/hubspoke_test.go internal/filetree/remote_test.go
git commit -m "feat: use EffectiveAddress() in all SSH/SCP target construction"
```

---

## Task 3: Store failure reasons and add cursor/overlay fields to `DistributeModel`

**Files:**
- Modify: `internal/tui/distribute.go`
- Modify: `internal/tui/execute.go`

- [ ] **Step 1: Write failing tests for error reason storage**

Add to `internal/tui/execute_hub_retry_test.go` (new file, or add to existing execute test file):

Create `internal/tui/execute_failure_reason_test.go`:

```go
package tui

import (
	"errors"
	"testing"

	"github.com/Suckzoo/smux/internal/config"
	"github.com/Suckzoo/smux/internal/executor"
)

// TestHandleProgressUpdate_StoresErrorReason verifies that a TransferFailed
// update populates hostErrors with the Err message when Stderr is empty.
func TestHandleProgressUpdate_StoresErrorReason(t *testing.T) {
	m := DistributeModel{
		hostProgress: make(map[string]executor.TransferStatus),
		hostErrors:   make(map[string]string),
	}
	host := config.ResolvedHost{Host: "spoke-01.example.com", DisplayName: "spoke-01.example.com"}
	u := executor.ProgressUpdate{
		Host:   host,
		Status: executor.TransferFailed,
		Err:    errors.New("connection refused"),
	}
	updated, _ := m.handleProgressUpdate(u)
	got, ok := updated.hostErrors["spoke-01.example.com"]
	if !ok {
		t.Fatal("expected hostErrors entry for failed host")
	}
	if got != "connection refused" {
		t.Errorf("hostErrors reason: got %q, want %q", got, "connection refused")
	}
}

// TestHandleProgressUpdate_PrefersStderrOverErr verifies that Stderr is used
// as the failure reason when it is non-empty.
func TestHandleProgressUpdate_PrefersStderrOverErr(t *testing.T) {
	m := DistributeModel{
		hostProgress: make(map[string]executor.TransferStatus),
		hostErrors:   make(map[string]string),
	}
	host := config.ResolvedHost{Host: "spoke-01.example.com", DisplayName: "spoke-01.example.com"}
	u := executor.ProgressUpdate{
		Host:   host,
		Status: executor.TransferFailed,
		Err:    errors.New("exit status 1"),
		Stderr: "  ssh: connect to host 10.0.0.1 port 22: Connection refused\n",
	}
	updated, _ := m.handleProgressUpdate(u)
	want := "ssh: connect to host 10.0.0.1 port 22: Connection refused"
	if updated.hostErrors["spoke-01.example.com"] != want {
		t.Errorf("hostErrors reason: got %q, want %q", updated.hostErrors["spoke-01.example.com"], want)
	}
}

// TestHandleProgressUpdate_NoErrorOnSuccess verifies that TransferDone does
// not create a hostErrors entry.
func TestHandleProgressUpdate_NoErrorOnSuccess(t *testing.T) {
	m := DistributeModel{
		hostProgress: make(map[string]executor.TransferStatus),
		hostErrors:   make(map[string]string),
	}
	host := config.ResolvedHost{Host: "spoke-01.example.com", DisplayName: "spoke-01.example.com"}
	u := executor.ProgressUpdate{Host: host, Status: executor.TransferDone}
	updated, _ := m.handleProgressUpdate(u)
	if _, ok := updated.hostErrors["spoke-01.example.com"]; ok {
		t.Error("hostErrors should not have entry for successful host")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
make test 2>&1 | grep -E "FAIL|hostErrors|undefined"
```

Expected: compile errors because `hostErrors` doesn't exist yet.

- [ ] **Step 3: Add fields to `DistributeModel` in `distribute.go`**

In `internal/tui/distribute.go`, add three fields to the `DistributeModel` struct after `hostProgress`:

```go
// hostErrors stores the trimmed failure reason for each host that failed,
// keyed by host.Host. Populated by handleProgressUpdate on TransferFailed.
hostErrors map[string]string

// progressCursor is the row index of the highlighted host in the execute
// step's progress list. j/k move it; Enter opens the error overlay.
progressCursor int

// errorOverlay is non-nil when the full-error overlay is open. It holds
// the complete error text for the host selected by progressCursor.
// Set by handleExecuteKey on Enter; cleared by the global Esc handler.
errorOverlay *string
```

- [ ] **Step 4: Update `handleProgressUpdate` in `execute.go` to populate `hostErrors`**

Replace the existing `handleProgressUpdate` function:

```go
func (m DistributeModel) handleProgressUpdate(u executor.ProgressUpdate) (DistributeModel, tea.Cmd) {
	if m.hostProgress == nil {
		m.hostProgress = make(map[string]executor.TransferStatus)
	}
	m.hostProgress[u.Host.Host] = u.Status

	if u.Status == executor.TransferFailed {
		if m.hostErrors == nil {
			m.hostErrors = make(map[string]string)
		}
		reason := strings.TrimSpace(u.Stderr)
		if reason == "" && u.Err != nil {
			reason = u.Err.Error()
		}
		if reason == "" {
			reason = "(unknown error)"
		}
		m.hostErrors[u.Host.Host] = reason
	}

	return m, waitForProgress(m.progressCh)
}
```

Note: `handleProgressUpdate` is in `distribute.go` today; move it to `execute.go` since that file owns the execute-step logic. Either location works — keep it where it currently lives in `distribute.go` to minimise churn.

- [ ] **Step 5: Run tests to verify they pass**

```bash
make test 2>&1 | grep -E "FAIL|PASS"
```

Expected: all tests PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/tui/distribute.go internal/tui/execute.go internal/tui/execute_failure_reason_test.go
git commit -m "feat(tui): store per-host failure reasons; add progressCursor and errorOverlay fields"
```

---

## Task 4: Render failure reasons inline and add error overlay

**Files:**
- Modify: `internal/tui/execute.go`

- [ ] **Step 1: Write failing tests for cursor render and overlay render**

Create `internal/tui/execute_render_failure_test.go`:

```go
package tui

import (
	"strings"
	"testing"

	"github.com/Suckzoo/smux/internal/config"
	"github.com/Suckzoo/smux/internal/executor"
)

// TestRenderHostProgressRows_ShowsTruncatedError verifies that a failed host
// row includes a truncated error reason after "failed".
func TestRenderHostProgressRows_ShowsTruncatedError(t *testing.T) {
	m := DistributeModel{
		width: 120,
		destHosts: []config.ResolvedHost{
			{Host: "spoke-01.example.com", DisplayName: "spoke-01.example.com"},
		},
		hostProgress: map[string]executor.TransferStatus{
			"spoke-01.example.com": executor.TransferFailed,
		},
		hostErrors: map[string]string{
			"spoke-01.example.com": "ssh: connect to host 10.0.0.1 port 22: Connection refused",
		},
	}
	out := m.renderHostProgressRows()
	if !strings.Contains(out, "failed") {
		t.Error("expected 'failed' in output")
	}
	if !strings.Contains(out, "ssh: connect to host") {
		t.Error("expected truncated error message in output")
	}
}

// TestRenderHostProgressRows_CursorHighlight verifies that the row at
// progressCursor is visually distinct (contains the cursor marker).
func TestRenderHostProgressRows_CursorHighlight(t *testing.T) {
	m := DistributeModel{
		width: 120,
		destHosts: []config.ResolvedHost{
			{Host: "h1.example.com", DisplayName: "h1.example.com"},
			{Host: "h2.example.com", DisplayName: "h2.example.com"},
		},
		hostProgress: map[string]executor.TransferStatus{
			"h1.example.com": executor.TransferDone,
			"h2.example.com": executor.TransferDone,
		},
		progressCursor: 1,
	}
	out := m.renderHostProgressRows()
	// The cursor row should contain "▶" or similar marker
	if !strings.Contains(out, "▶") {
		t.Error("expected cursor marker '▶' in progress rows output")
	}
}

// TestRenderErrorOverlay_ContainsFullError verifies that the overlay renders
// the full error text.
func TestRenderErrorOverlay_ContainsFullError(t *testing.T) {
	m := DistributeModel{width: 100, height: 30}
	full := "ssh: connect to host 10.0.0.1 port 22: Connection refused\nsome extra detail"
	out := m.renderErrorOverlay(full)
	if !strings.Contains(out, full) {
		t.Errorf("overlay should contain full error text; got:\n%s", out)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
make test 2>&1 | grep -E "FAIL|renderErrorOverlay|undefined"
```

Expected: `renderErrorOverlay` undefined; cursor highlight not present.

- [ ] **Step 3: Update `renderHostProgressRows` in `execute.go`**

Replace the existing `renderHostProgressRows` function:

```go
func (m DistributeModel) renderHostProgressRows() string {
	pendingStyle    := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	inProgressStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("12"))
	doneStyle       := lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	failedStyle     := lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	cursorStyle     := lipgloss.NewStyle().Background(lipgloss.Color("4")).Foreground(lipgloss.Color("15")).Bold(true)

	// Reserve space for the cursor marker and status; rest goes to truncated error.
	// Total usable width: m.width minus borders/padding (approx 6 chars).
	maxLineWidth := m.width - 6
	if maxLineWidth < 40 {
		maxLineWidth = 40
	}

	var sb strings.Builder
	for i, host := range m.destHosts {
		status := executor.TransferPending
		if m.hostProgress != nil {
			if s, ok := m.hostProgress[host.Host]; ok {
				status = s
			}
		}

		var icon string
		var style lipgloss.Style
		var detail string
		switch status {
		case executor.TransferPending:
			icon = "○"
			detail = "pending"
			style = pendingStyle
		case executor.TransferInProgress:
			icon = "→"
			detail = "transferring…"
			style = inProgressStyle
		case executor.TransferDone:
			icon = "✓"
			detail = "done"
			style = doneStyle
		case executor.TransferFailed:
			icon = "✗"
			detail = "failed"
			if m.hostErrors != nil {
				if reason, ok := m.hostErrors[host.Host]; ok && reason != "" {
					// Truncate reason to fit on one line.
					prefix := "failed: "
					available := maxLineWidth - 2 - 1 - 30 - 2 - len(prefix) // icon + space + name + spaces + prefix
					if available < 10 {
						available = 10
					}
					truncated := reason
					if len(truncated) > available {
						truncated = truncated[:available-1] + "…"
					}
					detail = prefix + truncated
				}
			}
			style = failedStyle
		}

		isCursor := i == m.progressCursor
		line := fmt.Sprintf("  %s %-30s  %s", icon, host.DisplayName, detail)
		if isCursor {
			line = cursorStyle.Render("▶" + line[1:]) // replace leading space with cursor
		} else {
			line = style.Render(line)
		}
		sb.WriteString(line + "\n")
	}
	return sb.String()
}
```

- [ ] **Step 4: Add `renderErrorOverlay` in `execute.go`**

Add after `renderHostProgressRows`:

```go
// renderErrorOverlay renders a full-screen overlay box showing the complete
// error text for a failed host. Dismissed with Esc.
func (m DistributeModel) renderErrorOverlay(fullError string) string {
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("9"))
	bodyStyle  := lipgloss.NewStyle().Foreground(lipgloss.Color("15"))
	hintStyle  := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	boxWidth   := m.width - 6
	if boxWidth < 40 {
		boxWidth = 40
	}
	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("9")).
		Padding(1, 3).
		Width(boxWidth)

	inner := titleStyle.Render("Transfer Error") + "\n\n" +
		bodyStyle.Render(fullError) + "\n\n" +
		hintStyle.Render("esc to close")
	return boxStyle.Render(inner)
}
```

- [ ] **Step 5: Update `renderExecuteStepWithProgress` to show overlay and update hint text**

In `renderExecuteStepWithProgress`, add overlay check at the top after `if m.width < 40...` check (which is in `View()`), and update hint text:

Replace the existing `renderExecuteStepWithProgress` function:

```go
func (m DistributeModel) renderExecuteStepWithProgress() string {
	headStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15"))
	hintStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))

	// Show error overlay when active.
	if m.errorOverlay != nil {
		return m.renderErrorOverlay(*m.errorOverlay)
	}

	var sb strings.Builder

	if !m.executeStarted {
		sb.WriteString(headStyle.Render(fmt.Sprintf("Step %d of %d: Execute Distribution", m.stepIndex()+1, m.totalSteps())))
		sb.WriteString("\n\n")
		sb.WriteString(m.renderOperationSummary())
		sb.WriteString("\n\n")
		sb.WriteString(hintStyle.Render("enter to start  esc back  q quit"))
		return sb.String()
	}

	sb.WriteString(headStyle.Render(fmt.Sprintf("Step %d of %d: Distributing Files", m.stepIndex()+1, m.totalSteps())))
	sb.WriteString("\n\n")
	sb.WriteString(m.renderHostProgressRows())

	if m.executeDone {
		sb.WriteString("\n")
		sb.WriteString(m.renderCompletionSummary())
		sb.WriteString("\n\n")
		if len(m.failedHosts()) > 0 {
			sb.WriteString(hintStyle.Render("j/k select  enter view error  r retry failed  esc back  q quit"))
		} else {
			sb.WriteString(hintStyle.Render("esc back  q quit"))
		}
	}

	return sb.String()
}
```

- [ ] **Step 6: Run tests to verify they pass**

```bash
make test 2>&1 | grep -E "FAIL|PASS"
```

Expected: all tests PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/tui/execute.go internal/tui/execute_render_failure_test.go
git commit -m "feat(tui): show truncated failure reason inline; add error overlay renderer"
```

---

## Task 5: Key handling — cursor navigation, overlay open/close, Esc returns to main

**Files:**
- Modify: `internal/tui/distribute.go`
- Modify: `internal/tui/execute.go`

- [ ] **Step 1: Write failing tests**

Create `internal/tui/execute_nav_test.go`:

```go
package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Suckzoo/smux/internal/config"
	"github.com/Suckzoo/smux/internal/executor"
)

func makeExecuteDoneModel() DistributeModel {
	hosts := []config.ResolvedHost{
		{Host: "h1.example.com", DisplayName: "h1.example.com"},
		{Host: "h2.example.com", DisplayName: "h2.example.com"},
		{Host: "h3.example.com", DisplayName: "h3.example.com"},
	}
	return DistributeModel{
		step:          DistributeStepExecute,
		destHosts:     hosts,
		executeStarted: true,
		executeDone:   true,
		hostProgress: map[string]executor.TransferStatus{
			"h1.example.com": executor.TransferFailed,
			"h2.example.com": executor.TransferDone,
			"h3.example.com": executor.TransferFailed,
		},
		hostErrors: map[string]string{
			"h1.example.com": "connection refused",
			"h3.example.com": "timeout",
		},
		progressCursor: 0,
	}
}

// TestExecuteStep_CursorMovesDown verifies that pressing 'j' increments progressCursor.
func TestExecuteStep_CursorMovesDown(t *testing.T) {
	m := makeExecuteDoneModel()
	updated, _ := m.handleExecuteKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if updated.progressCursor != 1 {
		t.Errorf("progressCursor after j: got %d, want 1", updated.progressCursor)
	}
}

// TestExecuteStep_CursorMovesUp verifies that pressing 'k' decrements progressCursor.
func TestExecuteStep_CursorMovesUp(t *testing.T) {
	m := makeExecuteDoneModel()
	m.progressCursor = 2
	updated, _ := m.handleExecuteKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	if updated.progressCursor != 1 {
		t.Errorf("progressCursor after k: got %d, want 1", updated.progressCursor)
	}
}

// TestExecuteStep_CursorClampsAtBottom verifies that j does not exceed len(destHosts)-1.
func TestExecuteStep_CursorClampsAtBottom(t *testing.T) {
	m := makeExecuteDoneModel()
	m.progressCursor = 2
	updated, _ := m.handleExecuteKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if updated.progressCursor != 2 {
		t.Errorf("cursor should clamp at bottom; got %d", updated.progressCursor)
	}
}

// TestExecuteStep_CursorClampsAtTop verifies that k does not go below 0.
func TestExecuteStep_CursorClampsAtTop(t *testing.T) {
	m := makeExecuteDoneModel()
	m.progressCursor = 0
	updated, _ := m.handleExecuteKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	if updated.progressCursor != 0 {
		t.Errorf("cursor should clamp at top; got %d", updated.progressCursor)
	}
}

// TestExecuteStep_EnterOpenOverlayForFailedHost verifies that Enter on a
// failed host sets errorOverlay to the host's error reason.
func TestExecuteStep_EnterOpenOverlayForFailedHost(t *testing.T) {
	m := makeExecuteDoneModel()
	m.progressCursor = 0 // h1 = failed
	updated, _ := m.handleExecuteKey(tea.KeyMsg{Type: tea.KeyEnter})
	if updated.errorOverlay == nil {
		t.Fatal("expected errorOverlay to be set after Enter on failed host")
	}
	if *updated.errorOverlay != "connection refused" {
		t.Errorf("errorOverlay: got %q, want %q", *updated.errorOverlay, "connection refused")
	}
}

// TestExecuteStep_EnterNoOverlayForSuccessHost verifies that Enter on a
// successful host does not open the overlay.
func TestExecuteStep_EnterNoOverlayForSuccessHost(t *testing.T) {
	m := makeExecuteDoneModel()
	m.progressCursor = 1 // h2 = done
	updated, _ := m.handleExecuteKey(tea.KeyMsg{Type: tea.KeyEnter})
	if updated.errorOverlay != nil {
		t.Error("errorOverlay should not be set for a successful host")
	}
}

// TestExecuteStep_EscWithOverlayClosesOverlay verifies that Esc clears the
// overlay without exiting the wizard.
func TestExecuteStep_EscWithOverlayClosesOverlay(t *testing.T) {
	m := makeExecuteDoneModel()
	reason := "connection refused"
	m.errorOverlay = &reason
	updated, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	if updated.errorOverlay != nil {
		t.Error("Esc should clear errorOverlay")
	}
	if updated.exitToMain {
		t.Error("Esc should not set exitToMain while overlay is open")
	}
}

// TestExecuteStep_EscAfterDoneExitsToMain verifies that Esc after execution
// completes (with no overlay) sets exitToMain.
func TestExecuteStep_EscAfterDoneExitsToMain(t *testing.T) {
	m := makeExecuteDoneModel()
	updated, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	if !updated.exitToMain {
		t.Error("Esc after executeDone should set exitToMain")
	}
	if !updated.done {
		t.Error("Esc after executeDone should set done")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
make test 2>&1 | grep -E "FAIL|undefined|progressCursor"
```

Expected: test failures because cursor/overlay key handling doesn't exist.

- [ ] **Step 3: Update `handleExecuteKey` in `distribute.go`**

Replace the existing `handleExecuteKey`:

```go
func (m DistributeModel) handleExecuteKey(msg tea.KeyMsg) (DistributeModel, tea.Cmd) {
	switch msg.String() {
	case "enter":
		if !m.executeStarted {
			cmd := m.startExecution()
			return m, cmd
		}
		// Open error overlay for the selected host if it failed.
		if m.executeDone && m.errorOverlay == nil && len(m.destHosts) > 0 {
			selected := m.destHosts[m.progressCursor]
			if m.hostProgress[selected.Host] == executor.TransferFailed {
				reason := "(no details available)"
				if m.hostErrors != nil {
					if r, ok := m.hostErrors[selected.Host]; ok && r != "" {
						reason = r
					}
				}
				m.errorOverlay = &reason
			}
		}
	case "j", "down":
		if len(m.destHosts) > 0 {
			if m.progressCursor < len(m.destHosts)-1 {
				m.progressCursor++
			}
		}
	case "k", "up":
		if m.progressCursor > 0 {
			m.progressCursor--
		}
	case "r":
		if m.executeDone {
			failed := m.failedHosts()
			if len(failed) > 0 {
				srcPath := ""
				if len(m.sourcePaths) > 0 {
					srcPath = m.sourcePaths[0]
				}
				allHosts := append([]config.ResolvedHost(nil), m.destHosts...)
				if m.retryParams != nil && len(m.retryParams.AllHosts) > 0 {
					allHosts = append([]config.ResolvedHost(nil), m.retryParams.AllHosts...)
				}
				params := executor.RetryParams{
					SourceHost:  m.resolvedSourceHost(),
					SourcePath:  srcPath,
					DestPath:    m.effectiveDestPath(),
					CopyMode:    m.copyMode,
					FailedHosts: failed,
					AllHosts:    allHosts,
				}
				retryModel := NewRetryDistributeModel(m.cfg, m.width, m.height, params)
				retryModel.verifyChecksum = m.verifyChecksum
				return retryModel, nil
			}
		}
	}
	return m, nil
}
```

- [ ] **Step 4: Update the global Esc handler in `handleKey` in `distribute.go`**

Find the `case "esc":` block inside `handleKey`. Replace:

```go
case "esc":
    if m.step > 0 {
        m.step--
        // In direct-parallel mode the hub-selection step does not exist.
        // If stepping back lands on DistributeStepHubSelect but the current
        // copy mode is not hub-and-spoke, skip over it.
        if m.step == DistributeStepHubSelect && m.copyMode != "hub-spoke" {
            m.step--
        }
    } else {
        // Esc from the first step or retry-confirm: signal the parent to
        // return to normal TUI.
        m.exitToMain = true
        m.done = true
    }
    return m, nil
```

with:

```go
case "esc":
    // Priority 1: dismiss the error overlay if it is open.
    if m.errorOverlay != nil {
        m.errorOverlay = nil
        return m, nil
    }
    // Priority 2: return to main host list when execution has finished.
    if m.step == DistributeStepExecute && m.executeDone {
        m.exitToMain = true
        m.done = true
        return m, nil
    }
    // Default: step back through the wizard.
    if m.step > 0 {
        m.step--
        // In direct-parallel mode the hub-selection step does not exist.
        // If stepping back lands on DistributeStepHubSelect but the current
        // copy mode is not hub-and-spoke, skip over it.
        if m.step == DistributeStepHubSelect && m.copyMode != "hub-spoke" {
            m.step--
        }
    } else {
        // Esc from the first step or retry-confirm: signal the parent to
        // return to normal TUI.
        m.exitToMain = true
        m.done = true
    }
    return m, nil
```

- [ ] **Step 5: Run tests to verify they pass**

```bash
make test 2>&1 | grep -E "FAIL|PASS"
```

Expected: all tests PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/tui/distribute.go internal/tui/execute_nav_test.go
git commit -m "feat(tui): cursor nav in execute step; error overlay open/close; esc returns to main after copy"
```

---

## Self-Review

**Spec coverage:**
1. ✅ `internal_ip` in config for each host — Task 1
2. ✅ Spokes (and all SSH targets when `internal_ip` set) use `EffectiveAddress()` — Task 2
3. ✅ Copy failure reason shown inline (truncated) — Task 4
4. ✅ Full error on key press (Enter → overlay) — Task 5
5. ✅ Keybind returns to initial screen (Esc after executeDone → exitToMain) — Task 5

**Placeholder scan:** No TBD/TODO present. All code is concrete.

**Type consistency:**
- `EffectiveAddress()` defined in Task 1, used in Task 2 — consistent.
- `hostErrors map[string]string` defined in Task 3, populated in Task 3, read in Task 4 — consistent.
- `progressCursor int` defined in Task 3, moved in Task 5, rendered in Task 4 — consistent.
- `errorOverlay *string` defined in Task 3, set in Task 5, rendered in Task 4 — consistent.
- `handleExecuteKey` in Task 5 references `m.retryParams`, `m.sourcePaths`, `executor.RetryParams`, `NewRetryDistributeModel` — all pre-existing, no new types.
