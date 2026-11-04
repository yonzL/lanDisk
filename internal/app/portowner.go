package app

import (
	"strconv"
	"strings"
)

// parseListeningPID finds the process id listening on port in `netstat -ano`
// output. Kept separate from the Windows-only exec so it can be unit tested on
// any platform.
func parseListeningPID(netstatOutput string, port int) (int, bool) {
	wantLocal := ":" + strconv.Itoa(port)
	for _, line := range strings.Split(netstatOutput, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		// Proto  Local Address  Foreign Address  State  PID
		if !strings.EqualFold(fields[0], "TCP") && !strings.EqualFold(fields[0], "TCPv6") {
			continue
		}
		if !strings.HasSuffix(fields[1], wantLocal) {
			continue
		}
		if len(fields) >= 5 && !strings.EqualFold(fields[3], "LISTENING") {
			continue
		}
		pid, err := strconv.Atoi(fields[len(fields)-1])
		if err != nil || pid <= 0 {
			continue
		}
		return pid, true
	}
	return 0, false
}
