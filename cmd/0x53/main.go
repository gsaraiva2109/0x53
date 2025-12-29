package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"0x53/internal/blocklist"
	"0x53/internal/config"
	"0x53/internal/core"
	"0x53/internal/dns"
	"0x53/internal/ipc" // Added import
	sys "0x53/internal/os"
	"0x53/internal/service"
	"0x53/internal/ui"

	tea "github.com/charmbracelet/bubbletea"
)

const SocketPath = "/run/sinkhole.sock"
const PidFile = "/run/sinkhole.pid"

func main() {
	// Simple subcommand logic
	mode := "run"
	if len(os.Args) > 1 {
		mode = os.Args[1]
	}

	switch mode {
	case "daemon":
		runDaemon()
	case "tui", "client":
		runClient()
	case "run", "monolith":
		runMonolith()
	default:
		// Fallback for flags (e.g. -restore)
		if strings.HasPrefix(mode, "-") {
			runMonolith()
		} else {
			fmt.Printf("Unknown command: %s\nUsage: sinkhole [run|daemon|tui]\n", mode)
			os.Exit(1)
		}
	}
}


// --- CLIENT MODE (TUI) ---
func runClient() {
	client, err := ipc.NewClient(SocketPath)
	if err != nil {
		fmt.Printf("Failed to connect to daemon at %s: %v\n", SocketPath, err)
		fmt.Println("Is the sinkhole daemon running?")
		os.Exit(1)
	}
	defer client.Close()

	tuiModel := ui.NewModel(client)
	p := tea.NewProgram(tuiModel, tea.WithAltScreen())

	if _, err := p.Run(); err != nil {
		fmt.Printf("TUI Error: %v\n", err)
		os.Exit(1)
	}
}

// --- DAEMON MODE (Root Required) ---
func runDaemon() {
	requireRoot()
	
	// Setup Signal Handling
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	fmt.Println("Starting Sinkhole Daemon...")
	
	// Write PID file
	if err := os.WriteFile(PidFile, []byte(fmt.Sprintf("%d", os.Getpid())), 0644); err != nil {
		fmt.Printf("Warning: Failed to write PID file: %v\n", err)
	}
	defer os.Remove(PidFile)

	// Init Components
	cfg := config.Default() // TODO: Load from /etc/sinkhole/config.yaml
	
	// Force system log path for daemon
	cfg.LogPath = "/var/log/go-sinkhole.log"

	blMgr := blocklist.NewManager(cfg)
	srv := dns.NewServer(cfg, blMgr)
	svc := service.NewAppService(srv, blMgr)
	
	// Setup File Logging (Same as Monolith)
	if err := os.MkdirAll(filepath.Dir(cfg.LogPath), 0755); err != nil {
		fmt.Printf("Failed to create log dir: %v\n", err)
	}
	logFile, err := os.OpenFile(cfg.LogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Printf("Failed to open log file %s: %v\n", cfg.LogPath, err)
	} else {
		defer logFile.Close()
		fmt.Printf("Daemon Logs: %s\n", cfg.LogPath)
	}

	// Helper for dual logging
	logFunc := func(msg string) {
		// 1. Send to Service (Ring Buffer for TUI/RPC)
		svc.Log(msg)
		
		// 2. Write to File (Persistent History)
		if logFile != nil {
			ts := time.Now().Format("2006-01-02 15:04:05")
			fmt.Fprintf(logFile, "[%s] %s\n", ts, msg)
		}
	}

	// Wire Logger
	srv.SetLogger(logFunc)
	blMgr.SetLogger(logFunc)

	// Start IPC Server
	listener, err := ipc.StartServer(svc, SocketPath)
	if err != nil {
		fmt.Printf("Failed to start IPC server: %v\n", err)
		os.Exit(1)
	}
	defer listener.Close()
	fmt.Printf("IPC active at %s\n", SocketPath)

	// Load Blocklists
	go func() {
		if err := svc.Reload(); err != nil {
			logFunc(fmt.Sprintf("Blocklist load error: %v", err))
		}
	}()
	
	// Start DNS
	osConfig := getOSConfig()
	fmt.Println("Unlocking Port 53...")
	osConfig.UnlockPort()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := srv.Start(ctx); err != nil {
		fmt.Printf("DNS Start Error: %v\n", err)
		os.Exit(1)
	}
	
	// Capture System DNS
	select {
	case <-srv.Ready:
		fmt.Println("DNS Ready. Configuring system...")
		if err := osConfig.SetupDNS(); err != nil {
			fmt.Printf("System DNS Setup Failed: %v\n", err)
			srv.Stop()
			os.Exit(1)
		}
	case <-time.After(2 * time.Second):
		fmt.Println("DNS Start Timeout")
		srv.Stop()
		os.Exit(1)
	}

	fmt.Println("Daemon Running.")
	<-stop
	fmt.Println("Stopping Daemon...")
	
	srv.Stop()
	osConfig.RestoreDNS()
}

