# LinkedIn Job Collector — Architecture V1.0

## Target Architecture

```text
                           LINKEDIN
                              |
                    +---------+---------+
                    |                   |
                    v                   v
              Guest Search        Auth Fallback
                 HTTP             (optional only)
                    |
                    v
             Search Collector
                    |
                    v
               Job Cards
                    |
        +-----------+------------+
        |                        |
        v                        v
   Extract job_id             Normalize
        |                        |
        +-----------+------------+
                    |
                    v
             Existing ID Check
                    |
              +-----+-----+
              |           |
           Existing       New
              |           |
              v           v
       update last_seen  Detail Fetch
                           |
                  +--------+---------+
                  |                  |
                  v                  v
           Guest/Public Detail   Authenticated
                                 fallback
                  |                  |
                  +--------+---------+
                           |
                           v
                    Full Description
                           |
         +-----------------+------------------+
         |                 |                  |
         v                 v                  v
   Email Extractor   Apply Instruction   Posted Date
         |              Parser             Parser
         +-----------------+------------------+
                           |
                           v
                    Content Fingerprint
                           |
                           v
                      Dedup / Repost
                           |
                           v
                         SQLite
                           |
                 +---------+----------+
                 |                    |
                 v                    v
              Query/List       Optional HR Contact
                                    Enrichment
```

## Package Direction

Target structure after refactor:

```text
linkedin-job-cli/
|
├── cmd/
│   ├── collect.go
│   ├── list.go
│   ├── show.go
│   └── stats.go
|
├── internal/
│   ├── linkedin/
│   │   ├── client.go
│   │   ├── search.go
│   │   ├── detail.go
│   │   ├── pagination.go
│   │   └── hr.go
│   |
│   ├── parser/
│   │   ├── job.go
│   │   ├── email.go
│   │   └── application.go
│   |
│   ├── store/
│   │   ├── store.go
│   │   └── dedup.go
│   |
│   └── models/
│       ├── job.go
│       └── contact.go
|
├── docs/
│   ├── PROJECT_SCOPE.md
│   ├── ARCHITECTURE.md
│   └── IMPLEMENTATION_PLAN.md
|
└── BACKLOG.md
```

The exact physical layout can evolve, but boundaries must remain clear: collection, parsing, persistence, and optional enrichment should not be mixed with application execution.

## Search Strategy

Primary anonymous endpoint:

```text
/jobs-guest/jobs/api/seeMoreJobPostings/search
```

Supported search inputs should include:

- keywords
- location
- work arrangement
- posted-within
- maximum result limit

### Pagination Rule

Do **not** assume 25 returned cards per request.

Use adaptive pagination:

```text
start = 0

loop:
    fetch(start)
    cards = parsed result

    if cards is empty:
        stop

    new_ids = cards not seen in this run

    if new_ids is empty:
        stop

    start += actual number of cards returned
```

This replaces the upstream fixed page-size assumption in the anonymous `Search()` path.

## Posted-Within Rule

Normalize day filters to LinkedIn's seconds window:

```text
1d  -> r86400
7d  -> r604800
30d -> r2592000
```

Validate this behavior with integration tests because LinkedIn can change internal query handling.

## Detail Fetch Strategy

Preferred sequence:

```text
1. public/guest job-detail response
2. public job page HTML / JSON-LD
3. authenticated Voyager fallback when a session exists
4. DETAIL_INCOMPLETE when all reliable sources fail
```

Authentication is a fallback, not a prerequisite.

## Description Extraction

The full description is a first-class field.

Potential sources in priority order:

1. structured JobPosting JSON-LD;
2. LinkedIn public job-detail HTML containers;
3. authenticated job API fallback.

Preserve plain text suitable for:

- email parsing;
- application-instruction parsing;
- future matching/scoring;
- manual review.

## Posted Date Extraction

Preferred sources:

```text
JSON-LD datePosted
        |
        v
<time datetime>
        |
        v
structured LinkedIn metadata
        |
        v
relative posted age
```

