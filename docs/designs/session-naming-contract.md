# Session naming contract

- **Status:** Approved by the owner on 2026-11-05; not implemented
- **Scope:** Feature 1 only, completing the naming questions in [design_draft.md](../../design_draft.md)
- **Evidence:** Mercurial 7.2.2 and Jujutsu 0.46.0 disposable-repository checks are recorded in that draft. Native slash-label checks cover tmux, Herdr, and Rex.
- **Approval boundary:** The owner approved proceeding after reviewing the contract and confirming the unset-template behavior. Approval covers naming only, including the Git/hg/jj metadata readers and the concrete rules below; grouping remains unapproved.

## 1. Configuration and template language

```toml
[session]
name-template = "{org}/{team}/{repo}"
```

- Missing `name-template`: use today's naming path exactly, without any extra naming-metadata discovery. Existing backend lookup/reuse stays unchanged.
- An explicitly empty or whitespace-only value is a configuration error, not an alias for the default.
- Recognize only `{org}`, `{team}`, and `{repo}`. Repeated or adjacent variables are allowed. Literal prefixes, suffixes, and separators are allowed.
- Unknown variables or unmatched braces fail configuration loading with the key and a useful explanation. Literal brace escaping and expressions are not part of this version.
- Render variable values once. A brace in a value is data, not another substitution. Do not evaluate shell expressions, environment variables, or commands in the template.
- If valid configuration renders an empty/whitespace-only name for a particular project, fail that open action with an explanation. Do not silently create an unnamed container or change the configured template.

## 2. VCS coverage and detection

Include Git, Mercurial, and Git-backed Jujutsu metadata discovery for naming only. This does not extend nexo's status, branch, clone, deletion, or worktree behavior to those VCSs.

Use the selected candidate's repository identity. A repository marker must belong to that candidate root: `.jj`, `.git` (file or directory), or `.hg`. Do not walk into an enclosing repository to assign a namespace to an unrelated child project. Where no supported marker belongs to the candidate, use filesystem fallback. Bare repositories without these markers are outside namespace discovery in this version.

In a colocated `.jj`/`.git` candidate, prefer the Jujutsu reader. Otherwise use Git, then Mercurial for candidates with conflicting markers. Do not probe another metadata source merely because a preferred source's remote is absent or ambiguous; use filesystem fallback. This keeps the namespace from changing according to whichever command happens to succeed first.

Keep the selected lexical project path for opening and filesystem fallback. Comparing a VCS-reported root with a selected symlink's target may establish ownership, but must not rewrite the selected path or relocate the checkout. A selected subdirectory without its own repository marker receives filesystem fallback.

No new mandatory dependencies or automatic tool installation. If the needed CLI is not installed or the reader cannot operate safely, fallback remains available.

## 3. Remote selection

### Git and Jujutsu

1. Prefer the fetch URL of the remote named `origin` if that remote exists.
2. If there is no `origin`, use the fetch URL only when exactly one remote exists.
3. If the selected remote has several fetch URLs, has no usable fetch URL, or no remote can be selected unambiguously, use filesystem fallback.
4. Never choose `upstream` or a push URL merely because it supplies a more attractive namespace. A fork's `origin` therefore names the fork, not its upstream project.
5. If an existing `origin` URL is malformed or unrecognized, fallback rather than silently trying other remotes.

Git commands must respect normal Git configuration and worktree layout, including `.git` files and supported URL rewrites, without bypassing trust checks. Use local metadata, not a network command.

Jujutsu queries must use `--ignore-working-copy`, disable color/paging, and preserve files and operation state. Use the documented `jj git root` command to obtain the backing Git directory, then use Git's local remote metadata commands against that returned directory. This shares the Git remote-selection rules, including detection of several fetch URLs, without assuming an internal `.jj` store layout. Also verify workspace-root ownership through the documented `jj root` command. Unexpected output or an unavailable tool falls back.

**Planning evidence:** Jujutsu 0.46.0 `git remote list` returned only the first URL when two fetch URLs were configured for `origin`, with exit 0 and no warning. It is therefore insufficient to enforce the approved ambiguity rule. In both colocated and non-colocated disposable fixtures, documented `jj git root` followed by `git --git-dir=RETURNED_PATH remote get-url --all -- origin` exposed both URLs. Hashes of the checked `.jj`/`.git` files and a dirty working file were unchanged. This command-selection correction preserves the approved behavior; it does not change the remote-selection policy.

