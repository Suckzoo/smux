package executor

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Suckzoo/smux/internal/config"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// makeResult is a convenience constructor for CopyResult values used in tests.
func makeResult(host string, success bool, stderr string, err error) CopyResult {
	return CopyResult{
		Host:    config.ResolvedHost{Host: host},
		Success: success,
		Stderr:  stderr,
		Err:     err,
	}
}

// makeResultWithUser constructs a CopyResult with user and port set.
func makeResultWithUser(host, user string, port int, success bool) CopyResult {
	return CopyResult{
		Host:    config.ResolvedHost{Host: host, User: user, Port: port},
		Success: success,
	}
}

// ---------------------------------------------------------------------------
// NewDistributeReport
// ---------------------------------------------------------------------------

// TestNewDistributeReport_Metadata verifies that operation parameters are
// stored verbatim in the report.
func TestNewDistributeReport_Metadata(t *testing.T) {
	report := NewDistributeReport("src.example.com", "/src/file.txt", "/dst/file.txt", "parallel", nil)

	if report.SourceHost != "src.example.com" {
		t.Errorf("SourceHost: expected %q, got %q", "src.example.com", report.SourceHost)
	}
	if report.SourcePath != "/src/file.txt" {
		t.Errorf("SourcePath: expected %q, got %q", "/src/file.txt", report.SourcePath)
	}
	if report.DestPath != "/dst/file.txt" {
		t.Errorf("DestPath: expected %q, got %q", "/dst/file.txt", report.DestPath)
	}
	if report.CopyMode != "parallel" {
		t.Errorf("CopyMode: expected %q, got %q", "parallel", report.CopyMode)
	}
}

// TestNewDistributeReport_LocalSourceEmptySourceHost verifies that empty
// source host is preserved (local machine indicator).
func TestNewDistributeReport_LocalSourceEmptySourceHost(t *testing.T) {
	report := NewDistributeReport("", "/local/file.txt", "/dst/file.txt", "parallel", nil)

	if report.SourceHost != "" {
		t.Errorf("local source: SourceHost should be empty string, got %q", report.SourceHost)
	}
}

// TestNewDistributeReport_EmptyDestPath verifies that an empty dest path is
// stored as-is (consumer interprets it as "same as source").
func TestNewDistributeReport_EmptyDestPath(t *testing.T) {
	report := NewDistributeReport("", "/src/file.txt", "", "parallel", nil)

	if report.DestPath != "" {
		t.Errorf("empty dest path should be preserved as empty, got %q", report.DestPath)
	}
}

// TestNewDistributeReport_NilResultsEmptyCounts verifies that nil results
// produce zero-value counts and an empty Hosts slice (not nil).
func TestNewDistributeReport_NilResultsEmptyCounts(t *testing.T) {
	report := NewDistributeReport("", "/src.txt", "/dst.txt", "parallel", nil)

	if report.TotalHosts != 0 {
		t.Errorf("TotalHosts: expected 0, got %d", report.TotalHosts)
	}
	if report.Succeeded != 0 {
		t.Errorf("Succeeded: expected 0, got %d", report.Succeeded)
	}
	if report.Failed != 0 {
		t.Errorf("Failed: expected 0, got %d", report.Failed)
	}
	if report.Hosts == nil {
		t.Error("Hosts should not be nil for nil results input")
	}
}

// TestNewDistributeReport_AllSucceed verifies counters and per-host Success
// flags when every transfer succeeds.
func TestNewDistributeReport_AllSucceed(t *testing.T) {
	results := []CopyResult{
		makeResult("host1.example.com", true, "", nil),
		makeResult("host2.example.com", true, "", nil),
		makeResult("host3.example.com", true, "", nil),
	}

	report := NewDistributeReport("", "/src.txt", "/dst.txt", "parallel", results)

	if report.TotalHosts != 3 {
		t.Errorf("TotalHosts: expected 3, got %d", report.TotalHosts)
	}
	if report.Succeeded != 3 {
		t.Errorf("Succeeded: expected 3, got %d", report.Succeeded)
	}
	if report.Failed != 0 {
		t.Errorf("Failed: expected 0, got %d", report.Failed)
	}
	for i, h := range report.Hosts {
		if !h.Success {
			t.Errorf("host[%d] (%s): expected Success=true", i, h.Host)
		}
		if h.Reason != "" {
			t.Errorf("host[%d] (%s): expected empty Reason on success, got %q", i, h.Host, h.Reason)
		}
	}
}

