# nextask demo — split screen: local + gpu-server
# Build:  container build -t nextask-demo -f doc/Dockerfile.demo .
# Run:    container run --rm --volume /path/to/nextask:/vhs nextask-demo doc/demo.tape
Output doc/demo.mp4
Output doc/demo.gif

Set Shell "bash"
Set FontSize 40
Set Width 3200
Set Height 1400
Set Padding 20
Set TypingSpeed 60ms
Set Framerate 30

# Hidden: start tmux, set up both panes
Hide
Type "tmux new-session -d -s demo && tmux set -t demo status off"
Enter
Sleep 0.5s
# Set up left pane (local)
Type `tmux send-keys -t demo 'source /setup-local.sh && clear' Enter`
Enter
Sleep 1s
# Split right pane at 20% width for server
Type `tmux split-window -h -p 30 -t demo`
Enter
Sleep 0.3s
Type `tmux send-keys -t demo 'source /setup-server.sh && clear' Enter`
Enter
Sleep 0.5s
# Attach to tmux (focus is on right pane after split)
Type "tmux attach -t demo"
Enter
Sleep 0.5s
Show

# Right pane (gpu-server): show hostname
Type "hostname"
Sleep 300ms
Enter
Sleep 1.5s

# Right pane: start worker
Type@80ms "nextask worker"
Sleep 400ms
Enter
Sleep 2s

# Switch to left pane
Ctrl+B Left
Sleep 0.5s

# Left pane (local): show workers
Type@80ms "nextask worker list"
Sleep 300ms
Enter
Sleep 2s

# Left pane: hostname task
Type@50ms "nextask enqueue 'hostname' --attach"
Sleep 400ms
Enter
Sleep 3s

# Left pane: training task
Type@50ms "nextask enqueue "
Sleep 200ms
Type@40ms "'python train.py'"
Sleep 150ms
Type@50ms " --snapshot --tag model=resnet"
Sleep 150ms
Type@50ms " --attach"
Sleep 400ms
Enter
Sleep 8s

# Left pane: list tasks
Type@80ms "nextask list --since 1h"
Sleep 300ms
Enter
Sleep 2s

Sleep 1s