### Mercurial

Query the native resolved `default` path in structured form: `hg --noninteractive --cwd PATH paths default -Tjson`.

- Use only a single resolved object's `url`.
- Ignore `pushurl` and `default-push` for naming.
- A missing `default`, two or more resolved URLs, malformed JSON, or a nonzero result means filesystem fallback.
- Let Mercurial resolve `path://` aliases. Do not duplicate alias resolution by parsing `.hg/hgrc` directly.
- Local-directory paths are not forge namespaces and use filesystem fallback.

These rules deliberately do not try to infer user intent from the currently tracked branch or push destination.

## 4. Namespace parsing

Recognize HTTP(S), SSH, and Git URLs with a host, plus SCP-style Git locations such as `git@example.org:acme/api.git`. A username, password, host, and port are transport details, not naming variables.

After removing transport syntax and an optional trailing slash:

- Two nonempty path components mean `org/repo`.
- Three nonempty components mean `org/team/repo`.
- For Git/Jujutsu locations, remove one terminal `.git` suffix from the repository component. Do not strip arbitrary extensions from Mercurial repository names.
- Reject `.`/`..` components, internal empty components, and invalid encodings as unavailable namespace metadata.
- Local filesystem paths, `file://` locations, unknown transports, and paths of other depths use filesystem fallback. Deep namespaces are intentionally not flattened or assigned a guessed team.
- Interpret recognized locations positionally without forge-specific discovery or API calls. A self-hosted URL whose path fits the shape receives that interpretation; a deployment prefix can therefore resemble an organization. Host-specific prefix configuration is outside this version.
- Ignore query/fragment and userinfo when deriving components; never retain them in names or naming diagnostics. Decode component escapes only when doing so does not introduce delimiters or control characters. Encoded `/`, `\\`, or controls cause fallback.

Do not log full remote URLs or raw CLI output in naming errors. Report the reader and reason without credential-bearing values.

## 5. Filesystem fallback

Use the selected local directory's immediate parent basename for `{org}`, its basename for `{repo}`, and no `{team}`. A renamed checkout therefore changes its fallback inputs; there is no persistent association or migration.

For `~/work/acme/api`, use `org=acme`, `repo=api`. This applies to unsupported VCSs, ordinary project directories, absent tools, absent/ambiguous remotes, unsupported remote shapes, and failed discovery.

At filesystem-root edges where a usable parent or basename cannot be formed, do not fabricate a namespace. Fail opening with a clear naming error if the configured template requires values that cannot be formed. Preserve the legacy behavior entirely when no template is configured.

## 6. Missing-team separators

Treat `/`, `-`, and `_` as separators only immediately next to an unavailable `{team}` occurrence.

1. Omit the unavailable placeholder.
2. If the immediately preceding literal text ends with a contiguous run of those separators, remove that run.
3. Otherwise remove such a run from the start of the immediately following literal text.
4. Do not collapse separators elsewhere or strip arbitrary punctuation. Apply the rule separately for each missing-team occurrence.

This makes mixed templates deterministic while avoiding a general conditional-template language:

| Template | Team `platform` | No team |
| --- | --- | --- |
| `{org}/{team}/{repo}` | `acme/platform/api` | `acme/api` |
| `{org}-{team}-{repo}` | `acme-platform-api` | `acme-api` |
| `{org}_{team}_{repo}` | `acme_platform_api` | `acme_api` |
| `{org}/{team}-{repo}` | `acme/platform-api` | `acme-api` |
| `{team}/{repo}` | `platform/api` | `api` |
| `task-{team}` | `task-platform` | `task` |
| `{team}` | `platform` | Open fails: empty rendered name |
| `literal--{org}/{repo}` | `literal--acme/api` | `literal--acme/api` |

Org/repo are resolved together from one metadata source or together from filesystem fallback. Do not mix a remote organization with a local repository name to fill incomplete metadata.

## 7. Native name constraints and compatibility

