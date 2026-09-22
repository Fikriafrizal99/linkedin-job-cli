# Job Triage Workflow

## Status

Approved target workflow for the next implementation milestone.

This document defines the product and data model between **job collection** and **application execution**. It replaces the assumption that every collected job is immediately eligible for Queue Selected / Process Selected.

~~~text
Collect
  ↓
Review / Triage
  ↓
Shortlist
  ↓
Start Applications
  ↓
Application Execution
  ↓
Track
~~~

## Problem

The current Jobs page mixes two decisions:

1. Do I want this job?
2. How should I execute the application?

That causes several UX problems:

- a job the user does not want can remain visible in the execution surface;
- selection is limited to the currently rendered page;
- moving between pages loses selection context;
- application actions appear before the user has finished deciding whether the job is worth pursuing;
- after collection there is no explicit inbox of new jobs that still require a decision.

The fix is a persistent **job decision state** independent from the existing application lifecycle.

## Core principle

A collected job and an application are different concepts.

Job triage answers:

> Is this opportunity interesting enough to pursue?

Application lifecycle answers:

> If it is being pursued, how far has the application progressed?

These two states must not be derived from each other.

## Job decision state

Add a persistent field on jobs:

**review_state**

Accepted values:

~~~text
UNREVIEWED
SHORTLISTED
LATER
SKIPPED
~~~

### UNREVIEWED

The job was collected and has not received a deliberate user decision.

Newly collected jobs default here.

### SHORTLISTED

The user is interested in the job and wants it to be eligible for the application workflow.

A shortlisted job is not yet an application.

### LATER

The job is interesting, but the user does not want to process it now.

It stays outside active application execution.

### SKIPPED

The user explicitly decided not to pursue the job.

Skipped jobs:

- remain in SQLite for history and deduplication;
- remain searchable in All Jobs;
- do not appear in the default Inbox;
- do not appear in Shortlisted;
- do not enter application previews or application batch execution;
- must not be automatically recreated as applications.

## Application lifecycle remains separate

The existing application lifecycle continues to own execution state, including:

~~~text
READY_EMAIL
READY_EASY_APPLY
NEED_REVIEW
DRAFT_CREATED
APPROVED
IN_PROGRESS
SENT
APPLIED
~~~

Relationship:

~~~text
UNREVIEWED
   ├── SHORTLISTED ──→ Start Applications ──→ APPLICATION LIFECYCLE
   ├── LATER
   └── SKIPPED
~~~

Changing a job to SHORTLISTED must not automatically create a Gmail draft, Easy Apply queue item, or application record.

## Jobs navigation

Target navigation:

~~~text
Jobs
├── Inbox
├── Shortlisted
├── Later
├── Skipped
└── All Jobs
~~~

Example counters:

~~~text
Inbox          42
Shortlisted    11
Later           5
Skipped       126
All Jobs      684
~~~

The default Jobs view should behave as the **Inbox** and show UNREVIEWED jobs.

The primary user task in Inbox is deciding, not applying.

## Jobs actions

Primary Inbox bulk actions:

~~~text
Shortlist Selected
Save for Later
Skip Selected
~~~

The current Jobs-level Queue Selected and Process Selected actions should no longer be the primary Inbox workflow.

Application execution begins from **Shortlisted**.

Single-job actions should mirror the same model:

~~~text
Shortlist
Later
Skip
Open Detail
~~~

When a decision is made from Inbox, the row should immediately leave the Inbox view.

## Skip behavior

Skip is a persistent decision, not a temporary navigation action.

~~~text
Inbox
  ↓ Skip
SKIPPED
  ↓
removed from Inbox
not eligible for Start Applications
retained in database/history
~~~

The UI should provide a short-lived Undo action when practical.

### Optional skip reason

Skip reason is optional and must not block fast triage.

Suggested values:

~~~text
ROLE_MISMATCH
LOCATION
EXPERIENCE_TOO_HIGH
INDUSTRY
COMPANY
COMPENSATION
UNCLEAR_POSTING
ALREADY_SEEN
OTHER
~~~

Suggested fields:

~~~text
review_reason
reviewed_at
~~~

This data may later support analytics and collector-quality feedback. It must not silently mutate collection rules or automatically reject future jobs.

## Cross-page selection

Pagination is a display concern, not a selection boundary.