If only a relative age is available (for example `3 days ago`), convert it to an approximate UTC timestamp and set:

```text
posted_at_estimated = true
```

Supported relative units include minutes, hours, days, weeks, months, and years. An exact JSON-LD `datePosted` or `<time datetime>` value always overrides an estimate and resets `posted_at_estimated = false`.

## Email Extraction

Do deterministic extraction before introducing any AI.

Responsibilities:

- extract syntactically valid email addresses;
- lowercase/canonicalize where appropriate;
- remove obvious punctuation around the address;
- deduplicate multiple occurrences;
- retain all explicit emails;
- optionally choose a primary application email based on nearby text.

Do not derive or guess unlisted email addresses.

## Application Instruction Parser

The parser should inspect the posting for explicit application destinations.

Outputs:

```text
apply_emails[]
apply_email
apply_url
application_method
application_instruction
```

Initial application methods:

```text
EMAIL
EXTERNAL_URL
LINKEDIN
UNKNOWN
```

## Persistence

Use SQLite.

Collector-owned tables:

```text
jobs
job_contacts
```

Potential `jobs` schema:

```text
job_id TEXT PRIMARY KEY
source TEXT
title TEXT
company TEXT
company_linkedin_url TEXT
location TEXT
job_url TEXT
description TEXT
workplace_type TEXT
employment_type TEXT
posted_at TEXT
posted_at_estimated INTEGER
first_seen TEXT
last_seen TEXT
scraped_at TEXT
detail_fetched_at TEXT
apply_email TEXT
apply_emails TEXT
apply_url TEXT
application_method TEXT
application_instruction TEXT
content_hash TEXT
structural_hash TEXT
duplicate_classification TEXT
duplicate_of_job_id TEXT
status TEXT
detail_status TEXT
```

JSON text is acceptable for `apply_emails` in V1 if a normalized child table is unnecessary.

Suggested `job_contacts` schema now implemented:

```text
id INTEGER PRIMARY KEY
job_id TEXT
name TEXT
title TEXT
contact_type TEXT
linkedin_url TEXT
search_url TEXT
source TEXT
priority INTEGER
why TEXT
created_at TEXT
```

Role-level enrichment deliberately leaves `name` and `linkedin_url` empty when no concrete person has been verified. `search_url` is a LinkedIn people/company search aid, not a profile URL.

## Dedup Strategy

### ID dedup

Check existing LinkedIn ID **before** expensive detail retrieval where possible.

If already known:

```text
update last_seen
skip unnecessary detail fetch
```

A future refresh mode may re-fetch old details explicitly.

### Content dedup

Compute fingerprint after detail normalization.

Check for an existing matching fingerprint **before inserting a new structural duplicate**.

The upstream order where a row can be upserted before duplicate evaluation should not be copied into the collector refactor.

### Reposts

A new LinkedIn ID with very similar company/title/description is not automatically deleted.

Use posting-date context to classify it as a probable repost.

## Rate-Limit Handling

Minimum client behavior:

```text
200 -> continue

429 ->
    honor Retry-After if provided
    exponential backoff
    retry with a bounded attempt count

500/502/503 ->
    bounded retry with backoff

403 ->
    stop or mark source as blocked
    do not hammer the endpoint
```

Detail requests should use a configurable polite delay/jitter.

Do not add CAPTCHA bypass, stealth bypass, or aggressive protection-evasion logic.

## Authentication

Default:

```text
anonymous
```

Optional authenticated session can be used for detail fallback or HR enrichment.

Session material:

- must not be committed;
- must not appear in logs;
- must remain outside the repository;
- should have restrictive local file permissions.

## LLM Boundary

Collection must not call `mustResolveProvider()`.

The core collector must work without:

- OpenAI;
- Anthropic;
- Claude CLI;
- Ollama;
- OpenCode;
- any external model.

If future enrichment uses an LLM, it must be optional and downstream of successful collection.

## HR Contact Enrichment

