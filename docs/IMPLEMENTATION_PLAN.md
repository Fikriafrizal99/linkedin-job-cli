# LinkedIn Job Collector — Implementation Plan

## P0 — Collector Core

Do these first.

- [x] Remove mandatory LLM/provider resolution from the collector path.
- [x] Introduce a collector-only command/path that works without API keys.
- [x] Fix anonymous pagination to advance by actual returned-card count.
- [x] Fix/verify `posted-within` parameter encoding.
- [x] Preserve anonymous search as the normal collection mode.
- [x] Fetch full job detail/description for new jobs.
- [x] Keep optional authenticated fallback.
- [x] Persist collector output to SQLite.
- [x] Check LinkedIn job ID before expensive reprocessing.

## P1 — Application Data Extraction

- [x] Parse and persist `posted_at` when an exact date is available.
- [x] Add `posted_at_estimated` when derived from relative time.
- [x] Add deterministic email extractor.
- [x] Support multiple explicit emails.
- [x] Add primary `apply_email`.
- [x] Detect `EMAIL`, `EXTERNAL_URL`, `LINKEDIN`, and `UNKNOWN`.
- [x] Read LinkedIn detail-page apply controls and unwrap external-apply URLs when exposed.
- [x] Extract explicit application URL/instruction.
- [x] Add a structural content fingerprint separate from the upstream scoring hash.
- [x] Classify matching structural content before persisting the new job ID.
- [x] Add probable repost classification using structural identity + posting-date context.
- [x] Add bounded retry/backoff for 429 and transient 5xx; do not retry 403.
- [x] Add collector integration tests covering anonymous search, adaptive pagination, detail/application parsing, dedup metadata, and SQLite round-trip.

## P2 — HR Contact Enrichment

Keep this feature, but outside mandatory collection.

- [x] Adapt upstream HR contact logic into deterministic, domain-aware collector enrichment.
- [x] Create `job_contacts` persistence.
- [x] Normalize contact categories (`TALENT_ACQUISITION`, `HR`, `HIRING_MANAGER`, `DEPARTMENT_LEADER`, `FOUNDER`, etc.).
- [x] Allow enrichment per job or bounded batch (`contacts enrich`).
- [x] Keep outreach/send actions out of the collector enrichment path.

### P2.1 — Actual Contact Resolution

- [x] Add authenticated, bounded LinkedIn people search scoped by current company.
- [x] Resolve role targets to concrete `name/title/linkedin_url` when a relevant profile is returned.
- [x] Reject placeholder/weak role matches instead of guessing.
- [x] Preserve previously resolved profiles across transient misses or heuristic-only refreshes.
- [x] Keep resolution opt-in via `contacts enrich --resolve`.
- [x] Keep messaging/outreach actions out of the collector.

## P3 — Separate Application Engine

Not part of Collector V1.

Future project/module:

- [x] read eligible jobs from collector;
- [x] application queue with separate lifecycle persistence;
- [x] deterministic CV selection from configured profiles;
- [x] deterministic subject/body generation and persistence;
- [x] Gmail draft creation through an external Gmail draft provider bridge;
- [x] explicit manual review/approval gate;
- [x] explicit-send bridge gated by APPROVED state;
- [ ] application tracking/follow-up.

### P3.1 — Queue Foundation

Implemented commands:

```bash
linkedin-jobs applications queue <job_id>
linkedin-jobs applications queue --all --limit 50
linkedin-jobs applications queue --all --include-review --limit 50
linkedin-jobs applications list
linkedin-jobs applications list --state READY_EMAIL
linkedin-jobs applications show <job_id>
```

Batch queue defaults to jobs with an explicit application email. Non-email jobs are only queued as `NEED_REVIEW` when requested with `--include-review`. Application lifecycle state is stored in a separate `applications` table so collector-owned job state remains independent. Queueing is idempotent by `job_id`.

### P3.2 — CV Selection + Email Preparation

Application settings live under `application:` in `settings.yaml`:

```yaml
application:
  candidate_name: "Your Name"
  default_cv_profile: general
  cv_profiles:
    - id: general
      path: /absolute/path/to/cv-general.pdf
      priority: 1
    - id: sales
      path: /absolute/path/to/cv-sales.pdf
      priority: 10
      keywords: [sales, account executive, business development]
```

The selector scores configured keywords deterministically, weighting title matches above description matches, then falls back to `default_cv_profile`. A profile can be overridden explicitly with `--cv-profile`.

```bash
linkedin-jobs applications profiles
linkedin-jobs applications prepare <job_id>
linkedin-jobs applications prepare --all --limit 50
linkedin-jobs applications prepare <job_id> --cv-profile sales
linkedin-jobs applications show <job_id>
```

Preparation persists `cv_profile`, `subject`, and `body` but keeps the application in `READY_EMAIL`; it does not create or send an email.

### P3.3 — Gmail Draft Creation

The CLI validates and emits a provider-ready payload, including the configured CV attachment path:

```bash
linkedin-jobs applications draft-payload <job_id>
linkedin-jobs applications draft-payload <job_id> --json
```

An external Gmail draft provider creates the draft without sending it. Only after the provider returns a real Gmail draft id does the CLI advance lifecycle state:

```bash
linkedin-jobs applications record-draft <job_id> --draft-id <gmail_draft_id>
```

Safety rules:

- source state must be `READY_EMAIL`;
- recipient, subject, body, CV profile, and CV file must all validate;
- an existing Gmail draft id blocks duplicate draft creation;
- `DRAFT_CREATED` is idempotent for the same provider draft id;
- a conflicting second draft id is rejected;
- this flow never sends email.

### P3.4 — Manual Review Gate

A created Gmail draft is not eligible for sending until it is explicitly approved:

