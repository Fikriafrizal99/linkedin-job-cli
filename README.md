# Fork Direction: LinkedIn Job Collector

> This fork is being refactored into a focused **LinkedIn Job Collector**. The upstream README below is retained as a reference while the refactor is in progress.
>
> Approved project documents:
> - [Project Scope](docs/PROJECT_SCOPE.md)
> - [Target Architecture](docs/ARCHITECTURE.md)
> - [Implementation Plan](docs/IMPLEMENTATION_PLAN.md)
> - [Backlog](BACKLOG.md)
> - [Application Workbench](docs/APPLICATION_WORKBENCH.md)
> - [Gmail Setup](docs/GMAIL_SETUP.md)
> - [UI Reference](docs/UI_REFERENCE.md)
>
> Scope rule: **collect accurately first; execute applications separately.** HR-contact research is retained as optional enrichment. Application execution, Gmail drafts, CV selection, and multi-source collectors belong to later/separate work.

---

# linkedin-jobs

<p align="center">
  <img src="docs/assets/logo.png" width="200" alt="linkedin-jobs logo">
</p>

Pull your personalized **LinkedIn "Recommended for you"** feed from your own
browser session, search the public job board, and score every job with
**custom rubrics you describe in plain English** — write a preferences
paragraph once and the LLM turns it into a weighted scoring model that rates
each posting your way. Everything persists to a local SQLite store with
offline full-text search.

<p align="center">
  <a href="https://www.youtube.com/shorts/9_SiM2UVm_I">
    <img src="https://img.youtube.com/vi/9_SiM2UVm_I/maxresdefault.jpg" width="640" alt="Intro video — click to play on YouTube">
  </a>
</p>

## Highlights

- `**recommended**` — your personalized job feed, authenticated via your own
LinkedIn browser session (your cookie; no password stored).
- `**search**` — anonymous public job-board search (no login required).
- `**url**` — paste any LinkedIn search/collection URL (job-alert email link,
saved search, browser URL) and score every job on that page; authenticated via
your session (like `recommended`) so `--top` pulls the full result set.
- `**hr**` — paste a job URL and get the best person to reach out to (ranked
contacts + reasoning + a tailored hook + company-scoped LinkedIn search links).
- **Salary parsing** — handles `CA$173,000.00 - CA$220,000.00`, `$212,500/yr`,
`$120k`, USD/CAD.
- **LLM enrichment + fit scoring** — OpenAI-compatible API extracts structured
facts and rates each job against your rubrics, producing a 0-100 fit score.
- **SQLite + FTS5** — every fetched job is stored and instantly searchable
offline across title, company, and description.
- **Agent-native** — every read command supports `--json`.
- **Pipeline tracking** — tag jobs `saved` / `applied` / `rejected` with notes.
- **Dynamic rubric scoring** — write a preferences paragraph once; the LLM
extracts your rubrics (plus system defaults for salary, work arrangement, and
location). Each job is rated 1-5 per rubric and combined into one normalized
0-100 weighted average, with a per-rubric `fit_reason` breakdown.
- **Tunable** — every rubric weight is editable (1-10, default 5) in
`settings.yaml`; `amend` changes a few rubrics, `reset` starts fresh.
- **Profile** — salary floor, locations, and work arrangement live under the
`profile:` section of `settings.yaml` and feed the system rubrics.
- **Export** — JSON / CSV / Markdown.

## Install

### Agent skill (recommended)

The CLI ships with an agent skill that wraps every command so your AI agent can
run it on your behalf — fetching jobs, scoring fit, finding contacts, managing
your pipeline. Install it for your agent, start a new session, and the skill
installs the `linkedin-jobs` CLI binary for you on first use (no manual binary
install needed).

**opencode, Cursor, Codex, and other `~/.agents/skills/` agents:**

```bash
npx skills add paputechxyz/linkedin-job-cli --skill linkedin-jobs -g
```

**Claude Code** (uses `~/.claude/skills/` — add `-a claude-code` so the
`skills` CLI symlinks it there automatically):

```bash
npx skills add paputechxyz/linkedin-job-cli --skill linkedin-jobs -g -a claude-code
```

**Hermes:**

```bash
hermes skills install paputechxyz/linkedin-job-cli/hermes-skill
```