// TestNewDistributeReport_AllFail verifies counters and Reason population when
// every transfer fails.
func TestNewDistributeReport_AllFail(t *testing.T) {
	results := []CopyResult{
		makeResult("host1.example.com", false, "connection refused", errors.New("scp to host1: exit status 1")),
		makeResult("host2.example.com", false, "permission denied (publickey)", errors.New("scp to host2: exit status 1")),
	}

	report := NewDistributeReport("", "/src.txt", "/dst.txt", "parallel", results)

	if report.TotalHosts != 2 {
		t.Errorf("TotalHosts: expected 2, got %d", report.TotalHosts)
	}
	if report.Succeeded != 0 {
		t.Errorf("Succeeded: expected 0, got %d", report.Succeeded)
	}
	if report.Failed != 2 {
		t.Errorf("Failed: expected 2, got %d", report.Failed)
	}
	for i, h := range report.Hosts {
		if h.Success {
			t.Errorf("host[%d] (%s): expected Success=false", i, h.Host)
		}
		if h.Reason == "" {
			t.Errorf("host[%d] (%s): expected non-empty Reason on failure", i, h.Host)
		}
	}
}

// TestNewDistributeReport_PartialFailure verifies mixed success/failure counts.
func TestNewDistributeReport_PartialFailure(t *testing.T) {
	results := []CopyResult{
		makeResult("host1.example.com", true, "", nil),
		makeResult("host2.example.com", false, "host unreachable", errors.New("scp failed")),
		makeResult("host3.example.com", true, "", nil),
	}

	report := NewDistributeReport("", "/src.txt", "/dst.txt", "parallel", results)

	if report.Succeeded != 2 {
		t.Errorf("Succeeded: expected 2, got %d", report.Succeeded)
	}
	if report.Failed != 1 {
		t.Errorf("Failed: expected 1, got %d", report.Failed)
	}
}

// TestNewDistributeReport_OrderPreserved verifies that Hosts entries are in
// the same order as the input results.
func TestNewDistributeReport_OrderPreserved(t *testing.T) {
	hosts := []string{"alpha.example.com", "beta.example.com", "gamma.example.com"}
	results := make([]CopyResult, len(hosts))
	for i, h := range hosts {
		results[i] = makeResult(h, true, "", nil)
	}

	report := NewDistributeReport("", "/src.txt", "/dst.txt", "parallel", results)

	if len(report.Hosts) != len(hosts) {
		t.Fatalf("expected %d host entries, got %d", len(hosts), len(report.Hosts))
	}
	for i, want := range hosts {
		if report.Hosts[i].Host != want {
			t.Errorf("Hosts[%d].Host: expected %q, got %q", i, want, report.Hosts[i].Host)
		}
	}
}

// TestNewDistributeReport_HostUserAndPortPopulated verifies that User and Port
// from config.ResolvedHost are preserved in HostReport.
func TestNewDistributeReport_HostUserAndPortPopulated(t *testing.T) {
	results := []CopyResult{
		makeResultWithUser("myhost.example.com", "alice", 2222, true),
	}

	report := NewDistributeReport("", "/src.txt", "/dst.txt", "parallel", results)

	if len(report.Hosts) != 1 {
		t.Fatalf("expected 1 host entry, got %d", len(report.Hosts))
	}
	h := report.Hosts[0]
	if h.Host != "myhost.example.com" {
		t.Errorf("Host: expected %q, got %q", "myhost.example.com", h.Host)
	}
	if h.User != "alice" {
		t.Errorf("User: expected %q, got %q", "alice", h.User)
	}
	if h.Port != 2222 {
		t.Errorf("Port: expected 2222, got %d", h.Port)
	}
}

// TestNewDistributeReport_ReasonFromStderr verifies that the failure Reason is
// derived from scp stderr when it is non-empty (preferred over the error
// message).
func TestNewDistributeReport_ReasonFromStderr(t *testing.T) {
	stderrMsg := "ssh: connect to host host1.example.com port 22: Connection refused"
	results := []CopyResult{
		makeResult("host1.example.com", false, stderrMsg, errors.New("wrapped error")),
	}

	report := NewDistributeReport("", "/src.txt", "/dst.txt", "parallel", results)

	if report.Hosts[0].Reason != stderrMsg {
		t.Errorf("Reason should be the stderr message; got %q", report.Hosts[0].Reason)
	}
}

// TestNewDistributeReport_ReasonFromErrWhenStderrEmpty verifies that Reason
// falls back to the Go error message when stderr is empty.
func TestNewDistributeReport_ReasonFromErrWhenStderrEmpty(t *testing.T) {
	errMsg := "scp to host1.example.com: exit status 255 (stderr: )"
	results := []CopyResult{
		makeResult("host1.example.com", false, "", errors.New(errMsg)),
	}

	report := NewDistributeReport("", "/src.txt", "/dst.txt", "parallel", results)

	if report.Hosts[0].Reason != errMsg {
		t.Errorf("Reason should fall back to error message; got %q", report.Hosts[0].Reason)
	}
}

