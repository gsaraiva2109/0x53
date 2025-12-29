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
	// 4. Update /etc/resolv.conf to point to 127.0.0.1
	// In "Coexistence", systemd-resolved might still manage this file.
	// We force it to be a symlink to our controlled setup OR just overwrite.
	// For "Infra-First" reliability: Overwrite with 127.0.0.1 is simplest.
	// But first, backup the original link/file.
	if err := os.WriteFile("/etc/resolv.conf.orig.sinkhole", readResolvConf(), 0644); err == nil {
		// Only write if backup succeeded
		content := "# Managed by Go-Sinkhole\nnameserver 127.0.0.1\noptions edns0 trust-ad\n"
		os.WriteFile("/etc/resolv.conf", []byte(content), 0644)
	}

	return nil
}

func (l *LinuxConfigurator) RestoreDNS() error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("root privileges required")
	}
	fmt.Println("Restoring systemd-resolved configuration...")

	// 1. Restore resolv.conf
	if orig, err := os.ReadFile("/etc/resolv.conf.orig.sinkhole"); err == nil {
		os.WriteFile("/etc/resolv.conf", orig, 0644)
		os.Remove("/etc/resolv.conf.orig.sinkhole")
	}

	// 2. Restore resolved.conf
	if _, err := os.Stat(l.backupConfPath); err == nil {
		if err := copyFile(l.backupConfPath, l.resolvedConfPath); err != nil {
			return err
		}
		os.Remove(l.backupConfPath)
	}

	// 3. Restart Service
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

	for _, line := range lines {
		trim := strings.TrimSpace(line)
		if trim == "[Resolve]" {
			inResolve = true
			newLines = append(newLines, line)
			// Ensure we set StubListener here if not present later? 
			// Simpler: Just append to end of file if not careful, but let's try to replace.
			continue
		}
		
		if inResolve && strings.HasPrefix(trim, "DNSStubListener=") {
			newLines = append(newLines, "DNSStubListener=no")
			stubFound = true
		} else {
			newLines = append(newLines, line)
		}
	}

	if !stubFound {
		// Append to [Resolve] section or end
		// Simple approach: Just append to end, systemd usually parses last entry wins or merges.
		newLines = append(newLines, "")
		newLines = append(newLines, "[Resolve]")
		newLines = append(newLines, "DNSStubListener=no")
	}

	output := strings.Join(newLines, "\n")
	return os.WriteFile(l.resolvedConfPath, []byte(output), 0644)
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
