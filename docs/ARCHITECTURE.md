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

If a relative age is converted to a timestamp, set a flag such as:

```text
posted_at_estimated = true
```

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
status TEXT
detail_status TEXT
```

JSON text is acceptable for `apply_emails` in V1 if a normalized child table is unnecessary.

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