Keep the existing generated-name replacement of `.` and `:` with `_` after rendering, so templates do not introduce characters tmux already excludes from the current generated name. Preserve Unicode and ordinary literal text; reject NUL, ASCII control characters, or a name with no visible content before handing it to a backend. Outer whitespace is not significant and is trimmed.

Retain Rex's existing generated-label truncation to 128 Unicode characters and the other backends' current validation/error handling. Do not implement a new cross-backend minimum length limit, automatic suffixing, or collision registry. Distinct templates or truncation may yield the same name; that accepted limitation follows existing lookup behavior.

Pass values as command arguments, never interpolated shell commands. A user-supplied leading dash must remain part of the name argument rather than becoming another option; tests must verify native command construction handles that boundary.

Changing the template or metadata can create a differently named session or match a different existing one. No migration, compatibility alias, old-name probe, automatic rename, collision prompt, or persisted project/session association is added for this feature.

## 8. Execution and failure behavior

Only resolve metadata when a configured naming template is needed for an open/create operation; do not add VCS processes to list rendering or default naming. Reuse existing operation inputs if available rather than issuing duplicate discovery calls.

Give metadata discovery a single one-second total deadline, bounded further by the caller's context, without a new configuration knob. A discovery timeout falls back to the selected filesystem path. Caller cancellation propagates instead of creating a session after the user cancels.

Read local configuration through native commands without prompting, fetching, authenticating, or changing repository configuration. Do not add trust bypasses. User-installed extensions and config remain part of the CLI's normal execution environment; the sandbox checks do not establish safety for arbitrary user extensions. Tests must cover command errors, timeouts, missing programs, and unexpected output through nexo's existing fake runner.

## 9. Verification before implementation acceptance

The CLI feasibility checks do not test a nexo implementation. A later plan must add automated tests for:

- Config absent versus explicitly empty; unknown/malformed variables; repeated placeholders; runtime-empty results.
- Every rendering row above, including mixed punctuation and preservation of unrelated literal separators.
- Remote priority, origin/upstream forks, fetch/push differences, several URLs, hg aliases/multi-URLs, and ambiguity fallback.
- All supported URL forms, missing namespaces, deep namespace fallback, credentials, ports, fragments, invalid encodings, and local paths.
- Colocated/non-colocated Jujutsu, `.git` worktree files, exact candidate-root ownership, symlinked candidates, missing tools, cancellation, and bounded discovery.
- Default behavior unchanged, all callers of shared naming policy, backend-specific normalization/limits, and no added migration/collision behavior.
- No production VCS or filesystem mutations from naming, including unsnapshotted Jujutsu changes.

No clones, worktrees, fetches, or real-network access should be necessary for tests of these rules.

## 10. Alternatives and trade-offs

### Remote selection

- **Conservative fixed rule (sections 2–3):** origin or a sole remote; hg default only. Predictable with no extra settings, but ambiguous projects fall back to directory names.
- **Branch-tracking or upstream preference:** can better name an upstream project, but varies by branch and requires different concepts across Git, jj, and hg. Fails when no branch/tracking state exists.
- **Configurable remote priority:** accommodates more workflows, but adds configuration unrelated to template formatting and conflicts with the aim of a small initial feature.

### Optional placeholder rendering

- **Adjacent delimiter removal (section 6):** covers agreed examples with a small rule; mixed punctuation follows the documented left-first convention rather than guessing intention.
- **Literal empty substitution:** simpler but leaves `acme//api` or `acme--api`, contrary to the accepted examples.
- **Conditional template syntax:** handles arbitrary punctuation precisely, but adds a language and escaping rules not requested here.

### VCS support boundary

- **Naming-only Git/hg/jj readers:** adds a small local command path per VCS while sharing namespace parsing and template rendering. Requires tests for command/version differences.
- **Git-only metadata with hg/jj filesystem fallback:** smaller implementation, but cannot use available hg/jj namespace metadata. The rendering interface remains the same.

## 11. What remains outside this contract

Session grouping, its launch UX, session/tab identity, scope membership, native tab support, and ordinary-open interactions with grouped sessions remain separate design work. Approval of this naming contract is not approval of grouping or authority to implement product code, create commits, or synchronize remotes.
