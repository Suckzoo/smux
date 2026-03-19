package config

import (
	"os"

	sshconfig "github.com/kevinburke/ssh_config"
)

// sshCfg is the parsed ~/.ssh/config, loaded once on demand.
var sshCfg *sshconfig.Config

func loadSSHConfig() {
	if sshCfg != nil {
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	f, err := os.Open(home + "/.ssh/config")
	if err != nil {
		return
	}
	defer f.Close()
	sshCfg, _ = sshconfig.Decode(f)
}

// SSHConfigGet returns the value of an SSH config keyword for a given host,
// falling back to the provided default if not found.
func SSHConfigGet(host, keyword, fallback string) string {
	loadSSHConfig()
	if sshCfg == nil {
		return fallback
	}
	val, err := sshCfg.Get(host, keyword)
	if err != nil || val == "" {
		return fallback
	}
	return val
}