```bash
linkedin-jobs applications approve <job_id>
linkedin-jobs applications approve <job_id> --note "Reviewed in Gmail"
linkedin-jobs applications unapprove <job_id> --note "Needs wording changes"
```

Lifecycle:

```text
DRAFT_CREATED
    |
    | explicit human approval
    v
APPROVED
    |
    | unapprove
    v
DRAFT_CREATED
```

Approval records `reviewed_at` and optional `review_note`. Re-queueing preserves `DRAFT_CREATED`, `APPROVED`, and `SENT`. Once a Gmail draft exists, `applications prepare` cannot silently replace the local subject/body/CV; draft revisions must be handled explicitly so the database cannot drift from the provider draft.

No command in this stage sends email.

### P3.5 — Explicit Send Bridge

Sending is deliberately split into validation, provider action, and persistence:

```bash
linkedin-jobs applications send-request <job_id>
linkedin-jobs applications send-request <job_id> --json
```

The request is emitted only when the application is `APPROVED` and still references a Gmail draft. The external Gmail provider may then execute `send_draft` only after an explicit send request from the user. After Gmail returns the sent message/thread identifiers:

```bash
linkedin-jobs applications record-sent <job_id> \
  --message-id <gmail_message_id> \
  --thread-id <gmail_thread_id>
```

The state transition is:

```text
APPROVED
    |
    | explicit user-authorized Gmail send_draft
    v
SENT
```

Safety rules:

- `DRAFT_CREATED` cannot be sent; it must first pass manual approval;
- `send-request` does not send anything itself;
- only a real provider result can advance the local record to `SENT`;
- repeated recording of the same Gmail message id is idempotent;
- conflicting second message ids are rejected;
- `gmail_message_id`, `gmail_thread_id`, and `sent_at` are persisted for follow-up tracking.

The bridge is implemented; live sending remains user-authorized per application.

### P3.6 — Follow-up Tracking

Intentionally not implemented. The Application Engine stops at explicit send and provider-result persistence. Reminder, follow-up email, and response-tracking automation are out of scope.


## P4 — Job Triage & Cross-Page Selection

Approved next milestone. Detailed product/data contract: [JOB_TRIAGE_WORKFLOW.md](JOB_TRIAGE_WORKFLOW.md).

The goal is to separate **job decision** from **application execution**:

~~~text
Collect
→ Review
→ Shortlist / Later / Skip
→ Start Applications
→ Execute / Track
~~~

### P4.1 — Persistent triage state

- [ ] add jobs.review_state with UNREVIEWED / SHORTLISTED / LATER / SKIPPED;
- [ ] add optional review_reason and reviewed_at;
- [ ] backfill jobs with existing application records to SHORTLISTED;
- [ ] backfill other existing jobs to UNREVIEWED;
- [ ] add validated single and bulk store mutations;
- [ ] add migration/store tests.

### P4.2 — Jobs Inbox

- [ ] make Jobs default to the UNREVIEWED Inbox;
- [ ] add Inbox / Shortlisted / Later / Skipped / All Jobs views and counts;
- [ ] make Shortlist / Later / Skip the primary Inbox actions;
- [ ] remove skipped jobs from active review immediately;
- [ ] keep skipped jobs stored and searchable;
- [ ] keep application lifecycle independent from review_state.

### P4.3 — Cross-page selection

- [ ] persist selected jobs across pagination;
- [ ] add Select visible / Select all matching / Clear selection;
- [ ] show selected-across-pages count;
- [ ] define explicit behavior when filters change;
- [ ] remove the 50-record limit from local triage state changes;
- [ ] retain downstream Gmail/provider batch limits;
- [ ] add browser/regression tests.

### P4.4 — Shortlist handoff

- [ ] add Start Applications to Shortlisted;
- [ ] preview Email / Easy Apply / Needs Attention routing before handoff;
- [ ] only SHORTLISTED jobs enter the normal application batch path;
- [ ] reuse existing idempotent application queue logic;
- [ ] stop presenting Queue Selected / Process Selected as the default Inbox workflow.

### P4.5 — Collection run handoff

- [ ] persist collection_runs and collection_run_jobs;
- [ ] add Review N New Jobs after collection;
- [ ] support run-scoped review so old jobs do not mix with a fresh collection;
- [ ] expose collection summary/history.

### P4.6 — Triage analytics

- [ ] optional skip reasons;
- [ ] funnel counts;
- [ ] skip-reason summaries;
- [ ] collector-quality insights without automatic rejection/query mutation.


## Explicitly Deferred

These remain outside Collector V1 unless explicitly approved:

- Telegram bot
- automatic email sending
- LinkedIn Easy Apply automation
- Jobstreet collector
- Glints collector
- Kalibrr collector
- large dashboard
- AI Agent orchestration
- complex job-fit scoring

Record ideas in `BACKLOG.md` instead.

## Initial CLI Target

```bash
linkedin-jobs collect "Sales Executive" \
  --location "Indonesia" \
  --posted-within 7d
```

Supporting commands:

```bash
linkedin-jobs list
linkedin-jobs list --has-email
linkedin-jobs list --no-email
linkedin-jobs show <job_id>
linkedin-jobs stats
```

## V1 Acceptance Checklist

- [ ] anonymous collection works without LLM configuration;
- [ ] anonymous collection works without LinkedIn login;
- [ ] pagination returns beyond the first response page;
- [ ] duplicate job IDs are not reprocessed unnecessarily;
- [ ] full descriptions are persisted;
- [ ] posting dates are persisted where available;
- [ ] explicit application emails are extracted;
- [ ] application method is classified;
- [ ] data survives process/server restart;
- [ ] rate limits do not cause aggressive retry loops;
- [ ] HR contact remains available as optional enrichment;
- [ ] no application is automatically sent.
