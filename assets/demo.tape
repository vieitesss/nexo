# nexo demo: the setup below is deliberately throwaway and never uses a live
# multiplexer or Herdr server. It creates a private tmux socket, then removes it.
Output assets/demo.gif
Require tmux
Require git
Require go

Set Shell "bash"
Set FontFamily "Iosevka Term"
Set FontSize 18
Set Width 1100
Set Height 620
Set Padding 18
Set Margin 12
Set MarginFill "#18242b"
Set WindowBar "Colorful"
Set Framerate 30
Set PlaybackSpeed 1.4
Set TypingSpeed 12ms

Hide
Type "set -eu; unset TMUX TMUX_PANE HERDR_ENV HERDR_SOCKET_PATH HERDR_PANE_ID; demo_tmp=$(mktemp -d); server=nexo-demo-$$; trap 'tmux -L $server kill-server >/dev/null 2>&1 || true; chmod -R u+w $demo_tmp 2>/dev/null || true; rm -rf $demo_tmp' EXIT; go build -o $demo_tmp/nexo ./cmd/nexo; export NEXO_DEMO_TMP=$demo_tmp NEXO_DEMO_SERVER=$server HOME=$demo_tmp/home XDG_CONFIG_HOME=$demo_tmp/config XDG_STATE_HOME=$demo_tmp/state; mkdir -p $HOME $XDG_CONFIG_HOME/nexo $XDG_STATE_HOME $demo_tmp/projects $demo_tmp/bin; for name in alpha beta; do mkdir -p $demo_tmp/projects/$name; git -C $demo_tmp/projects/$name init -q; git -C $demo_tmp/projects/$name config user.email demo@example.invalid; git -C $demo_tmp/projects/$name config user.name Demo; printf '%s project\\n' $name > $demo_tmp/projects/$name/README.md; git -C $demo_tmp/projects/$name add README.md; git -C $demo_tmp/projects/$name commit -qm initial; done; printf '%b\\n' '#!/bin/sh' 'printf \\047pi agent waiting\\n\\047' 'sleep 120' > $demo_tmp/bin/pi; chmod +x $demo_tmp/bin/pi; printf '%b\\n' 'paths = [\\047'$demo_tmp'/projects\\047]' '[agents]' 'idle-seconds = 1' 'poll-interval = 1' 'notify = false' > $demo_tmp/config.toml; tmux -L $server -f /dev/null new-session -d -s alpha -c $demo_tmp/projects/alpha; tmux -L $server new-session -d -s beta -c $demo_tmp/projects/beta; tmux -L $server new-session -d -s agent -c $demo_tmp/projects/beta $demo_tmp/bin/pi; tmux -L $server new-session -d -s review -c $demo_tmp/projects/alpha; socket=$(tmux -L $server display-message -p '#{socket_path}'); pane=$(tmux -L $server list-panes -a -F '#{pane_id}' | head -n 1); export TMUX=$socket,0,0 TMUX_PANE=$pane"
Enter
Sleep 2s
Show
Type "$NEXO_DEMO_TMP/nexo --backend=tmux -c $NEXO_DEMO_TMP/config.toml tui containers" Enter
Sleep 3s
Type "alp"
Sleep 2s
Escape
Sleep 1s
Type "$NEXO_DEMO_TMP/nexo --backend=tmux -c $NEXO_DEMO_TMP/config.toml tui agents" Enter
Sleep 3s
Type "pi"
Sleep 2s
Escape
Sleep 1s
Hide
Type "tmux -L $NEXO_DEMO_SERVER kill-server >/dev/null 2>&1 || true; chmod -R u+w $NEXO_DEMO_TMP 2>/dev/null || true; rm -rf $NEXO_DEMO_TMP"
Enter
Sleep 1s
Show
