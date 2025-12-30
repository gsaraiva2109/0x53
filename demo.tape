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
Sleep 1s
Type "j"
Sleep 500ms
Type "j"
Sleep 500ms
Type "k"
Sleep 500ms

# 2. Lists Tab
Type "	" # Focus Menu
Sleep 500ms
Right # Move to Lists
Sleep 500ms
Enter # Select Lists
Sleep 1s

Type "j"
Sleep 500ms
Type "j"
Sleep 500ms
Type " " # Toggle Effect
Sleep 1s
Type " " # Toggle Back
Sleep 1s

# 3. Allowlist Tab
Type "	" # Focus Menu
Sleep 500ms
Right # Move to Allowlist
Sleep 500ms
Enter # Select Allowlist
Sleep 1s

# Add Item
Type "a"
Sleep 1s
Type "my-app.local"
Sleep 500ms
Enter
Sleep 1s
# Add another to delete
Type "a"
Sleep 1s
Type "temp.lan"
Enter
Sleep 1s
# Delete it
Type "j"
Sleep 500ms
Type "d"
Sleep 1s

# 4. Local Records Tab
Type "	" # Focus Menu
Sleep 500ms
Right # Move to Local
Sleep 500ms
Enter # Select Local
Sleep 1s

Type "a"
Sleep 1s
Type "192.168.1.50"
Type "	" # Tab to domain
Type "nas.home.arpa"
Sleep 1s
Enter
Sleep 2s

# 5. Soft Reload
Type "r"
Sleep 2s

# Cleanup
Type "q"
Sleep 1s