The list may continue rendering 50 rows per page, but selection must survive page navigation.

Example:

~~~text
83 matching jobs
18 selected across 3 pages
~~~

Required controls:

~~~text
Select visible 50
Select all 83 matching
Clear selection
~~~

Selection should survive:

- Previous / Next page;
- returning from Job Detail to the same Jobs list;
- page-number changes.

A material filter change should either clear selection with a clear message or intentionally recompute query selection. It must not silently retain incompatible selections.

### Selection model

Distinguish:

1. explicit selected IDs;
2. all jobs matching the active query/filter.

Do not implement Select all matching by rendering thousands of hidden checkboxes.

Preferred future contract:

~~~text
selection_mode = explicit | query
selected_ids[]
filter snapshot or query token
excluded_ids[]
~~~

A local server-side selection set is acceptable for the first implementation.

## Bulk triage limits

Local triage actions should not inherit the current external-operation limits.

Recommended:

- Shortlist Selected: no artificial 50-job limit for query-backed local state changes;
- Later Selected: no artificial 50-job limit;
- Skip Selected: no artificial 50-job limit.

Bounded limits should remain for expensive or external side effects such as Gmail draft creation and send operations.

## Shortlisted view

Shortlisted is the handoff between decision and execution.

Primary action:

**Start Applications**

Before starting, show a channel preview:

~~~text
12 shortlisted

5 Email
6 Easy Apply
1 Needs Attention
~~~

Then reuse the existing deterministic routing:

~~~text
SHORTLISTED
   ↓ Start Applications
   ├── EMAIL       → READY_EMAIL
   ├── EASY_APPLY  → READY_EASY_APPLY
   └── unsupported → NEED_REVIEW
~~~

Application creation remains idempotent by job_id.

UNREVIEWED, LATER, and SKIPPED jobs should not enter the normal application batch path.

## Changing intent after application creation

For early states such as READY_EMAIL, READY_EASY_APPLY, and NEED_REVIEW, the user may still:

~~~text
Remove from Applications
Return to Shortlisted
Skip Job
~~~

Once durable provider/review/submission history exists, do not rewrite the record as if it was simply skipped.

Protected examples include:

~~~text
DRAFT_CREATED
APPROVED
IN_PROGRESS
SENT
APPLIED
~~~

A later milestone may add STOPPED or WITHDRAWN semantics. The triage milestone only needs to preserve existing history and prevent destructive conversion.

## Collection run handoff

Collection should end in a decision-oriented result rather than sending the user to the entire accumulated Jobs database.

Target:

~~~text
COLLECTION COMPLETE

4 searches
183 listings scanned

42 new jobs
113 already known
18 exact duplicates
10 likely reposts

[Review 42 New Jobs]
~~~

The Review button should open only the jobs introduced by that run.

### Future collection-run persistence

Target tables:

~~~text
collection_runs
collection_run_jobs
~~~

Suggested collection_runs fields:

~~~text
id
started_at
finished_at
queries_json
locations_json
posted_within
searched_count
new_count
persisted_count
exact_duplicate_count
likely_repost_count
status
~~~

Suggested collection_run_jobs fields:

~~~text
run_id
job_id
classification
is_new
created_at
~~~

Collection-run persistence is intentionally after the core triage work.

## Database changes

Minimum first migration:

~~~text
jobs.review_state TEXT NOT NULL DEFAULT 'UNREVIEWED'
jobs.review_reason TEXT
jobs.reviewed_at TEXT
~~~

Recommended backfill:

1. jobs with an existing application record → SHORTLISTED;
2. jobs without an application record → UNREVIEWED.

Do not infer SKIPPED from the absence of an application.

## Store API

Suggested operations:

~~~text
SetJobReviewState(jobID, state, reason)
BulkSetJobReviewState(jobIDs, state, reason)
ListJobsByReviewState(...)
CountJobsByReviewState(...)
~~~

The store layer must validate review states.

## HTTP / UI actions

Suggested routes:

~~~text
POST /app/jobs/<job_id>/review-state
POST /app/jobs/bulk/review-state
POST /app/jobs/bulk/start-applications
~~~

Example bulk payload:

~~~text
review_state=SHORTLISTED|LATER|SKIPPED
job_id=<repeated ids>
review_reason=<optional>
~~~

Cross-page selection can later replace repeated IDs with a query-backed selection payload.

# Implementation order

