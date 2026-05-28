//go:build linux

package cmd

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

// captureUpstreamCommand returns the command line of the upstream process(es)
// in `cmd | spout`. Linux-only and best-effort; returns "" when nothing can
// be determined.
//
// Approach: shell pipelines run all their members in one process group
// distinct from the shell itself, and every member shares the same parent
// (the shell). So spout's "upstream" is any process that shares spout's
// PGID and PPID. /proc/<pid>/stat is world-readable, so this works under
// Ubuntu's default kernel.yama.ptrace_scope=1 (which blocks /proc/<pid>/fd
// of sibling processes and rules out an inode-match approach).
//
// For multi-stage pipelines (`a | b | spout`) we return all upstream stages
// joined by " | " in PID order.
func captureUpstreamCommand() string {
	myPgid, err := syscall.Getpgid(0)
	if err != nil {
		return ""
	}
	self := os.Getpid()
	myPpid := os.Getppid()

	entries, err := os.ReadDir("/proc")
	if err != nil {
		return ""
	}
	type sib struct {
		pid int
		cmd string
	}
	var sibs []sib
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == self {
			continue
		}
		statData, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if err != nil {
			continue
		}
		ppid, pgid, ok := parseStatPpidPgid(string(statData))
		if !ok || pgid != myPgid || ppid != myPpid {
			continue
		}
		data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
		if err != nil {
			continue
		}
		cmd := strings.TrimSpace(strings.ReplaceAll(string(data), "\x00", " "))
		if cmd == "" {
			continue
		}
		sibs = append(sibs, sib{pid, cmd})
	}
	if len(sibs) == 0 {
		return ""
	}
	sort.Slice(sibs, func(i, j int) bool { return sibs[i].pid < sibs[j].pid })
	parts := make([]string, len(sibs))
	for i, s := range sibs {
		parts[i] = s.cmd
	}
	return strings.Join(parts, " | ")
}

// parseStatPpidPgid parses the `ppid` and `pgrp` fields out of /proc/<pid>/stat.
// Format: `pid (comm-may-have-spaces-and-)) state ppid pgrp ...`
func parseStatPpidPgid(s string) (ppid, pgid int, ok bool) {
	rparen := strings.LastIndex(s, ")")
	if rparen < 0 {
		return 0, 0, false
	}
	fields := strings.Fields(s[rparen+1:])
	if len(fields) < 3 {
		return 0, 0, false
	}
	// fields[0] = state, [1] = ppid, [2] = pgrp.
	ppid, err := strconv.Atoi(fields[1])
	if err != nil {
		return 0, 0, false
	}
	pgid, err = strconv.Atoi(fields[2])
	if err != nil {
		return 0, 0, false
	}
	return ppid, pgid, true
}
