# Output assets/tui-screenshot.png
Output assets/tui-demo.gif 

Set FontSize 16
Set Width 1200
Set Height 800
Set Padding 20
Set FontFamily "JetBrains Mono"

Hide
Type "go build -o 0x53 ./cmd/0x53"
Enter
# Run daemon in background (requires sudo, but standard user usually can't passwordless sudo in VHS)
# However, github runners have passwordless sudo.
Type "sudo ./0x53 daemon > /dev/null 2>&1 &"
Enter
Sleep 2s
Show

Type "./0x53 tui"
Enter
Sleep 2s

# Navigation Demo
Type "j"
Sleep 500ms
Type "j"
Sleep 500ms
Type "k"
Sleep 500ms

# Switch Tabs
Type @2s " " # Tab is hard to simulate sometimes, explicit key is better if mapped, assuming standard input
Type "	" # Tab character
Sleep 2s

# List View
Type "j"
Sleep 500ms
Type " " # Toggle
Sleep 1s
Type " " # Toggle back
Sleep 1s

# Allowlist
Type "	"
Sleep 1s
Type "a"
Sleep 1s
Type "example.com"
Sleep 1s
Enter
Sleep 2s

# Quit
Type "q"
Sleep 1s
