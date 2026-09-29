# Remote containers

Remote machines are listed through one SSH round trip. The remote shell first
checks whether a Herdr server is running with the read-only
`herdr status server --json` command, then emits that machine's kind beside
its container listing. The kind is cached in that listing result and reused
when a place is opened; no second probe is needed.

A machine whose server is not running Herdr is treated as a tmux machine. On a
tmux backend, opening one of its containers creates a local proxy container
named `MACHINE/CONTAINER`. Its only pane runs the configured attachment
transport; the local prefix and status bar are disabled for that container, so
input and the status bar belong to the remote multiplexer.

```toml
[remote]
machines = ["devbox"]
ssh-config = "~/.ssh/config"
transport = "ssh"              # ssh (default), mosh, or et
reconnect-initial = "1s"
reconnect-max = "30s"
```

## Herdr machines

Herdr is native when the remote machine runs a Herdr server. On the Herdr
backend, `nexo remote open MACHINE/CONTAINER` does not create a local proxy or
nest a Herdr client in a pane. If `MACHINE` is not already in Herdr's saved
machine list, nexo explicitly runs:

```sh
herdr machine add --label MACHINE MACHINE
```

It reports that the machine was saved, then reports that the selected
container is available under that machine in Herdr's sidebar. `nexo remote
new MACHINE NAME` creates the remote workspace with Herdr over SSH and follows
the same native-open flow. Closing a Herdr remote container reports that there
is no local proxy to close; it never closes the remote workspace.

A Herdr remote cannot be opened from the tmux backend. nexo returns an
unsupported-backend message directing the user to open it from Herdr instead.

## tmux remotes from Herdr

When the local backend is Herdr and the remote kind is tmux, nexo fills the
missing capability with a Herdr workspace proxy. The workspace label is
`MACHINE/CONTAINER`, but labels are display values only. Nexo stores the exact
identity in Herdr workspace display metadata as the `nexo-proxy` token and
finds proxies by that token, never by label. Its root pane runs the same
hidden `nexo remote attach` command and reconnect loop used by tmux proxies.

Tokens are display metadata rather than durable identity. If Herdr drops them,
for example after a server restart, nexo cannot identify the old workspace and
creates a new proxy instead of reusing it.

## Attachment and commands

The SSH transport retries dropped attachments with exponential backoff up to
`reconnect-max`. Only an SSH connection error (exit status 255) is retried;
any other exit status ends the attachment. The SSH attachment enables
`ServerAliveInterval=15` and `ServerAliveCountMax=3` so a dropped link is
detected promptly; listing and other remote commands use plain SSH. Press `q`
during a retry wait to leave the local proxy; other keys typed during the wait
are discarded. Mosh and ET provide their own reconnection behavior. Closing a
proxy only removes the local container; it never closes the remote container.

Commands:

- `nexo remote open MACHINE/CONTAINER` opens a native Herdr container or the
  appropriate local proxy.
- `nexo remote new MACHINE [NAME]` creates a remote Herdr workspace or tmux
  session and opens it using the same rules as `remote open`.
- `nexo remote close [MACHINE/CONTAINER]` closes only the local proxy. With no
  target, the current proxy is used where the active backend exposes one.
- `nexo remote list` lists remote containers and includes each machine's
  detected kind in `--json` output. Listing always uses SSH.
