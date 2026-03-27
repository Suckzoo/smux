// Package executor — machine-parsable per-host distribution report.
//
// DistributeReport and its JSON serialisation provide a structured, machine-
// readable summary of the outcome of a distribute-file operation.  Each host
// that participated in the transfer is represented by one HostReport entry
// that captures whether the copy succeeded and, on failure, a human-readable
// reason extracted from the scp stderr / error message.
//
// Intended use:
//
//	results := RunParallel(ctx, src, srcPath, dests, dstPath, kp)
//	report := NewDistributeReport("", srcPath, dstPath, "parallel", results)
//	data, _ := report.FormatJSON()
//	fmt.Fprintln(os.Stderr, string(data))
package executor

import (
	"encoding/json"
	"fmt"
	"strings"
)

// HostReport holds the outcome for a single destination host in a distribute
// operation.  The JSON tags use snake_case to match conventional CLI tooling
// expectations.
type HostReport struct {
	// Host is the SSH hostname or IP address of the destination.
	Host string `json:"host"`

	// User is the remote username, or empty when the default SSH user is used.
	User string `json:"user,omitempty"`

	// Port is the SSH port, or 0 when the standard port (22) is used.
	Port int `json:"port,omitempty"`

	// Success is true when scp exited with code 0.
	Success bool `json:"success"`

	// Reason is a non-empty human-readable explanation when Success is false.
	// It is derived from scp stderr output, falling back to the Go error
	// message when stderr is empty.
	Reason string `json:"reason,omitempty"`
}

// DistributeReport is the final machine-parsable summary of a distribute-file
// operation.  It aggregates per-host outcomes together with the operation
// parameters, enabling callers to parse the report without retaining the
// original context.
type DistributeReport struct {
	// SourceHost is the SSH alias of the host that held the source file.
	// Empty string indicates the local machine.
	SourceHost string `json:"source_host"`

	// SourcePath is the filesystem path of the distributed file on the source.
	SourcePath string `json:"source_path"`

	// DestPath is the filesystem path on each destination host.
	// An empty string indicates that the same path as SourcePath was used.
	DestPath string `json:"dest_path"`

	// CopyMode identifies the distribution strategy: "parallel" for direct
	// parallel copy or "hub-spoke" for hub-and-spoke.
	CopyMode string `json:"copy_mode"`

	// Hosts contains one entry per destination host, in the same order that
	// results were provided to NewDistributeReport.
	Hosts []HostReport `json:"hosts"`

	// TotalHosts is the total number of destination hosts targeted.
	TotalHosts int `json:"total_hosts"`

	// Succeeded is the number of hosts that received the file successfully.
	Succeeded int `json:"succeeded"`

	// Failed is the number of hosts whose transfer failed.
	Failed int `json:"failed"`
}

// NewDistributeReport constructs a DistributeReport from the slice of
// CopyResult values returned by RunParallel or FanOutFromHub.
//
// sourceHost, sourcePath, destPath, and copyMode are operation metadata stored
// verbatim in the report; they are not validated.  An empty destPath is stored
// as-is (the consumer can interpret it as "same as source").
//
// The order of entries in DistributeReport.Hosts matches the order of results.
func NewDistributeReport(
	sourceHost, sourcePath, destPath, copyMode string,
	results []CopyResult,
) DistributeReport {
	hosts := make([]HostReport, 0, len(results))
	succeeded := 0
	failed := 0

	for _, r := range results {
		hr := HostReport{
			Host:    r.Host.Host,
			User:    r.Host.User,
			Port:    r.Host.Port,
			Success: r.Success,
		}

		if !r.Success {
			hr.Reason = extractReason(r)
			failed++
		} else {
			succeeded++
		}

		hosts = append(hosts, hr)
	}

	return DistributeReport{
		SourceHost: sourceHost,
		SourcePath: sourcePath,
		DestPath:   destPath,
		CopyMode:   copyMode,
		Hosts:      hosts,
		TotalHosts: len(results),
		Succeeded:  succeeded,
		Failed:     failed,
	}
}

// FormatJSON serialises the DistributeReport as indented JSON.
//
// The returned bytes are suitable for writing directly to a file, a log
// stream, or standard error for machine consumption.  The output is always
// a complete, valid JSON object followed by a newline.
func (r DistributeReport) FormatJSON() ([]byte, error) {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal distribute report: %w", err)
	}
	// Append a trailing newline for shell-friendly output.
	data = append(data, '\n')
	return data, nil
}

// FormatText renders the DistributeReport as a human-readable multi-line
// summary.  Each host is listed with a ✓ or ✗ prefix, indented under a
// header that shows the overall success rate.
//
// This is a convenience for TUI rendering; the canonical machine-parsable
// format is FormatJSON.
func (r DistributeReport) FormatText() string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf(
		"Distribution complete: %d/%d succeeded",
		r.Succeeded, r.TotalHosts,
	))
	if r.Failed > 0 {
		sb.WriteString(fmt.Sprintf(", %d failed", r.Failed))
	}
	sb.WriteString("\n")

	for _, h := range r.Hosts {
		if h.Success {
			sb.WriteString(fmt.Sprintf("  ✓ %s\n", hostLabel(h)))
		} else {
			sb.WriteString(fmt.Sprintf("  ✗ %s — %s\n", hostLabel(h), h.Reason))
		}
	}

	return sb.String()
}

// hostLabel returns a compact [user@]host[:port] string for display purposes.
func hostLabel(h HostReport) string {
	label := h.Host
	if h.User != "" {
		label = h.User + "@" + h.Host
	}
	if h.Port != 0 {
		label = fmt.Sprintf("%s:%d", label, h.Port)
	}
	return label
}

// extractReason derives a short failure reason string from a CopyResult.
//
// Priority order:
//  1. Non-empty trimmed stderr from scp (most specific).
//  2. The Go error message from CopyResult.Err.
//  3. A generic fallback when both are empty.
func extractReason(r CopyResult) string {
	if trimmed := strings.TrimSpace(r.Stderr); trimmed != "" {
		return trimmed
	}
	if r.Err != nil {
		return r.Err.Error()
	}
	return "unknown error"
}