// TestNewDistributeReport_ReasonUnknownWhenBothEmpty verifies that Reason is
// set to a generic fallback when both stderr and Err are empty/nil.
func TestNewDistributeReport_ReasonUnknownWhenBothEmpty(t *testing.T) {
	results := []CopyResult{
		{Host: config.ResolvedHost{Host: "host1.example.com"}, Success: false},
	}

	report := NewDistributeReport("", "/src.txt", "/dst.txt", "parallel", results)

	if report.Hosts[0].Reason == "" {
		t.Error("Reason should not be empty even when stderr and Err are both absent")
	}
}

// TestNewDistributeReport_ReasonTrimsWhitespace verifies that the Reason is
// trimmed of leading/trailing whitespace derived from stderr.
func TestNewDistributeReport_ReasonTrimsWhitespace(t *testing.T) {
	results := []CopyResult{
		makeResult("host1.example.com", false, "  connection refused  \n", errors.New("err")),
	}

	report := NewDistributeReport("", "/src.txt", "/dst.txt", "parallel", results)

	reason := report.Hosts[0].Reason
	if strings.HasPrefix(reason, " ") || strings.HasSuffix(reason, " ") || strings.HasSuffix(reason, "\n") {
		t.Errorf("Reason should be trimmed of whitespace, got %q", reason)
	}
}

// ---------------------------------------------------------------------------
// FormatJSON
// ---------------------------------------------------------------------------

// TestFormatJSON_ValidJSON verifies that FormatJSON returns valid JSON that
// can be unmarshalled back to a DistributeReport.
func TestFormatJSON_ValidJSON(t *testing.T) {
	results := []CopyResult{
		makeResult("host1.example.com", true, "", nil),
		makeResult("host2.example.com", false, "connection refused", errors.New("scp failed")),
	}
	report := NewDistributeReport("src.example.com", "/src.txt", "/dst.txt", "parallel", results)

	data, err := report.FormatJSON()
	if err != nil {
		t.Fatalf("FormatJSON: unexpected error: %v", err)
	}

	var parsed DistributeReport
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("FormatJSON output is not valid JSON: %v\noutput:\n%s", err, string(data))
	}
}

// TestFormatJSON_RoundTrip verifies that serialising and deserialising a
// DistributeReport preserves all fields.
func TestFormatJSON_RoundTrip(t *testing.T) {
	results := []CopyResult{
		makeResultWithUser("host1.example.com", "alice", 2222, true),
		makeResult("host2.example.com", false, "permission denied", errors.New("scp to host2: exit status 1")),
	}
	original := NewDistributeReport("hub.example.com", "/src/file.txt", "/dst/file.txt", "hub-spoke", results)

	data, err := original.FormatJSON()
	if err != nil {
		t.Fatalf("FormatJSON: %v", err)
	}

	var parsed DistributeReport
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if parsed.SourceHost != original.SourceHost {
		t.Errorf("SourceHost round-trip: expected %q, got %q", original.SourceHost, parsed.SourceHost)
	}
	if parsed.SourcePath != original.SourcePath {
		t.Errorf("SourcePath round-trip: expected %q, got %q", original.SourcePath, parsed.SourcePath)
	}
	if parsed.DestPath != original.DestPath {
		t.Errorf("DestPath round-trip: expected %q, got %q", original.DestPath, parsed.DestPath)
	}
	if parsed.CopyMode != original.CopyMode {
		t.Errorf("CopyMode round-trip: expected %q, got %q", original.CopyMode, parsed.CopyMode)
	}
	if parsed.TotalHosts != original.TotalHosts {
		t.Errorf("TotalHosts round-trip: expected %d, got %d", original.TotalHosts, parsed.TotalHosts)
	}
	if parsed.Succeeded != original.Succeeded {
		t.Errorf("Succeeded round-trip: expected %d, got %d", original.Succeeded, parsed.Succeeded)
	}
	if parsed.Failed != original.Failed {
		t.Errorf("Failed round-trip: expected %d, got %d", original.Failed, parsed.Failed)
	}
	if len(parsed.Hosts) != len(original.Hosts) {
		t.Fatalf("Hosts length round-trip: expected %d, got %d", len(original.Hosts), len(parsed.Hosts))
	}
	for i := range original.Hosts {
		orig := original.Hosts[i]
		got := parsed.Hosts[i]
		if got.Host != orig.Host {
			t.Errorf("Hosts[%d].Host: expected %q, got %q", i, orig.Host, got.Host)
		}
		if got.User != orig.User {
			t.Errorf("Hosts[%d].User: expected %q, got %q", i, orig.User, got.User)
		}
		if got.Port != orig.Port {
			t.Errorf("Hosts[%d].Port: expected %d, got %d", i, orig.Port, got.Port)
		}
		if got.Success != orig.Success {
			t.Errorf("Hosts[%d].Success: expected %v, got %v", i, orig.Success, got.Success)
		}
		if got.Reason != orig.Reason {
			t.Errorf("Hosts[%d].Reason: expected %q, got %q", i, orig.Reason, got.Reason)
		}
	}
}

