# Code Review Guidelines

Procedural rules for reviewing code changes — applied as a pass over a
diff (uncommitted, a commit, or a PR). Complements `ai-instructions.md`:
this file is the procedural reference for the REVIEW phase; the
universal directives there still apply.

The intent is **broad coverage + honest reporting**. Check every angle
explicitly; report every real issue the angle surfaces, even
medium-impact ones; cull only the genuinely-noise categories below. A
review that fires on every linter complaint loses signal; a review that
silently passes over real issues loses trust. Calibrate between them.

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
lens; combining them produces shallow coverage. Every review report
MUST explicitly enumerate which angles were checked and the outcome
(clean / N findings).

1. **Project-guidelines compliance** — verify the change conforms to
   `CLAUDE.md`, `DESIGN_PRINCIPLES.md`, `DESIGN_PRINCIPLES_NEWTRON.md`,
   `editing-guidelines.md`, and `ai-instructions.md`. Match each cited
   principle to the diff line that violates it.

2. **Obvious bugs** — read the change at face value. Logic errors,
   null/undefined handling, race conditions, resource leaks, security
   issues, off-by-one errors. Stay focused on the change itself.

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

## What to skip

Cull only these categories — they are noise the review should not add to:

- **Pre-existing issues** — only flag what THIS diff introduced. A bug
  on a line the diff didn't touch belongs in a separate cleanup. (If
  the bug is critical, mention it separately as "out-of-scope but
  noted" so it doesn't get lost.)
- **Compiler / linter / typechecker territory** — missing imports,
  type errors, unused vars, formatting violations. Assume CI catches
  them; flagging them duplicates a more reliable signal.
- **Lint-ignored intentionally** — issues silenced by an explicit
  `// nolint`, `# noqa`, or equivalent. The author already decided.
- **Out-of-scope lines** — real issues on lines this diff didn't modify
  belong in a separate cleanup, not this review.

Note: there is intentionally NO blanket filter for "nitpicks" or
"likely intentional changes." A nit might be the only signal that
something deeper is wrong; an "intentional" change might still be a
bug. Surface the finding with low confidence; the operator decides.

---

## Confidence rubric

Score each candidate issue 0–100. **Report all issues at confidence ≥ 50.**
If nothing meets that bar, still produce the per-angle enumeration and
say so explicitly per angle — no silent passes.

| Score | Description | Action |
|------:|-------------|--------|
| 0–25 | Likely false positive, pre-existing, or genuinely a non-issue | Skip |
| 26–49 | Possibly real but unverified, or so minor the report-cost exceeds value | Skip (mention in passing if relevant) |
| 50–69 | Valid but low-impact, OR a nit on something the principles touch on | **Report (Minor)** |
| 70–84 | Important issue requiring attention | **Report (Important)** |
| 85–100 | Critical bug or explicit principle violation | **Report (Critical)** |

When scoring, ask: would a senior engineer on this team want to see
this raised? If the answer is "probably yes", report it; the operator
can dismiss what they don't act on.

If a candidate is flagged because of a `CLAUDE.md` / `DESIGN_PRINCIPLES`
rule, the score is not automatic — verify the cited principle actually
addresses this case. Citing a principle that doesn't apply is the same
as flagging without grounds.

---

## Output

Every review report has TWO parts: the **per-angle enumeration** (always
present, even when clean) and the **findings list** (grouped by
severity).

**Part 1 — Per-angle enumeration:**

```
Angles checked:
1. Project-guidelines compliance — clean
2. Obvious bugs — 1 finding
3. Historical context — clean
4. Past PR feedback — clean
5. Inline-comment compliance — 2 findings
```

Use one line per angle. Outcome per angle is either `clean` or
`N finding(s)`. If an angle is genuinely not applicable (e.g.,
historical context on a brand-new file), say `n/a — <one-sentence
reason>`. Do NOT silently skip an angle.

**Part 2 — Findings, grouped by severity:**

For each reported issue:

- One-sentence description
- File path and line number (or range)
- Cite the violated principle or describe the bug in one sentence
- Concrete fix suggestion (what to change, not just what's wrong)

Group by severity:

- **Critical (85–100)**: must fix before merging
- **Important (70–84)**: should fix; merge only with explicit
  acknowledgement of the tradeoff
- **Minor (50–69)**: should consider; merge fine without

If a finding crosses multiple angles, list it once under the highest-
severity angle and reference the others.

If there are no findings at all, Part 1 still appears (every angle
reported `clean`), followed by one sentence: "No findings."

No emojis. No "great work" preambles. No summary of what the diff does
(the user wrote it). Lead with Part 1, then findings.

---

## What this is NOT

- Not a substitute for running the test suite — that's the test gate's
  job, not the review's
- Not a code-style preference debate — formatters and linters own that
- Not a redesign — surface issues, do not propose alternate
  architectures unless the issue is itself an architectural violation
