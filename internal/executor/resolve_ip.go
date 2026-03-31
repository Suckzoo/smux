package executor

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"strings"

	"github.com/Suckzoo/smux/internal/config"
	"github.com/Suckzoo/smux/internal/filetree"
)

// ResolvePrivateIP determines the private/internal IP address for a host.
// This is used exclusively for inter-host communication (spoke-pull), never
// for local→host connections.
//
// Resolution order:
//  1. If host.InternalIP is set (explicit per-host override), return it.
//  2. If host.InternalIPBase is set (template like "10.0.0.{100+$index}"),
//     evaluate the template with host.IPBaseIndex and return the result.
//  3. If host.InternalCIDR is set, SSH into the host, run "ip -4 -o addr show",
//     and return the first address within the CIDR.
//  4. Fall back to host.RemoteReachableAddress() (SSH config Hostname or alias).
func ResolvePrivateIP(ctx context.Context, host config.ResolvedHost) (string, error) {
	if host.InternalIP != "" {
		return host.InternalIP, nil
	}
	if host.InternalIPBase != "" {
		return config.ResolveIPBasePublic(host.InternalIPBase, host.IPBaseIndex)
	}
	if host.InternalCIDR == "" {
		return host.RemoteReachableAddress(), nil
	}

	_, ipNet, err := net.ParseCIDR(host.InternalCIDR)
	if err != nil {
		return "", fmt.Errorf("invalid CIDR %q: %w", host.InternalCIDR, err)
	}

	args := filetree.BuildSSHArgsForHost(host)
	args = append(args, "--", "ip -4 -o addr show")

	cmd := exec.CommandContext(ctx, "ssh", args...)
	out, err := cmd.Output()
	if err != nil {
		stderr := ""
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = strings.TrimSpace(string(ee.Stderr))
		}
		return "", fmt.Errorf("ssh ip-addr on %s: %w (stderr: %s)", host.Host, err, stderr)
	}

	for _, ip := range parseIPv4Addresses(string(out)) {
		if ipNet.Contains(net.ParseIP(ip)) {
			return ip, nil
		}
	}

	return "", fmt.Errorf("no IP matching CIDR %s found on %s", host.InternalCIDR, host.Host)
}

// parseIPv4Addresses extracts IPv4 addresses from the output of
// "ip -4 -o addr show". Each line has the form:
//
//	2: eth0    inet 10.61.2.36/24 brd 10.61.2.255 scope global eth0\ ...
//
// The function finds the field immediately after "inet" and strips the
// /prefix to return the bare IP.
func parseIPv4Addresses(output string) []string {
	var ips []string
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		for i, f := range fields {
			if f == "inet" && i+1 < len(fields) {
				addr := fields[i+1]
				if slash := strings.IndexByte(addr, '/'); slash != -1 {
					addr = addr[:slash]
				}
				ips = append(ips, addr)
				break
			}
		}
	}
	return ips
}