// TestFormatJSON_TrailingNewline verifies that FormatJSON appends a trailing
// newline for shell-friendly output.
func TestFormatJSON_TrailingNewline(t *testing.T) {
	report := NewDistributeReport("", "/src.txt", "/dst.txt", "parallel", nil)
	data, err := report.FormatJSON()
	if err != nil {
		t.Fatalf("FormatJSON: %v", err)
	}
	if len(data) == 0 || data[len(data)-1] != '\n' {
		t.Errorf("FormatJSON output should end with a newline; got %q", string(data[max(0, len(data)-5):]))
	}
}

// TestFormatJSON_SuccessHostHasNoReason verifies that successful hosts are
// serialised without a "reason" key (omitempty).
func TestFormatJSON_SuccessHostHasNoReason(t *testing.T) {
	results := []CopyResult{
		makeResult("host1.example.com", true, "", nil),
	}
	report := NewDistributeReport("", "/src.txt", "/dst.txt", "parallel", results)

	data, _ := report.FormatJSON()
	jsonStr := string(data)

	if strings.Contains(jsonStr, `"reason"`) {
		t.Errorf("successful host should not emit 'reason' field; got:\n%s", jsonStr)
	}
}

// TestFormatJSON_FailedHostHasReason verifies that failed hosts include a
// non-empty "reason" key in the JSON output.
func TestFormatJSON_FailedHostHasReason(t *testing.T) {
	results := []CopyResult{
		makeResult("host1.example.com", false, "connection refused", errors.New("scp failed")),
	}
	report := NewDistributeReport("", "/src.txt", "/dst.txt", "parallel", results)

	data, _ := report.FormatJSON()
	jsonStr := string(data)

	if !strings.Contains(jsonStr, `"reason"`) {
		t.Errorf("failed host should emit 'reason' field; got:\n%s", jsonStr)
	}
	if !strings.Contains(jsonStr, "connection refused") {
		t.Errorf("reason field should contain the failure message; got:\n%s", jsonStr)
	}
}

