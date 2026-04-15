package cmd

import (
	"os"
	"os/exec"
	"os/user"
	"strings"
)

type runMeta struct {
	Host      string
	User      string
	Dir       string
	GitBranch string
}

func collectMeta() runMeta {
	m := runMeta{}
	m.Host, _ = os.Hostname()
	m.Dir, _ = os.Getwd()
	if u, err := user.Current(); err == nil {
		m.User = u.Username
	}
	if out, err := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD").Output(); err == nil {
		m.GitBranch = strings.TrimSpace(string(out))
	}
	return m
}
