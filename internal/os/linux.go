package os

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"
)

type LinuxConfigurator struct {
	resolvedConfPath string
	backupConfPath   string
}

func NewLinuxConfigurator() *LinuxConfigurator {
	return &LinuxConfigurator{
		resolvedConfPath: "/etc/systemd/resolved.conf",
		backupConfPath:   "/etc/systemd/resolved.conf.backup.sinkhole",
	}
}

// UnlockPort disables the system stub listener to free port 53.
func (l *LinuxConfigurator) UnlockPort() error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("root privileges required")
	}

	fmt.Println("Applying Safer Coexistence Strategy (systemd-resolved)...")

	// 1. Backup resolved.conf
	if err := l.backup(); err != nil {
		return fmt.Errorf("failed to backup resolved.conf: %w", err)
	}

	// 2. Modify resolved.conf to disable StubListener
	// We read the file and ensure [Resolve] section has DNSStubListener=no
	if err := l.patchResolvedConf(); err != nil {
		return fmt.Errorf("failed to patch resolved.conf: %w", err)
	}

	// 3. Restart systemd-resolved to free port 53
	fmt.Println("Restarting systemd-resolved to apply changes...")
	if err := exec.Command("systemctl", "restart", "systemd-resolved").Run(); err != nil {
		return fmt.Errorf("failed to restart systemd-resolved: %w", err)
	}
	
	// 4. Wait for Port 53 to be free
	fmt.Println("Waiting for Port 53 to be released...")
	for i := 0; i < 10; i++ {
		// Try to bind strictly to verify availability
		// We use "udp" because DNS uses UDP primarily
		l, err := net.ListenPacket("udp", ":53")
		if err == nil {
			l.Close()
			return nil // Port is free!
		}
		time.Sleep(500 * time.Millisecond)
	}

	return fmt.Errorf("timeout waiting for port 53 to be free (is another service using it?)")
}

// SetupDNS points the system to localhost.
func (l *LinuxConfigurator) SetupDNS() error {
	const resolvPath = "/etc/resolv.conf"

	// Detect whether resolv.conf is a symlink.
	linfo, err := os.Lstat(resolvPath)
	if err == nil && linfo.Mode()&os.ModeSymlink != 0 {
		// Save symlink target so we can restore it later.
		target, err := os.Readlink(resolvPath)
		if err != nil {
			return fmt.Errorf("readlink %s: %w", resolvPath, err)
		}
		if err := os.WriteFile(resolvPath+".sinkhole-link", []byte(target), 0644); err != nil {
			return fmt.Errorf("save symlink target: %w", err)
		}
		if err := os.Remove(resolvPath); err != nil {
			return fmt.Errorf("remove symlink %s: %w", resolvPath, err)
		}
	} else {
		// Regular file — back up content.
		if content := readResolvConf(); len(content) > 0 {
			os.WriteFile(resolvPath+".orig.sinkhole", content, 0644)
		}
	}

	content := "# Managed by 0x53\nnameserver 127.0.0.1\noptions edns0 trust-ad\n"
	if err := os.WriteFile(resolvPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("write %s: %w", resolvPath, err)
	}
	return nil
}

func (l *LinuxConfigurator) RestoreDNS() error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("root privileges required")
	}
	fmt.Println("Restoring systemd-resolved configuration...")

	const resolvPath = "/etc/resolv.conf"

	// Restore resolv.conf: re-create symlink if we saved one, else restore content.
	if linkTarget, err := os.ReadFile(resolvPath + ".sinkhole-link"); err == nil {
		os.Remove(resolvPath)
		target := strings.TrimSpace(string(linkTarget))
		if err := os.Symlink(target, resolvPath); err != nil {
			return fmt.Errorf("restore symlink %s -> %s: %w", resolvPath, target, err)
		}
		os.Remove(resolvPath + ".sinkhole-link")
	} else if orig, err := os.ReadFile(resolvPath + ".orig.sinkhole"); err == nil {
		if err := os.WriteFile(resolvPath, orig, 0644); err != nil {
			return fmt.Errorf("restore %s: %w", resolvPath, err)
		}
		os.Remove(resolvPath + ".orig.sinkhole")
	}

	// Restore resolved.conf and restart service.
	if _, err := os.Stat(l.backupConfPath); err == nil {
		if err := copyFile(l.backupConfPath, l.resolvedConfPath); err != nil {
			return err
		}
		os.Remove(l.backupConfPath)
	}

	fmt.Println("Restarting systemd-resolved...")
	return exec.Command("systemctl", "restart", "systemd-resolved").Run()
}

func (l *LinuxConfigurator) backup() error {
	if _, err := os.Stat(l.backupConfPath); err == nil {
		return nil // Already exists
	}
	return copyFile(l.resolvedConfPath, l.backupConfPath)
}

func (l *LinuxConfigurator) patchResolvedConf() error {
	input, err := os.ReadFile(l.resolvedConfPath)
	if err != nil {
		return err
	}

	lines := strings.Split(string(input), "\n")
	var newLines []string
	inResolve := false
	stubFound := false
	hasResolveSection := false

	for _, line := range lines {
		trim := strings.TrimSpace(line)

		if trim == "[Resolve]" {
			inResolve = true
			hasResolveSection = true
			newLines = append(newLines, line)
			continue
		}

		// Entering a new section — inject before leaving [Resolve] if needed.
		if strings.HasPrefix(trim, "[") && inResolve && !stubFound {
			newLines = append(newLines, "DNSStubListener=no")
			stubFound = true
			inResolve = false
		} else if strings.HasPrefix(trim, "[") {
			inResolve = false
		}

		if inResolve && strings.HasPrefix(trim, "DNSStubListener=") {
			newLines = append(newLines, "DNSStubListener=no")
			stubFound = true
		} else {
			newLines = append(newLines, line)
		}
	}

	// Still in [Resolve] at EOF (no following section).
	if inResolve && !stubFound {
		newLines = append(newLines, "DNSStubListener=no")
		stubFound = true
	}

	// No [Resolve] section found at all.
	if !hasResolveSection {
		newLines = append(newLines, "")
		newLines = append(newLines, "[Resolve]")
		newLines = append(newLines, "DNSStubListener=no")
	}

	// Also remove the stale TODO comment if present.
	var cleaned []string
	for _, l := range newLines {
		if strings.Contains(l, "Ensure we set StubListener here if not present later?") {
			continue
		}
		cleaned = append(cleaned, l)
	}

	return os.WriteFile(l.resolvedConfPath, []byte(strings.Join(cleaned, "\n")), 0644)
}

func copyFile(src, dst string) error {
	input, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, input, 0644)
}

func readResolvConf() []byte {
	// Best effort read
	b, _ := os.ReadFile("/etc/resolv.conf")
	return b
}
