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
- [ ] Add `posted_at_estimated` when derived from relative time.
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
- [ ] Add collector integration tests.

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

- [ ] read eligible jobs from collector;
- [ ] application queue;
- [ ] CV selection;
- [ ] subject/body generation;
- [ ] Gmail draft creation;
- [ ] manual review;
- [ ] explicit send;
- [ ] application tracking/follow-up.

## Explicitly Deferred

Do not implement these while P0/P1 are incomplete:

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
linkedin-collector collect \
  --keyword "Sales Executive" \
  --location "Indonesia" \
  --posted-within 7d
```

Supporting commands:

```bash
linkedin-collector list
linkedin-collector list --has-email
linkedin-collector list --no-email
linkedin-collector show <job_id>
linkedin-collector stats
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
