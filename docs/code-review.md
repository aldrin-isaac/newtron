# Code Review Guidelines

Procedural rules for reviewing code changes — applied as a pass over a
diff (uncommitted, a commit, or a PR). Complements `ai-instructions.md`:
this file is the procedural reference for the REVIEW phase; the
universal directives there still apply.

The intent is quality over quantity. A review that surfaces 30 nits is
ignored; a review that surfaces 3 high-confidence issues gets fixed.
The rubric below is calibrated to filter aggressively.

---

## When to apply

This guide applies whenever the task is **reviewing existing code** rather
than writing new code:

- The user asks for a review of a feature, diff, or PR
- An assistant proactively reviews its own newly-written code before
  declaring a task done
- A pre-PR sanity check after a feature lands but before the PR opens

By default, the scope is the unstaged diff (`git diff`) unless the user
names a specific commit, branch, or PR.

---

## Review angles

Apply each angle independently against the diff. Each is a distinct
lens; combining them produces shallow coverage.

1. **Project-guidelines compliance** — verify the change conforms to
   `CLAUDE.md`, `DESIGN_PRINCIPLES.md`, `DESIGN_PRINCIPLES_NEWTRON.md`,
   `editing-guidelines.md`, and `ai-instructions.md`. Match each cited
   principle to the diff line that violates it.

2. **Obvious bugs** — read the change at face value. Logic errors,
   null/undefined handling, race conditions, resource leaks, security
   issues, off-by-one errors. Stay focused on the change itself; do not
   read extra context.

3. **Historical context** — read `git blame` and prior commits touching
   the modified lines. Some changes look fine in isolation but conflict
   with the reason the code got there. Catch the "this was added to
   work around X" cases where the workaround is being silently removed.

4. **Past PR feedback** — past PRs that touched these files may have
   review comments that still apply. If a prior PR rejected an approach
   the current PR re-introduces, surface that.

5. **Inline comment compliance** — code comments are guidance to future
   readers. Verify the change respects what the surrounding comments
   say (e.g., a comment that says "must hold lock X" must still hold).

---

## False-positive filter

Do NOT flag any of the following, regardless of confidence:

- **Pre-existing issues** — only flag what THIS diff introduced. A bug
  on a line the diff didn't touch is out of scope.
- **Looks-like-a-bug-but-isn't** — patterns that superficially resemble
  bugs but work correctly on inspection.
- **Pedantic nitpicks** — a senior engineer wouldn't call this out in
  review. Variable names, comment phrasing, whitespace, single-line
  formatting.
- **Compiler/linter/typechecker territory** — missing imports, type
  errors, unused vars, formatting violations. Assume CI runs them.
- **General quality complaints** — "needs more tests", "needs more
  comments", "could be more secure" — unless `CLAUDE.md` explicitly
  requires the specific dimension.
- **Lint-ignored intentionally** — issues silenced by an explicit
  `// nolint`, `# noqa`, etc. The author already decided.
- **Likely intentional** — changes that look unusual but are clearly
  part of the broader feature's design.
- **Out-of-scope lines** — real issues on lines this diff didn't modify
  belong in a separate cleanup, not this review.

---

## Confidence rubric

Score each candidate issue 0–100 before deciding whether to report it.
**Report only issues at confidence ≥ 80.** If nothing scores 80 or
higher, the review's conclusion is "no issues found" — say so, briefly.

| Score | Description |
|------:|-------------|
| 0–25 | Likely false positive or pre-existing. Skip. |
| 26–50 | Minor nitpick not explicitly in project guidelines. Skip. |
| 51–75 | Valid but low-impact. Skip. |
| 76–90 | Important issue requiring attention. **Report.** |
| 91–100 | Critical bug or explicit principle violation. **Report.** |

When scoring, ask: would a senior engineer on this team agree this is
worth raising? If the answer is "maybe", the score is < 80.

If a candidate is flagged because of a `CLAUDE.md` / `DESIGN_PRINCIPLES`
rule, the score is not automatic — verify the cited principle actually
addresses this case. Citing a principle that doesn't apply is the same
as flagging without grounds.

---

## Output

For each reported issue:

- One-sentence description
- File path and line number (or range)
- Cite the violated principle or describe the bug in one sentence
- Concrete fix suggestion (what to change, not just what's wrong)

Group by severity:

- **Critical (90–100)**: must fix before merging
- **Important (80–89)**: should fix; merge only with explicit
  acknowledgement of the tradeoff

If there are no high-confidence issues, the report is one or two
sentences confirming the diff looks clean. Do not pad with
"considered these dimensions" — silence on a dimension means it
passed.

No emojis. No "great work" preambles. No summary of what the diff does
(the user wrote it). Lead with the issues; if none, say so.

---

## What this is NOT

- Not a substitute for running the test suite — that's the test gate's
  job, not the review's
- Not a code-style preference debate — formatters and linters own that
- Not a redesign — surface issues, do not propose alternate
  architectures unless the issue is itself an architectural violation
