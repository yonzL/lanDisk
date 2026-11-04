//go:build windows

package app

import (
	"context"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// PortOwnerHint names the process holding a port, so a failed start says "被
// 占用" with the actual owner instead of leaving the operator to guess.
// Returns "" when the owner cannot be determined.
func PortOwnerHint(addr string) string {
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return ""
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return ""
	}
	pid, ok := parseListeningPID(netstat(), port)
	if !ok {
		return ""
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "tasklist", "/FI", "PID eq "+strconv.Itoa(pid), "/FO", "CSV", "/NH").Output()
	if err != nil {
		return "，占用 PID " + strconv.Itoa(pid)
	}
	name := firstCSVField(string(out))
	if name == "" {
		return "，占用 PID " + strconv.Itoa(pid)
	}
	return "，占用进程 " + name + " (PID " + strconv.Itoa(pid) + ")"
}

func netstat() string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "netstat", "-ano").Output()
	if err != nil {
		return ""
	}
	return string(out)
}

func firstCSVField(out string) string {
	line := strings.TrimSpace(strings.SplitN(out, "\n", 2)[0])
	line = strings.TrimPrefix(line, "\"")
	if i := strings.Index(line, "\""); i > 0 {
		return line[:i]
	}
	return strings.TrimSpace(line)
}
