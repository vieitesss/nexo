# `nexo list --json`

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
