# `nexo list --json`

Agent automation reference: [the nexo structured CLI skill](../skills/nexo/SKILL.md).

`nexo list --json` writes one JSON array to standard output. Candidates are
ordered by configured path order, then filesystem name order. A candidate is
emitted only once, using its canonical path; its first configured group wins.
Warnings are written to standard error and do not change the JSON shape.

Each array element has this schema:

```json
{
  "path": "/home/me/work/project",
  "group": "/home/me/work",
  "git": true,
  "linked_worktree": false
}
```

| Field | Type | Meaning |
| --- | --- | --- |
| `path` | string | Canonical path to the project candidate. |
| `group` | string | Canonical configured parent directory containing the candidate. |
| `git` | boolean | Whether the candidate has a `.git` directory or file. |
| `linked_worktree` | boolean | Whether `.git` is a file identifying a linked worktree. |

An empty discovery result is `[]`.

## `nexo remote list [--json]`

`nexo remote list` lists containers from every configured machine. With
`--json`, it writes one array whose per-machine objects have this shape. A
successful listing includes `kind`, either `tmux` or `herdr`:

```json
{
  "machine": "devbox",
  "kind": "tmux",
  "containers": [
    {"id": "$1", "name": "api", "machine": "devbox"}
  ]
}
```

A machine that cannot be reached is still included with an `error` string and
an omitted `containers` field. Results retain configured machine order.

## `nexo list --places --json`

This command writes one array of place objects. Every object has a `kind`:
`project` for a discovered project or `container` for an open container.

Project places include `path`, `group`, `git`, and `linked_worktree`.
Container places include `machine`, `group`, `container_id`,
`container_name`, and optionally `preview`. Search and display fields
(`search_text` and `display_remainder`) may also be present; they are
picker-facing values.

`group` is the configured parent directory for project places and the machine
name for container places. The place picker keeps those namespaces separate,
so a project group and machine with the same text do not merge. Results are
ordered by project discovery followed by local and remote container sources.