## Phase 1 — Persistent triage state

Goal: make Skip, Shortlist, and Later real persisted decisions.

- [ ] add review_state, review_reason, reviewed_at to the jobs schema;
- [ ] add migration and backfill;
- [ ] add model fields/constants;
- [ ] add store methods and validation;
- [ ] add unit tests.

Acceptance:

- restart does not lose triage decisions;
- existing applications backfill to SHORTLISTED;
- SKIPPED remains a deliberate persistent state.

## Phase 2 — Jobs Inbox and triage UI

Goal: separate deciding from applying.

- [ ] default Jobs view to UNREVIEWED Inbox;
- [ ] add Inbox / Shortlisted / Later / Skipped / All Jobs navigation;
- [ ] replace primary Inbox Queue/Process actions with Shortlist/Later/Skip;
- [ ] add single-job triage actions;
- [ ] make a triaged row leave Inbox immediately;
- [ ] show counts per review state;
- [ ] keep All Jobs as complete searchable history.

Acceptance:

- skipped jobs no longer appear in Inbox;
- skipped jobs do not appear in application execution previews;
- shortlisted jobs are visibly separate from unreviewed jobs.

## Phase 3 — Cross-page selection

Goal: make pagination a display concern rather than a decision boundary.

- [ ] persist selection across page navigation;
- [ ] show selected-across-pages count;
- [ ] add Select visible;
- [ ] add Select all matching;
- [ ] add Clear selection;
- [ ] define filter-change behavior;
- [ ] add regression/browser tests.

Acceptance:

- selection made on page 1 remains after moving to page 2;
- bulk triage can operate beyond one 50-row page;
- selection scope is always explicit.

## Phase 4 — Shortlist to Applications handoff

Goal: execution starts only after a positive job decision.

- [ ] add Start Applications to Shortlisted;
- [ ] preview Email / Easy Apply / Needs Attention counts;
- [ ] reuse existing application queue creation logic;
- [ ] keep application creation idempotent;
- [ ] remove Inbox direct Process Selected as the default workflow;
- [ ] preserve bounded provider-operation limits downstream.

Acceptance:

- UNREVIEWED, LATER, and SKIPPED cannot enter the normal application batch path;
- SHORTLISTED jobs can be handed off in bulk;
- existing application records are not duplicated.

## Phase 5 — Collection run handoff

Goal: each collection has an explicit review entry point.

- [ ] persist collection runs;
- [ ] associate newly collected jobs with a run;
- [ ] add Review N New Jobs CTA;
- [ ] support latest-run and historical-run review;
- [ ] add collection completion summary.

Acceptance:

- after collect, the user can review exactly the new jobs from that run;
- old jobs are not mixed into new-job review unless deliberately requested.

## Phase 6 — Triage analytics

Goal: use decisions as visibility and feedback, not silent automation.

- [ ] optional skip reasons;
- [ ] review funnel counts;
- [ ] skip-reason summary;
- [ ] collector-quality insights.

No automatic query mutation or automatic skipping is included in this phase.

## Dashboard target

Recommended funnel:

~~~text
New to Review
Shortlisted
Later
Ready to Apply
Drafts to Review
Awaiting Send
Applied / Sent
Needs Attention
~~~

The dashboard should answer:

> What should I do next?

## Non-goals

This milestone does not add:

- automatic job rejection by AI;
- automatic application submission;
- automatic LinkedIn Easy Apply;
- automatic Gmail sending;
- automatic collector-query modification;
- deletion of skipped jobs;
- replacement of the existing application audit/history model.

## Migration safety rules

1. Never delete a job because it is skipped.
2. Never infer that an old un-applied job was previously skipped.
3. Never erase protected application history when job intent changes.
4. Keep collector dedup/history independent from review_state.
5. Keep review_state independent from application lifecycle state.
6. Keep web mutations CSRF protected.
7. Keep provider/network limits separate from local triage limits.

## Product definition of done

The milestone is complete when the normal experience is:

~~~text
Collect
→ Review only new/unreviewed jobs
→ Shortlist / Later / Skip
→ skipped jobs disappear from active review
→ shortlist can span multiple pages
→ Start Applications from Shortlisted
→ execute Email / Easy Apply workflows
→ track application history
~~~

At that point Jobs is a clear **decision workspace**, while Applications is a clear **execution workspace**.
