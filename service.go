package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// installService writes a user-level launchd plist (macOS) or systemd
// user unit (Linux) so the yullu server auto-starts on login. Returns
// nil on Windows with a clear message rather than failing — Windows
// service install isn't supported yet.
//
// Asks the user before doing anything: this writes a persistent file and
// kicks off a daemon, both of which deserve consent. `yullu install` calls
// this from its tail with `--yes` to skip the prompt for scripted setups.
func installService(autoYes bool) error {
	bin, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate yullu binary: %w", err)
	}
	switch runtime.GOOS {
	case "darwin":
		return installLaunchAgent(bin, autoYes)
	case "linux":
		return installSystemdUser(bin, autoYes)
	default:
		fmt.Printf("service: auto-start not yet supported on %s; run `yullu` manually or set it up via your platform's startup mechanism.\n", runtime.GOOS)
		return nil
	}
}

// uninstallService is the reverse — used by `yullu uninstall`.
func uninstallService() error {
	switch runtime.GOOS {
	case "darwin":
		return uninstallLaunchAgent()
	case "linux":
		return uninstallSystemdUser()
	default:
		return nil
	}
}

// ---------- macOS launchd ----------

const launchAgentLabel = "ai.yullu.server"

func launchAgentPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", launchAgentLabel+".plist")
}

func installLaunchAgent(bin string, autoYes bool) error {
	path := launchAgentPath()
	if !autoYes {
		fmt.Printf("service: install a launchd agent at %s so yullu auto-starts on login? [y/N] ", path)
		if !readYes() {
			fmt.Println("service: skipped.")
			return nil
		}
	}
	logDir, _ := os.UserHomeDir()
	logDir = filepath.Join(logDir, "Library", "Logs", "yullu")
	_ = os.MkdirAll(logDir, 0o755)

	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>%s</string>
    <key>ProgramArguments</key>
    <array>
        <string>%s</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>%s/server.log</string>
    <key>StandardErrorPath</key>
    <string>%s/server.err.log</string>
</dict>
</plist>
`, launchAgentLabel, bin, logDir, logDir)

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(plist), 0o644); err != nil {
		return err
	}
	fmt.Printf("service: wrote %s\n", path)

	// Try to load it. launchctl bootstrap is the modern verb; load works
	// on older macOS too. If both fail, the user can launch manually.
	uid := os.Getuid()
	domain := fmt.Sprintf("gui/%d", uid)
	if out, err := exec.Command("launchctl", "bootstrap", domain, path).CombinedOutput(); err != nil {
		// "service already loaded" is a soft error — try unload+bootstrap
		// once. Otherwise fall back to the legacy load command.
		_ = exec.Command("launchctl", "bootout", domain+"/"+launchAgentLabel).Run()
		if out2, err2 := exec.Command("launchctl", "bootstrap", domain, path).CombinedOutput(); err2 != nil {
			if loadOut, loadErr := exec.Command("launchctl", "load", path).CombinedOutput(); loadErr != nil {
				fmt.Printf("service: writeable but couldn't load: %s / %s\n",
					string(out2), string(loadOut))
				fmt.Printf("service: try `launchctl load %s` manually\n", path)
				return nil
			}
		}
	} else if len(out) > 0 {
		fmt.Printf("service: launchctl: %s\n", out)
	}
	fmt.Printf("service: yullu running at http://localhost:47823 (logs in %s)\n", logDir)
	return nil
}

func uninstallLaunchAgent() error {
	path := launchAgentPath()
	if !fileExists(path) {
		return nil
	}
	uid := os.Getuid()
	domain := fmt.Sprintf("gui/%d", uid)
	_ = exec.Command("launchctl", "bootout", domain+"/"+launchAgentLabel).Run()
	if err := os.Remove(path); err != nil {
		return err
	}
	fmt.Printf("service: removed %s\n", path)
	return nil
}

// ---------- Linux systemd user units ----------

func systemdUnitPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "systemd", "user", "yullu.service")
}

func installSystemdUser(bin string, autoYes bool) error {
	path := systemdUnitPath()
	if !autoYes {
		fmt.Printf("service: install a systemd user unit at %s so yullu auto-starts on login? [y/N] ", path)
		if !readYes() {
			fmt.Println("service: skipped.")
			return nil
		}
	}
	unit := fmt.Sprintf(`[Unit]
Description=Yul'lu - persistent memory for AI coding assistants
After=network-online.target

[Service]
Type=simple
ExecStart=%s
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
`, bin)

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(unit), 0o644); err != nil {
		return err
	}
	fmt.Printf("service: wrote %s\n", path)

	for _, args := range [][]string{
		{"--user", "daemon-reload"},
		{"--user", "enable", "--now", "yullu.service"},
	} {
		if out, err := exec.Command("systemctl", args...).CombinedOutput(); err != nil {
			fmt.Printf("service: `systemctl %s` failed: %s\n", args, string(out))
			fmt.Println("service: enable manually with `systemctl --user enable --now yullu.service`")
			return nil
		}
	}
	fmt.Println("service: yullu running at http://localhost:47823 (`journalctl --user -u yullu.service` for logs)")
	return nil
}

func uninstallSystemdUser() error {
	path := systemdUnitPath()
	if !fileExists(path) {
		return nil
	}
	_ = exec.Command("systemctl", "--user", "disable", "--now", "yullu.service").Run()
	if err := os.Remove(path); err != nil {
		return err
	}
	_ = exec.Command("systemctl", "--user", "daemon-reload").Run()
	fmt.Printf("service: removed %s\n", path)
	return nil
}

// ---------- helpers ----------

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// readYes returns true if the user answers yes/y/Y at the prompt.
// Anything else (including EOF, which happens under `yullu install` in a
// piped/non-interactive shell) is "no".
func readYes() bool {
	var resp string
	_, _ = fmt.Scanln(&resp)
	switch resp {
	case "y", "Y", "yes", "YES", "Yes":
		return true
	}
	return false
}
