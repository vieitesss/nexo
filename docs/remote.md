# Remote proxies

Remote machines are listed through SSH using the selected multiplexer backend.
Opening a remote container creates a local proxy container named
`MACHINE/CONTAINER`. Its only pane runs the configured attachment transport;
the local prefix and status bar are disabled for that container, so input and
the status bar belong to the remote multiplexer.

```toml
[remote]
machines = ["devbox"]
ssh-config = "~/.ssh/config"
transport = "ssh"              # ssh (default), mosh, or et
reconnect-initial = "1s"
reconnect-max = "30s"
```

The SSH transport retries dropped attachments with exponential backoff up to
`reconnect-max`. Only an ssh connection error (exit status 255) is retried; any
other exit status ends the attachment. The SSH attachment enables
`ServerAliveInterval=15` and `ServerAliveCountMax=3` so a dropped link is
detected promptly; listing and other remote commands use plain SSH. Press `q`
during a retry wait to leave the local proxy; other keys typed during the wait
are discarded. Mosh and ET provide their own reconnection behavior. Closing a
proxy only removes the local container; it never closes the remote container.

Commands:

- `nexo remote open MACHINE/CONTAINER` opens or switches to a proxy.
- `nexo remote new MACHINE [NAME]` creates a remote tmux container and opens
  its proxy. A missing name is read from a prompt.
- `nexo remote close [MACHINE/CONTAINER]` closes only the local proxy. With no
  target, the current proxy is used.
- `nexo remote list` lists remote containers. Listing always uses SSH.
