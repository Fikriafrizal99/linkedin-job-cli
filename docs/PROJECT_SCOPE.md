# LinkedIn Job Collector — Project Scope V1.0

## Purpose

This fork is being adapted into a focused **LinkedIn Job Collector**.

Its job is to:

1. Search LinkedIn jobs.
2. Collect stable job identifiers and metadata.
3. Fetch the full job description.
4. Extract application email addresses when they are explicitly present in the posting.
5. Detect application destination/method when possible.
6. Persist jobs in SQLite.
7. Deduplicate repeated collection runs.
8. Keep optional HR/recruiter contact enrichment for later use.

This project is **not** the application execution engine.

## Scope Lock

The collector owns:

- LinkedIn search
- pagination
- job detail retrieval
- job ID normalization
- full job description
- posted date
- email extraction
- application method detection
- application URL/instruction extraction
- SQLite persistence
- deduplication
- repost detection
- rate-limit handling
- optional HR contact enrichment

The collector does **not** own:

- sending job applications
- Gmail sending
- automatic Easy Apply
- CV generation
- CV selection
- cover letter generation
- application follow-up
- Telegram bot
- multi-source Jobstreet/Glints/Kalibrr collection
- large dashboard
- mandatory AI/LLM scoring

New ideas that are outside this scope go to the backlog instead of being implemented immediately.

## System Boundary

```text
SYSTEM A — THIS REPOSITORY

LinkedIn
   |
   v
Search Collector
   |
   v
Job Detail Collector
   |
   v
Parser / Normalizer
   |
   +--> Email Extraction
   +--> Apply Method
   +--> Apply URL / Instruction
   |
   v
Deduplication
   |
   v
SQLite
   |
   +--> Optional HR Contact Enrichment


SYSTEM B — SEPARATE APPLICATION ENGINE

Collector DB
   |
   v
Application Queue
   |
   v
CV Selection
   |
   v
Email Draft Generation
   |
   v
Human Review
   |
   v
Send
```

The future Application Engine should initially create **drafts only**, not automatically send applications.

## Upstream

Fork baseline:

- Upstream project: `paputechxyz/linkedin-job-cli`
- Fork: `Fikriafrizal99/linkedin-job-cli`

The upstream implementation remains useful as a reference for LinkedIn HTTP collection, SQLite, authentication fallback, and HR contact research.

## Retained Upstream Concepts

Keep/adapt:

- `internal/linkedin/`
- `internal/store/`
- `internal/models/`
- anonymous LinkedIn job search
- public job detail retrieval
- optional authenticated fallback
- SQLite
- full-text searchable job storage where useful
- HR contact research logic

Do not make LLM configuration a prerequisite for collection.

## Collector Data Contract

Minimum job record:

```text
job_id
source
title
company
location
job_url
description
posted_at
first_seen
last_seen
scraped_at
apply_email
apply_url
application_method
status
```

Recommended extended fields:

```text
company_linkedin_url
workplace_type
employment_type
apply_emails
detail_fetched_at
content_hash
detail_status
posted_at_estimated
```

## Application Method

Initial normalized values:

```text
EMAIL
EXTERNAL_URL
LINKEDIN
UNKNOWN
```

Examples:

- explicit recruitment email in description -> `EMAIL`
- explicit career/application link -> `EXTERNAL_URL`
- LinkedIn application only -> `LINKEDIN`
- no reliable instruction -> `UNKNOWN`

## Email Extraction

Email extraction should be deterministic first.

Example:

```text
"Please submit your CV to recruitment@example.com"
```

becomes:

```text
apply_email = recruitment@example.com
application_method = EMAIL
```

Support multiple emails when a posting contains more than one address.

No guessing of recruiter email addresses.

## HR Contact

HR Contact is retained as an **optional enrichment module**.

Possible contact categories:

- Recruiter
- Talent Acquisition
- HR
- Hiring Manager
- Relevant Department Manager

Contacts are informational only. The collector does not automatically message them.

Suggested separate table:

```text
job_contacts
------------
id
job_id
name
title
linkedin_url
contact_type
source
created_at
```

## Deduplication

Level 1:

```text
source + job_id
```

Example:

```text
linkedin:123456789
```

Level 2: normalized content fingerprint using company, title, description, and posting-time context when available.

Do not treat every same-title posting as a duplicate. Distinguish:

```text
SAME_JOB_ID
EXACT_DUPLICATE
LIKELY_REPOST
NEW_JOB
```

## Statuses

Collector statuses may include:

```text
NEW
DETAIL_COMPLETE
DETAIL_INCOMPLETE
EMAIL_FOUND
NO_EMAIL
EXTERNAL_APPLY
LINKEDIN_APPLY
FAILED
```

Application lifecycle statuses such as `DRAFT_CREATED`, `SENT`, `FOLLOW_UP`, `INTERVIEW`, and `OFFER` belong to the separate Application Engine.

## Definition of Done — Collector V1

A command equivalent to:

```bash
linkedin-collector collect \
  --keyword "Sales Executive" \
  --location "Indonesia" \
  --posted-within 7d
```

must be able to:

1. collect multiple LinkedIn jobs;
2. paginate until exhausted or until a configured limit;
3. persist LinkedIn job IDs;
4. store title/company/location;
5. store full descriptions;
6. store posting date when available;
7. extract explicit application emails;
8. classify application method;
9. avoid duplicate inserts across reruns;
10. preserve data across restarts;
11. work without an LLM provider;
12. work without LinkedIn authentication for normal anonymous collection;
13. optionally use authenticated fallback for missing detail fields;
14. expose the stored data for the future Application Engine.

## Guiding Principle

> Collect accurately first. Execute applications separately.