Retain and adapt upstream HR research separately from the mandatory collection path.

Suggested flow:

```text
Stored Job
   |
   +--> optional request
          |
          v
     HR Contact Enrichment
          |
          v
      job_contacts
```

The result is supporting information, not an automated outreach action.

Collector-aligned commands:

```bash
linkedin-jobs contacts enrich <job_id>
linkedin-jobs contacts enrich --all --unknown-only --limit 20
linkedin-jobs contacts list <job_id>
```

The deterministic collector path does not guess person names. It stores normalized role targets such as Talent Acquisition, Hiring Manager, and Department Leader.

Optional actual-person resolution is authenticated and bounded:

```text
role target
   |
   v
LinkedIn Voyager GraphQL people search
(currentCompany scoped)
   |
   v
role/headline validation
   |
   +--> strong match -> name + title + linkedin_url
   |
   +--> no strong match -> retain role-level target
```

Usage:

```bash
linkedin-jobs contacts enrich <job_id> --resolve
linkedin-jobs contacts enrich --all --unknown-only --limit 10 --resolve
```

Resolution requires a valid LinkedIn session. It inspects at most 10 search results per role target, has a configurable delay between role searches, and never sends a message or connection request.

People search uses LinkedIn's persisted Voyager GraphQL `voyagerSearchDashClusters` query. The older REST `/search/dash/clusters` surface is not used. Because LinkedIn rotates persisted query IDs with web-client releases, a bounded recent-ID fallback is allowed only for HTTP 400 stale-query responses; authentication, challenge, forbidden, and rate-limit responses are not bypassed.

For WSL/headless environments, session material can be imported from standard input instead of command-line arguments. A full LinkedIn Cookie header from an active browser session is preferred; `li_at` + `JSESSIONID` are structurally necessary but may not be sufficient for live Voyager requests:

```bash
powershell.exe -NoProfile -Command 'Get-Clipboard' | tr -d '\r' | linkedin-jobs auth import
linkedin-jobs auth status --live
```

`auth status` checks structural completeness. `auth status --live` performs one bounded authenticated Voyager probe with redirects disabled, so login/challenge redirects are reported rather than followed.

For WSL/headless environments, session material can be imported from standard input instead of command-line arguments:

```bash
printf '%s\n' '<Cookie header>' | linkedin-jobs auth import
linkedin-jobs auth status
```

The import path validates that `li_at` and `JSESSIONID` are present and writes the session to the local cookie file with permission `0600`. Session values should never be committed, logged, or pasted into issue/chat history.

## Collector Integration Testing

The V1 collector has a deterministic local integration test using an HTTP fixture rather than live LinkedIn traffic. It covers:

- anonymous search query parameters;
- adaptive pagination offsets;
- card parsing and relative posting-date estimation;
- full detail/description retrieval;
- `EMAIL`, `EXTERNAL_URL`, and `LINKEDIN` application classification;
- structural dedup metadata;
- SQLite persistence and round-trip reads.

This keeps regression coverage stable without depending on LinkedIn availability or rate limits.

## External Boundary

The future Application Engine consumes collector data but remains separately deployable.

Contract:

```text
Collector DB / Export
        |
        v
Application Queue
        |
        v
Draft Generator
```

The collector must not require the Application Engine to function.


## Application Engine Boundary

Application execution state is stored separately from collector state.

```text
jobs
  |
  v
applications
  |
  +-- READY_EMAIL
  +-- NEED_REVIEW
  +-- DRAFT_CREATED
  +-- SENT
```

The collector remains the source of job metadata such as `apply_email` and `application_method`. The Application Engine owns CV-profile selection, prepared email content, Gmail draft identifiers, and later send/follow-up state.

CV selection is deterministic and configuration-driven. Profiles live under `application.cv_profiles` in `settings.yaml`; title keyword matches receive higher weight than description matches, with `default_cv_profile` as fallback. Preparing an application does not advance it past `READY_EMAIL` and never sends email.