// --- MONOLITH MODE (Dev/Standalone) ---
func runMonolith() {
	// Flags
	restoreFlag := flag.Bool("restore", false, "Emergency restore of system DNS settings")
	// Only parse flags if we are in run mode to avoid conflict with subcommands
	if len(os.Args) > 1 && strings.HasPrefix(os.Args[1], "-") {
		flag.Parse()
	} else if len(os.Args) > 2 {
		// Handle "sinkhole run -restore"
		flag.CommandLine.Parse(os.Args[2:])
	}

	osConfig := getOSConfig()

	// 1. Emergency Restore Mode
	if *restoreFlag {
		if err := osConfig.RestoreDNS(); err != nil {
			fmt.Printf("Restore failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("System DNS settings restored.")
		os.Exit(0)
	}

	requireRoot()

	// 1. Setup Signal Handling
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	// 2. Normal Startup
	fmt.Println("Initializing Go-Sinkhole (Monolith)...")

	// Check Privileges - Moved to requireRoot()

	cfg := config.Default()

	// Create Core Components
	blMgr := blocklist.NewManager(cfg)
	srv := dns.NewServer(cfg, blMgr)

	// Create Service Layer (The Brain)
	svc := service.NewAppService(srv, blMgr)

	// Load Blocklists asynchronously
	fmt.Println("Loading blocklists...")
	// Use Service to reload (it wraps manager)
	go func() {
		if err := svc.Reload(); err != nil {
			fmt.Printf("Error loading blocklists: %v\n", err)
		}
	}()

	// 3. Prepare Environment
	fmt.Println("Unlocking Port 53...")
	if err := osConfig.UnlockPort(); err != nil {
		fmt.Printf("Failed to unlock port 53: %v\n", err)
	}

	// 4. Start Server
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := srv.Start(ctx); err != nil {
		fmt.Printf("Server failed to start: %v\n", err)
		os.Exit(1)
	}

	// Wait for listener
	select {
	case <-srv.Ready:
		fmt.Println("DNS Listener ready.")
	case <-time.After(2 * time.Second):
		fmt.Println("Timeout waiting for server startup.")
		srv.Stop()
		os.Exit(1)
	}

	// 4. Takeover System DNS
	fmt.Println("Configuring System DNS...")
	if err := osConfig.SetupDNS(); err != nil {
		fmt.Printf("Failed to setup system DNS: %v\n", err)
		srv.Stop()
		os.Exit(1)
	}

	fmt.Println("Sinkhole is running. Press Ctrl+C to stop.")

	// 5. Setup TUI Model (Inject Service)
	tuiModel := ui.NewModel(svc)

	// 6. Setup File Logging
	if err := os.MkdirAll(filepath.Dir(cfg.LogPath), 0755); err != nil {
		fmt.Printf("Failed to create log dir: %v\n", err)
	}
	logFile, err := os.OpenFile(cfg.LogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Printf("Failed to open log file %s: %v\n", cfg.LogPath, err)
	} else {
		defer logFile.Close()
		absPath, _ := filepath.Abs(cfg.LogPath)
		fmt.Printf("App Logs: %s (Check /root/ if running with sudo!)\n", absPath)
	}

	// Helper for dual logging
	logFunc := func(msg string) {
		// 1. Send to Service (Ring Buffer for TUI)
		svc.Log(msg)
		
		// 2. Write to File (Persistent History)
		if logFile != nil {
			ts := time.Now().Format("2006-01-02 15:04:05")
			fmt.Fprintf(logFile, "[%s] %s\n", ts, msg)
		}
	}

	// Wire Logger
	srv.SetLogger(logFunc)
	blMgr.SetLogger(logFunc)

	// 7. Setup TUI Debug Logging (Bubbletea)
	if f, err := tea.LogToFile("debug.log", "debug"); err != nil {
		fmt.Println("fatal: could not create debug logs:", err)
		os.Exit(1)
	} else {
		defer f.Close()
	}

	p := tea.NewProgram(tuiModel, tea.WithAltScreen())

	if _, err := p.Run(); err != nil {
		fmt.Printf("Alas, there's been an error: %v", err)
	}

	// 7. Graceful Shutdown
	fmt.Println("\nShutting down...")

	cancel()
	srv.Stop()

	if cfg.RestoreOnExit {
		if err := osConfig.RestoreDNS(); err != nil {
			fmt.Printf("Error restoring DNS: %v\n", err)
		} else {
			fmt.Println("DNS restored successfully.")
		}
	}

	time.Sleep(500 * time.Millisecond)
}

func getOSConfig() core.DNSConfigurator {
	if runtime.GOOS == "windows" {
		return sys.NewWindowsConfigurator()
	}
	return sys.NewLinuxConfigurator()
}

func requireRoot() {
	if runtime.GOOS == "linux" && os.Geteuid() != 0 {
		fmt.Println("Error: Rule #1: You must be root (sudo) to run the Daemon/Server.")
		os.Exit(1)
	}
}