Start a **new agent session** after installing — skills load at session start,
not mid-session. The first time you use it, the skill detects if the
`linkedin-jobs` binary is missing, downloads a pinned, checksum-verified release into
`~/.local/bin`, and walks you through setup (LLM provider, LinkedIn session). Browse it on [skills.sh](https://www.skills.sh/paputechxyz/linkedin-job-cli/linkedin-jobs).

#### Update

Pull the latest skill (and re-trigger the binary self-update on next session):

```bash
# opencode / Claude Code / Cursor / Codex (global install)
npx skills update linkedin-jobs -g

# Hermes
hermes skills update linkedin-jobs
```

To check what's pending before updating, Hermes users can run
`hermes skills check linkedin-jobs`. The installed CLI binary also self-updates
on run when a newer GitHub release is available, so updating the skill alone is
usually enough.

#### Uninstall

Remove the skill wrapper, then delete the CLI binary the skill placed on
`PATH`:

```bash
# 1. Remove the skill
# opencode / Claude Code / Cursor / Codex (global install)
npx skills remove linkedin-jobs -g -y
# Hermes
hermes skills uninstall linkedin-jobs

# 2. Remove the CLI binary and its data
rm -f ~/.local/bin/linkedin-jobs
rm -rf ~/.linkedin-jobs        # config, cache, db
```

> Config and local data live under `~/.linkedin-jobs`. Drop that directory to
> wipe everything;

### CLI binary only

If you don't use an agent, or want the binary on `PATH` yourself:

- **Prebuilt binary** — run the canonical installer (the same one the skill
  uses). It pins to the latest release tag and verifies the binary against
  `checksums.txt` (sha256) plus a cosign keyless signature before installing to
  `~/.local/bin`:
  ```bash
  curl -fsSL https://raw.githubusercontent.com/paputechxyz/linkedin-job-cli/main/hermes-skill/scripts/install-cli.sh \
    -o /tmp/lj-install.sh && bash /tmp/lj-install.sh
  ```
  To do it by hand instead: download the asset for your platform from the
  [latest release](https://github.com/paputechxyz/linkedin-job-cli/releases/latest),
  verify it against the release's `checksums.txt`, put it on `PATH`
  (e.g. `~/.local/bin`), and `chmod +x` it. Assets:
  `linkedin-jobs_{darwin,linux}_{arm64,amd64}` and
  `linkedin-jobs_windows_amd64.exe`.
- **From source** — requires Go 1.26+:
  ```bash
  just build
  ```

### Local skill development

```bash
just install-skill      # symlink ~/.hermes/skills/productivity/linkedin-jobs -> ./hermes-skill
just uninstall-skill    # remove the symlink
```

The skill lives in `hermes-skill/` (`SKILL.md` + `references/`). It documents
when to use each command, prerequisite checks, approval gates for destructive
operations, workflow recipes, and common pitfalls.

## Auth (for `recommended` and `url`)

`recommended` and `url` use your LinkedIn session. `search` works without it.

### Easy way: `auth login` (macOS & Windows + Chrome)

If you're already logged into LinkedIn in Chrome, the CLI grabs your session
automatically — no cookie extensions, no DevTools:

```bash
linkedin-jobs auth login
```

#### How it works (two stages)

**Stage 1 — silent read (no browser window opens):**

1. The CLI locates Chrome's encrypted cookie database
2. Chrome holds a lock on this file while running, so the CLI copies it to a temp directory, then opens the copy read-only.
3. It retrieves the Chrome cookie key from the OS secret store — the **macOS Keychain** (`Chrome Safe Storage` passphrase) on macOS, or **Windows DPAPI** (the AES key in Chrome's `Local State`, unprotected via `CryptUnprotectData`) on Windows. **On macOS the first run shows a keychain prompt** — click **Always Allow** so every future run is silent. Windows DPAPI needs no prompt.

**Stage 2 — guided browser login (fallback):**

If the silent read fails — you're not logged in, Chrome isn't installed, the
keychain was denied, or the cookies are stale — the CLI launches a **headed**
Chrome window via the Chrome DevTools Protocol (`chromedp`):

1. A dedicated **managed profile** is used (`~/.linkedin-jobs/chrome-profile/`),
 not your real Chrome profile, so it never conflicts with your running
 browser. This profile persists across runs and accumulates LinkedIn trust.
2. Log in normally, all `linkedin.com` cookies are captured, the browser
 closes, and the session is written.

**Timeout:** the guided flow waits up to 5 minutes for you to complete login.
LinkedIn may challenge a fresh managed profile on first login (email/SMS
verification) — complete it in the window and the capture
proceeds automatically.

#### Where cookies are stored


| `LJ_COOKIES_FILE` env | Write target                                                         |
| --------------------- | -------------------------------------------------------------------- |
| set                   | that path                                                            |
| unset                 | `~/.linkedin-jobs/cookies.txt` (created automatically, `0600` perms) |

#### Refreshing a stale session

Re-run `linkedin-jobs auth login`. The silent read pulls fresh cookies from
Chrome's current cookie store. If `li_at` itself has expired, the guided
fallback lets you log in again.

```bash
linkedin-jobs auth status      # "Session captured but incomplete" → it's stale
linkedin-jobs auth login       # re-capture
```

### Manual way: env vars (headless, agent, CI)

For headless use where no browser is available, export your cookies manually
and point the CLI at them:

```bash
export LJ_COOKIES_FILE=/path/to/cookies.txt   # raw "name=val; name=val" header
# or:  export LJ_COOKIE="li_at=...; JSESSIONID=ajax:..."
```

The `csrf-token` is derived automatically from your `JSESSIONID` cookie.

### Verify

```bash
linkedin-jobs auth status      # checks li_at + JSESSIONID are present and valid
```

## Usage

### Recommended (your personalized feed — primary command)

```bash
linkedin-jobs recommended                       # pull your feed
linkedin-jobs recommended --top 25              # cap at 25 jobs
linkedin-jobs recommended --json                # machine-readable output
```

Every fetched job is persisted and scored. Preferences
(work arrangement, salary floor) live under `profile:` in `settings.yaml` and
feed the soft system rubrics, which lower the score on mismatches rather than
dropping jobs. Use `list --remote --min-salary ...` (or the `serve` UI filters)
to exclude at view time.

### Search (anonymous)

Searches the public job board. Jobs already in the DB (by LinkedIn ID) are
skipped entirely — re-running the same query shows only what's new since the
last run. Pass `--force-overwrite` to re-process existing jobs.

The first positional argument is the keyword search. Use `--location` for
geographic filtering, `--remote`/`--hybrid`/`--onsite` for workplace type, and
`--posted-within` to limit how recent listings are. These are passed to LinkedIn
as structured query params (`location`, `f_WT`, `f_TPR`). LinkedIn geocodes the
location server-side, so "Toronto" covers the entire GTA. Combine work-type
flags for OR (e.g. `--remote --hybrid`).

`--posted-within Nd` keeps only jobs posted in the last N days (LinkedIn's
`f_TPR` "Date posted" filter). The value must be a number of days with a `d`
suffix — e.g. `1d`, `7d`, `30d`, `365d`; any other shape errors out.

```bash
linkedin-jobs search "Senior Software Engineer" --location Toronto --remote
linkedin-jobs search "Staff Engineer" --location "Mississauga, ON" --hybrid --top 50
linkedin-jobs search "Backend Developer" --location "San Francisco" --remote --hybrid
linkedin-jobs search "Go Engineer" --posted-within 7d                   # only postings from the past week
linkedin-jobs search "Go Engineer"                                     # keywords only
```

### URL (authenticated)

Paste a LinkedIn search/collection URL — typically a job-alert email link or a
URL copied from the browser. For URLs with a `keywords=` param, the URL's
filters are replayed against the authenticated Voyager `jobCards` API — the
same XHR the browser fires when you scroll `/jobs/search/` — so `--top` pulls
every page (the anonymous `seeMoreJobPostings` endpoint caps early, e.g. 10 of
32). For URLs that only carry explicit job IDs
(`originToLandingJobPostings` from a job-alert email with no keywords, or
`currentJobId`), those IDs are used directly.

```bash
linkedin-jobs url "https://www.linkedin.com/jobs/search/?currentJobId=4415889466&originToLandingJobPostings=4415889466%2C4434154740&keywords=Staff%20Engineer"
linkedin-jobs url "https://www.linkedin.com/jobs/search/?keywords=Staff%20Engineer&geoId=101788145" --top 50
linkedin-jobs url "https://www.linkedin.com/jobs/collections/recommended/?start=0"
```

Authenticated via your captured browser session (see `auth status`); without a
session it falls back to the limited anonymous endpoint. Salary and full
description are fetched per-job from the public detail page.

`url` is for **pages of many jobs**. A single-job posting URL
(`/jobs/view/<id>/`) is rejected with a hint to use `job <id>` instead — that
one-job URL carries no job-card list, so `url` would do the wrong thing.

### Job (single posting by ID)

Fetch + fit-score exactly one posting by its numeric LinkedIn job ID.

```bash
linkedin-jobs job 4431544268
```

The argument must be a bare integer ID (digits only) — the trailing digits of a
`/jobs/view/<...>-<id>/` URL. A full URL or any non-integer is rejected with a
hint; if you paste a `/jobs/view/` URL the error tells you the ID to use. For a
page of many jobs, use `url <search-url>` instead.

### HR (who to reach out to about a job)

Paste any LinkedIn job URL and get back the single best person to contact to get
your application noticed, a ranked shortlist, the reasoning, a tailored outreach
hook, and a ready-to-click LinkedIn people-search URL for each contact scoped to
that company. It fetches the public job page (extracting the company, its
LinkedIn slug, and its numeric company id from the page's "See who you know"
links), pulls the company's public profile, then asks the LLM to pick the
highest-leverage contact — reading the job description for signals like "work
directly with our CTO". With no LLM configured it falls back to a deterministic
heuristic (founding roles → founders/CTO; manager roles → VP/Director;
otherwise recruiter first) and still emits usable search links. Works
anonymously; no session required.

```bash
linkedin-jobs hr "https://www.linkedin.com/jobs/view/4435820129/"
linkedin-jobs hr "https://www.linkedin.com/jobs/search/?currentJobId=4435820129&f_C=105863333"
linkedin-jobs hr "<url>" --json      # machine-readable report
linkedin-jobs hr "<url>" --no-llm    # heuristic only (no LLM key needed)
```

### Work with stored jobs

```bash
linkedin-jobs list --company Google --min-salary 150k
linkedin-jobs list --sort-score --min-score 70      # your best-fit shortlist
linkedin-jobs show 4430749190
linkedin-jobs query "staff backend"            # offline FTS5 search
linkedin-jobs query "engineer" --exclude amazon
linkedin-jobs stats --top 25
linkedin-jobs tag 4430749190 applied --note "referred by Sam"
linkedin-jobs export --format csv -o jobs.csv
linkedin-jobs search "Staff Engineer" --location Toronto --top 10  # skips jobs already in the DB; only new ones are fetched/scored
linkedin-jobs count
linkedin-jobs purge
```

### Web UI (local browser)

```bash
linkedin-jobs serve                      # read-only browser on http://127.0.0.1:8080
linkedin-jobs serve --port 9000          # custom port
```

The local browser now includes the **Job Command Center** under `/app/` with Dashboard, Jobs, Applications, CV Profiles, Collect Jobs, and Settings. The previous jobs browser remains available at `/legacy`.

The **Jobs** database and **Applications** page now work as one batch pipeline.

From Jobs:

- Select all visible / multi-select filtered jobs
- Queue Selected
- Process Selected, which routes supported jobs by application method:
  - EMAIL → Prepare → Gmail Draft → Review Queue
  - EASY_APPLY → manual Easy Apply Queue
  - unsupported destinations → skipped by default

From Applications:

- Prepare Selected
- Create Drafts
- Review Selected / Review Draft Queue
- Work the Easy Apply Queue one item at a time
- Open LinkedIn Easy Apply in a new tab, or explicitly open the next 3
- Mark Applied & Next only after manually submitting on LinkedIn
- Confirm Send for already-APPROVED Gmail drafts

The Review Queue provides sequential `Approve & Next` review for email applications. The Easy Apply Queue uses `READY_EASY_APPLY → IN_PROGRESS → APPLIED`; opening LinkedIn never counts as submission, and APPLIED requires explicit human confirmation after manual submission. Sending remains explicit: a separate final confirmation page and checkbox are required before Gmail `drafts.send` is called. There is no auto-send, LinkedIn form auto-fill, or auto-submit.

See [Application Workbench](docs/APPLICATION_WORKBENCH.md), [Gmail Setup](docs/GMAIL_SETUP.md), and [UI Reference](docs/UI_REFERENCE.md) for the current workflow and safety boundaries.

The local server binds to localhost by default. Browser write endpoints are POST actions guarded by the per-session CSRF token and existing lifecycle checks.

### Rubric scoring

Fit scores are driven by a **rubric set** you generate once from a preferences
paragraph. Run `linkedin-jobs setup` and describe what you want in a job (work
arrangement, salary, location, tech, perks, deal-breakers). The LLM extracts
your criteria as **dynamic rubrics** and merges them onto three **system
rubrics** that always apply:

- **System rubrics** (computed deterministically in Go): `salary` (vs your
floor), `work_arrangement` (remote/hybrid/onsite match), `location` (preferred
location match).
- **Dynamic rubrics** (rated 1-5 by the LLM per job): everything else — e.g.
`preferred_tech`, `avoided_tech`, `free_snacks`, `ai_intensity`. List-type
criteria (preferred/avoided tech) collapse into one rubric carrying `items`.

Each rubric has a **weight** (1-10, default 5) tunable in `settings.yaml`. The
final score is a weight-normalized average of every rubric's 1-5 rating mapped
to 0-100 — so a job rated 4/5 across the board scores ~80 whether you have 3
rubrics or 15. **There are no hard caps**: a job matching an avoided tech just
gets a low rating on that rubric. `fit_reason` shows the per-rubric breakdown,
e.g. `salary 4/5 (w5), preferred_tech 5/5 (w5), avoided_tech 1/5 (w5) | total 73`.

```bash
linkedin-jobs setup          # paragraph → rubrics (+ system defaults)
linkedin-jobs amend          # add/change a few rubrics; unmentioned ones preserved
linkedin-jobs reset          # wipe all rubrics and restart setup
linkedin-jobs rescore-all    # re-score every stored job against the current rubrics
```

The structured inputs the system rubrics need live under `profile:` in
`settings.yaml` (salary floor, currency, locations, work arrangement, tech
lists) — `setup` fills them from your paragraph and prompts for any required
number (like a salary floor) it omitted.

```bash
linkedin-jobs profile show            # show active profile knobs
```

Every fetched job is enriched and scored (one LLM call per new candidate);
duplicates are skipped by content-hash. Run `rescore-all` to re-run enrichment
and scoring across the whole DB after editing rubrics.

### LLM configuration

Bring your own key — no provider key ships, and nothing is persisted to disk.
Set an env var (or rely on opencode discovery):

```bash
export OPENAI_API_KEY="sk-..."                 # or LJ_LLM_API_KEY
export LJ_LLM_MODEL="gpt-4o-mini"              # optional, default gpt-4o-mini
# For Ollama / vLLM / Azure:
export LJ_LLM_BASE_URL="http://localhost:11434/v1"
# Or Anthropic Claude:
export ANTHROPIC_API_KEY="sk-ant-..."
```

Resolution order (first match wins): `LJ_LLM_*` / `OPENAI_API_KEY` env →
`ANTHROPIC_API_KEY` env → opencode's stored credentials → **`claude` CLI
(Claude Code session reuse, last fallback)**. Explicit env vars win over both,
so you can override either. The opencode path reuses the provider configured in
opencode (e.g. your GLM Coding Plan key → `glm-5.2`); `ANTHROPIC_API_KEY`
targets Anthropic's OpenAI-compatible endpoint. If no opencode credentials are
found, the `claude` CLI path detects a logged-in `claude` on PATH
(`claude auth status`) and shells out to `claude -p` per call, reusing your
Claude Pro/Max subscription **without a separate API key** — handy when running
under a Claude Code OAuth session, whose token is not exposed to subprocesses.
Set `LJ_LLM_DISABLE_CLAUDE_CLI=1` to force the HTTP/key path.

```bash
linkedin-jobs config show             # resolved provider (key redacted) + settings
linkedin-jobs config path             # settings/db file locations
linkedin-jobs doctor                  # diagnose provider + settings completeness
```

No key? Fetch+score commands (`recommended`/`url`/`search`/`job`) exit with a setup prompt instead of running — configure a provider first.

### Settings

`settings.yaml` lives in `~/.linkedin-jobs/` (override with `$LJ_SETTINGS_FILE`).
Run `linkedin-jobs setup` to create it interactively. Everything (DB, settings,
FX cache) lives under `~/.linkedin-jobs/`:

```yaml
scoring:
  rubrics:                       # weight 1-10 (default 5); system rubrics are computed in Go,
                                 # dynamic rubrics are rated 1-5 by the LLM. Run `setup` to generate.
    - id: salary
      kind: system
      weight: 5
    - id: work_arrangement
      kind: system
      weight: 5
    - id: preferred_tech
      kind: dynamic
      weight: 5
      items: [Java, Python, Go]
    - id: avoided_tech
      kind: dynamic
      weight: 5
      items: [C#, .NET]
    - id: location               # dynamic: LLM rates jurisdiction/proximity fit
      kind: dynamic              # from the description, e.g. "remote flexible anywhere"
      weight: 5
      description: "Hybrid or Onsite must be in Toronto; remote is flexible"
      applies_to: [hybrid, onsite]  # optional: skip this rubric for other arrangements
profile:                         # structured inputs for the system rubrics
  work_arrangement: [remote, hybrid]
  min_salary: 200000
  min_salary_currency: CAD
  preferred_tech: [Java, Python, Go]
  avoided_tech: [C#, .NET, Ruby]
```

When scoring runs, the CLI prints which profile context it loaded (knobs from
`settings.yaml`), so you can tell at a glance whether scores reflect your actual
context or ran context-free.

## Configuration &amp; env


| Variable               | Purpose                                                             | Default                             |
| ---------------------- | ------------------------------------------------------------------- | ----------------------------------- |
| `LJ_DB_PATH`           | SQLite database path                                                | `~/.linkedin-jobs/linkedin_jobs.db` |
| `LJ_LLM_DELAY_SECONDS` | seconds to pause between successive LLM scoring calls (avoids 429s) | `2.0`                               |
| `ANTHROPIC_API_KEY`    | Claude provider (auto-detected by config)                           | —                                   |
| `LJ_LLM_DISABLE_CLAUDE_CLI` | Disable the `claude` CLI backend (force HTTP/key path)         | —                                   |
| `LJ_COOKIES_FILE`      | path to a file with a raw `Cookie:` header                          | —                                   |
| `LJ_COOKIE`            | raw cookie header string                                            | —                                   |
| `OPENAI_API_KEY`       | LLM key (or `LJ_LLM_API_KEY`)                                       | —                                   |
| `LJ_LLM_BASE_URL`      | OpenAI-compatible base URL (or `OPENAI_BASE_URL`)                   | `https://api.openai.com/v1`         |
| `LJ_LLM_MODEL`         | model name                                                          | `gpt-4o-mini`                       |


> `settings.yaml` always resolves to `~/.linkedin-jobs/settings.yaml` unless
> `$LJ_SETTINGS_FILE` is set. There is no persisted provider file — set an env
> var for the LLM.

## Project structure

```
main.go
cmd/                       cobra commands (recommended, search, list, score, profile, config, hr, …)
internal/
  auth/                    session resolution (cookie env/file/browser), Chrome cookie-store reader, guided browser login, csrf
  config/                  env-based config + YAML settings
  filter/                  deterministic hard preference filter
  hr/                      outreach research: best contact + ranked list for a job
  linkedin/                HTTP client, anonymous scraper, recommended graphql
  llm/                     OpenAI-compatible provider resolution + enrich/score
  models/                  JobPosting, Profile, Enrichment
  profile/                preference knobs (settings.yaml profile: section)
  render/                  table / detail / JSON / stats output
  salary/                  salary parsing + filtering
  store/                   SQLite + FTS5 persistence + content-hash dedup
```

## Releasing

Releases are automated by [release-please](https://github.com/googleapis/release-please)
(see `.github/workflows/release.yml` and `release-please-config.json`). Merge a
[Conventional Commit](https://www.conventionalcommits.org/) to `main` and
release-please opens a "release PR"; merge that PR and it tags `vX.Y.Z`, builds
the platform binaries with GoReleaser, keyless-signs `checksums.txt` with
cosign, and attaches binaries + checksums + signature to the GitHub Release.
The skill installer's version pin then auto-advances to the new tag (via the
`sync-version-pin` job).

Which commit **type** triggers a release (release-please's default strategy):

| Type                                        | Bump    | Example                              |
| ------------------------------------------- | ------- | ------------------------------------ |
| `feat:` / `feat(scope):`                    | minor   | `feat: add csv export`               |
| `fix:` / `fix(scope):`                      | patch   | `fix: stop double-counting salaries` |
| `feat!:` / `fix!:` / any `<type>!:`         | major\* | `feat!: drop the visa field`         |
| any type + `BREAKING CHANGE:` footer        | major\* | any commit carrying this footer      |

\* On a `0.x` version a breaking change bumps to `1.0.0` by default. Set
`bump-minor-pre-major` in `release-please-config.json` if you'd rather keep
breaking changes within the `0.x` line.

These types do **not** trigger a release on their own — they're folded into the
*next* release's changelog when one happens: `chore:`, `docs:`, `refactor:`,
`perf:`, `style:`, `test:`, `build:`, `ci:`, `revert:`.

Notes:

- **Highest bump wins.** A release window with both a `feat` and a breaking
  change bumps major — there's no mixing.
- **`feat(scope)` is still `feat`.** The scope is informational (it groups the
  changelog entry) and doesn't change the bump.
- **Force a specific version** with a `Release-As: X.Y.Z` footer on any commit.

## Notes

- This tool is for personal job-search use. Respect LinkedIn's Terms of Service.

