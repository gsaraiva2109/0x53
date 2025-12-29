package os

import (
	"fmt"
	"os/exec"
	"strings"
)

type WindowsConfigurator struct {
	interfaceName string
}

func NewWindowsConfigurator() *WindowsConfigurator {
	return &WindowsConfigurator{}
}

func (w *WindowsConfigurator) UnlockPort() error {
	// Windows typically doesn't bind 0.0.0.0:53 unless DNS Server role is installed.
	// We assume it's free or user has stopped the service.
	return nil
}

func (w *WindowsConfigurator) SetupDNS() error {
	// 1. Detect active interface (Gateway)
	// This is tricky in pure Go without calls.
	// For MVP, we default to "Wi-Fi" or "Ethernet".
	// Better: Parse 'netsh interface ip show config' or 'route print'.
	
	ifName, err := w.detectInterface()
	if err != nil {
		return err
	}
	w.interfaceName = ifName

	fmt.Printf("Detected Interface: %s. Setting DNS to 127.0.0.1...\n", ifName)

	// 2. Set DNS to 127.0.0.1
	// netsh interface ip set dns "Interface Name" static 127.0.0.1
	cmd := exec.Command("netsh", "interface", "ip", "set", "dns", ifName, "static", "127.0.0.1")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("netsh failed: %v (%s)", err, string(out))
	}
	
	// Set secondary to 8.8.8.8 (fallback)?
	// No, that defeats the adblocker.
	
	return nil
}

func (w *WindowsConfigurator) RestoreDNS() error {
	if w.interfaceName == "" {
		// Try to detect again if we crashed and lost state
		ifName, err := w.detectInterface()
		if err != nil {
			return err
		}
		w.interfaceName = ifName
	}

	fmt.Printf("Restoring DNS for %s to DHCP...\n", w.interfaceName)

	// Set to DHCP
	cmd := exec.Command("netsh", "interface", "ip", "set", "dns", w.interfaceName, "dhcp")
	return cmd.Run()
}

func (w *WindowsConfigurator) detectInterface() (string, error) {
	// PowerShell hack to find interface with Default Gateway
	cmd := exec.Command("powershell", "-Command", 
		"Get-NetIPConfiguration | Where-Object { $_.IPv4DefaultGateway -ne $null } | Select-Object -ExpandProperty InterfaceAlias")
	
	out, err := cmd.Output()
	if err != nil {
		// Fallback
		return "Wi-Fi", nil
	}
	return strings.TrimSpace(string(out)), nil
}
