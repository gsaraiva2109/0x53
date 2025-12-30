# Output assets/tui-screenshot.png
Output assets/tui-demo.gif 

Set FontSize 16
Set Width 1920
Set Height 1080
Set Padding 40
Set FontFamily "JetBrainsMono Nerd Font"

Hide
Type "go build -o 0x53 ./cmd/0x53"
Enter
# Start Daemon
Type "sudo ./0x53 daemon > /dev/null 2>&1 &"
Enter
Sleep 2s
# Generate Traffic (Blocked)
Type "dig @127.0.0.1 -p 53 doubleclick.net > /dev/null"
Enter
Type "dig @127.0.0.1 -p 53 ads.google.com > /dev/null"
Enter
# Generate Traffic (Allowed)
Type "dig @127.0.0.1 -p 53 example.com > /dev/null"
Enter
Sleep 1s
Show

Type "./0x53 tui"
Enter
Sleep 1s

# 1. Dashboard & Logs (Show blocked requests)
Sleep 3s

# 2. Lists Tab
Type "	" # Tab
Sleep 1s
Type "j"
Sleep 500ms
Type " " # Toggle
Sleep 1s
Type " " # Toggle back
Sleep 1s

# 3. Allowlist Tab
Type "	" # Tab
Sleep 1s
Type "a"
Sleep 1s
Type "my-app.local"
Sleep 500ms
Enter
Sleep 1s

# 4. Local Records Tab
Type "	" # Tab
Sleep 1s
Type "a"
Sleep 1s
Type "192.168.1.10"
Type "	" # Tab to domain
Type "homeserver.lan"
Sleep 1s
Enter
Sleep 2s

# Cleanup
Type "q"
Sleep 1s