// TestFormatJSON_SuccessFieldPresent verifies that the "success" boolean field
// is present in the serialised JSON for both success and failure cases.
func TestFormatJSON_SuccessFieldPresent(t *testing.T) {
	results := []CopyResult{
		makeResult("host1.example.com", true, "", nil),
		makeResult("host2.example.com", false, "err", errors.New("err")),
	}
	report := NewDistributeReport("", "/src.txt", "/dst.txt", "parallel", results)

	data, _ := report.FormatJSON()
	jsonStr := string(data)

	if !strings.Contains(jsonStr, `"success": true`) {
		t.Errorf("JSON should contain '\"success\": true'; got:\n%s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"success": false`) {
		t.Errorf("JSON should contain '\"success\": false'; got:\n%s", jsonStr)
	}
}

// TestFormatJSON_SummaryCountsPresent verifies that top-level summary counts
// appear in the JSON output.
func TestFormatJSON_SummaryCountsPresent(t *testing.T) {
	results := []CopyResult{
		makeResult("host1.example.com", true, "", nil),
		makeResult("host2.example.com", false, "err", errors.New("err")),
	}
	report := NewDistributeReport("", "/src.txt", "/dst.txt", "parallel", results)

	data, _ := report.FormatJSON()
	jsonStr := string(data)

	for _, field := range []string{"total_hosts", "succeeded", "failed"} {
		if !strings.Contains(jsonStr, `"`+field+`"`) {
			t.Errorf("JSON should contain field %q; got:\n%s", field, jsonStr)
		}
	}
}

// ---------------------------------------------------------------------------
// FormatText
// ---------------------------------------------------------------------------

// TestFormatText_ContainsSummaryLine verifies that the text report begins with
// a human-readable summary line showing succeeded/total counts.
func TestFormatText_ContainsSummaryLine(t *testing.T) {
	results := []CopyResult{
		makeResult("host1.example.com", true, "", nil),
		makeResult("host2.example.com", false, "connection refused", errors.New("scp failed")),
	}
	report := NewDistributeReport("", "/src.txt", "/dst.txt", "parallel", results)

	text := report.FormatText()

	if !strings.Contains(text, "1/2") {
		t.Errorf("FormatText should show '1/2' succeeded; got:\n%s", text)
	}
}

// TestFormatText_SuccessHostMarkedWithCheckmark verifies that successful hosts
// are prefixed with ✓ in the text output.
func TestFormatText_SuccessHostMarkedWithCheckmark(t *testing.T) {
	results := []CopyResult{
		makeResult("host1.example.com", true, "", nil),
	}
	report := NewDistributeReport("", "/src.txt", "/dst.txt", "parallel", results)

	text := report.FormatText()

	if !strings.Contains(text, "✓") {
		t.Errorf("FormatText should prefix successful hosts with ✓; got:\n%s", text)
	}
}

// TestFormatText_FailedHostMarkedWithX verifies that failed hosts are prefixed
// with ✗ in the text output.
func TestFormatText_FailedHostMarkedWithX(t *testing.T) {
	results := []CopyResult{
		makeResult("host2.example.com", false, "timeout", errors.New("scp failed")),
	}
	report := NewDistributeReport("", "/src.txt", "/dst.txt", "parallel", results)

	text := report.FormatText()

	if !strings.Contains(text, "✗") {
		t.Errorf("FormatText should prefix failed hosts with ✗; got:\n%s", text)
	}
}

// TestFormatText_FailedHostIncludesReason verifies that the failure reason is
// shown in the text output for failed hosts.
func TestFormatText_FailedHostIncludesReason(t *testing.T) {
	results := []CopyResult{
		makeResult("host2.example.com", false, "connection refused", errors.New("scp failed")),
	}
	report := NewDistributeReport("", "/src.txt", "/dst.txt", "parallel", results)

	text := report.FormatText()

	if !strings.Contains(text, "connection refused") {
		t.Errorf("FormatText should include failure reason; got:\n%s", text)
	}
}

// TestFormatText_AllSucceedNoFailedCount verifies that when all transfers
// succeed the text output does not mention a "failed" count.
func TestFormatText_AllSucceedNoFailedCount(t *testing.T) {
	results := []CopyResult{
		makeResult("host1.example.com", true, "", nil),
		makeResult("host2.example.com", true, "", nil),
	}
	report := NewDistributeReport("", "/src.txt", "/dst.txt", "parallel", results)

	text := report.FormatText()

	if strings.Contains(text, "failed") {
		t.Errorf("FormatText should not mention 'failed' when all succeed; got:\n%s", text)
	}
}

// ---------------------------------------------------------------------------
// hostLabel helper
// ---------------------------------------------------------------------------

// TestHostLabel_HostOnly verifies that hostLabel returns just the hostname when
// user and port are absent.
func TestHostLabel_HostOnly(t *testing.T) {
	h := HostReport{Host: "myhost.example.com"}
	label := hostLabel(h)
	if label != "myhost.example.com" {
		t.Errorf("expected %q, got %q", "myhost.example.com", label)
	}
}

// TestHostLabel_WithUser verifies that user@ is prepended when User is set.
func TestHostLabel_WithUser(t *testing.T) {
	h := HostReport{Host: "myhost.example.com", User: "alice"}
	label := hostLabel(h)
	if label != "alice@myhost.example.com" {
		t.Errorf("expected %q, got %q", "alice@myhost.example.com", label)
	}
}

// TestHostLabel_WithPort verifies that :port is appended when Port is non-zero.
func TestHostLabel_WithPort(t *testing.T) {
	h := HostReport{Host: "myhost.example.com", Port: 2222}
	label := hostLabel(h)
	if label != "myhost.example.com:2222" {
		t.Errorf("expected %q, got %q", "myhost.example.com:2222", label)
	}
}

// TestHostLabel_WithUserAndPort verifies that user@ prefix and :port suffix
// are both applied when both fields are set.
func TestHostLabel_WithUserAndPort(t *testing.T) {
	h := HostReport{Host: "myhost.example.com", User: "alice", Port: 2222}
	label := hostLabel(h)
	if label != "alice@myhost.example.com:2222" {
		t.Errorf("expected %q, got %q", "alice@myhost.example.com:2222", label)
	}
}

// ---------------------------------------------------------------------------
// max helper (Go 1.20 does not have built-in max for ints)
// ---------------------------------------------------------------------------

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
