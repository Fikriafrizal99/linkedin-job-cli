# UI Reference Suite

This document defines the canonical visual system for the local LinkedIn Job CLI web application.

The September 2026 workflow review is documented in [UI_UX_REVIEW.md](./UI_UX_REVIEW.md). Its implemented navigation, responsive layouts, action hierarchy and accessibility refinements supersede the historical dashboard email-preview layout while retaining this visual language.

## Master reference

![Full Application UI Reference](./ui-reference/full-application-ui-reference.svg)

The master board defines the **shared application shell and page family**. All pages must feel like one product: same sidebar, top navigation, spacing scale, surface colors, typography, controls, table density, badges, cards, and action hierarchy.

## Detailed Dashboard reference

![Job Command Center](./ui-reference/job-command-center.webp)

The Dashboard WebP remains the detailed **1:1 desktop fidelity reference** at 1536×1024. Use it for exact proportions, visual density, border treatment, shadows, panel spacing, and the Application Detail rail.

## Canonical page set

The UI implementation should contain these screens in this order:

1. **Dashboard**
   - KPI cards: Jobs Collected, With Email Contact, Application Pipeline, Applications Sent.
   - Recent Jobs table with job/company/status scanning.
   - Next actions panel: email review, preparation, manual Easy Apply, approved work and destination problems.
   - Quick Actions.
   - Primary action: Collect New Jobs.

2. **Jobs**
   - Search and filter toolbar.
   - Full collected-jobs database table.
   - Filters for location, method, application state, and collector metadata.
   - Multi-select with **Select all visible**.
   - Batch actions: **Queue Selected** and **Process Selected**.
   - The Apply cell distinguishes EMAIL, EASY_APPLY, and unsupported destinations.
   - Process Selected routes EMAIL jobs to Gmail draft review and EASY_APPLY jobs to the manual Easy Apply Queue; unsupported jobs are skipped by default.
   - No batch action approves, sends email, fills LinkedIn forms, or submits LinkedIn applications.
   - Row title navigation still opens Job Detail for one-record inspection.

3. **Job Detail**
   - Job title, company, location, posted date, application method, LinkedIn job id.
   - Tabs/sections for Overview, Description, Company, Application, and Notes where supported by existing data.
   - External LinkedIn action.
   - Application quick action only when supported by current workflow.

4. **Applications**
   - Application workbench / queue table.
   - Lifecycle filters cover both channels: `READY_EMAIL`, `DRAFT_CREATED`, `APPROVED`, `SENT`, `READY_EASY_APPLY`, `IN_PROGRESS`, and `APPLIED`.
   - Search/filter by company, method, state, and date.
   - Multi-select batch actions for Prepare, Create Drafts, Review, and Confirm Send.
   - Supporting-file selection for batch draft creation.
   - Review Draft Queue shortcut for sequential email review.
   - Easy Apply Queue shortcut for sequential manual LinkedIn application work.
   - Selecting a record still opens Application Detail.

5. **Application Detail**
   - Recipient, subject, email body, CV profile, Gmail draft id, review state, and provider metadata.
   - Tabs/sections for Email, CV & Files, Timeline, and Notes only where backed by real data.
   - Actions map directly to the existing lifecycle.
   - Sending remains explicit: APPROVED records must pass a separate final confirmation page before Gmail `drafts.send` is called.

6. **CV Profiles**
   - This is the single file-management page; do not add a separate Documents navigation item.
   - Upload or replace CV files directly from the browser.
   - Cards for configured CV profiles such as General, Sales, Finance, or future profiles.
   - Default profile indicator plus editable matching keywords and priority.
   - Additional Attachments section for portfolio, cover letter, certificates, and other supporting files.
   - Additional files are selected manually per application before creating the Gmail draft.

7. **Collect Jobs**
   - Search criteria form.
   - Keyword/title query.
   - Location.
   - Posted-within window.
   - Bounded collection options.
   - Start Collection primary action.
   - Collection progress/log panel.
   - Results should flow into the existing SQLite collector store.

8. **Settings**
   - General candidate/application preferences.
   - Collector configuration.
   - Email/Gmail bridge configuration where applicable.
   - CV defaults.
   - Database/runtime information.
   - Appearance/about sections may be present, but avoid fake settings that are not implemented.

## Global navigation

Use one persistent application shell:

```text
LinkedIn Job CLI

Dashboard
Jobs
Applications
CV Profiles

TOOLS
Collect Jobs
Settings
```

The active item uses the blue selected state shown in the references. The topbar remains persistent and contains global search, utility/settings access, and the local user identity treatment.

## Design system

### Visual language

- Dark navy application background.
- Slightly lighter elevated panels.
- Thin blue-gray borders.
- Restrained shadows.
- One primary blue accent.
- Semantic green, purple, amber, red, and neutral gray status colors.
- Rounded corners, but not oversized/pill-heavy.
- Dense professional table layouts.
- Clear hierarchy between page title, card title, body, and metadata.
- No decorative gradients or illustrations that break the command-center aesthetic.

### Core color direction

- App background: approximately `#071321` / `#0A1728`.
- Elevated panel: approximately `#102238`.
- Card/input surface: approximately `#12263D` / `#152A42`.
- Border: approximately `#223A55` / `#29435E`.
- Primary blue: approximately `#2E8BFF`.
- Success: approximately `#25C58B`.
- Draft/purple: approximately `#8D66FF`.
- Warning: approximately `#F3A23A`.
- Danger: approximately `#EF5D67`.
- Primary text: approximately `#E8F0FA`.
- Secondary text: approximately `#8799B2`.

These values are visual reference tokens, not a requirement to hard-code duplicate values throughout the codebase. Centralize them in the UI stylesheet.

### Shared component rules

The same component variants must be reused across pages:

- primary / secondary / ghost / danger buttons;
- text inputs and search inputs;
- select/filter controls;
- cards and elevated panels;
- data table header and row styles;
- pagination;
- status badges;
- detail metadata rows;
- tabs;
- empty, loading, and error states.

Do not redesign a component independently per page.

## Functional mapping

The visual design must be backed by the existing repository workflow rather than mock-only data:

- **Jobs Collected** → collector / SQLite jobs.
- **With Email Contact** → explicit extracted application emails.
- **Application Pipeline** → `applications` lifecycle.
- **Application Detail** → selected job + application record.
- **CV Profile** → configured deterministic CV profile.
- **Open in Gmail** → persisted Gmail draft when available.
- **Collect Jobs** → existing collector workflow.
- **Applications / email** → `READY_EMAIL`, `DRAFT_CREATED`, `APPROVED`, and `SENT`.
- **Applications / Easy Apply** → `READY_EASY_APPLY`, `IN_PROGRESS`, and `APPLIED`.

## Safety / product constraints

- `Send` remains a separate explicit/manual phase after review approval.
- Explicit send is wired only behind a dedicated final confirmation screen and checkbox.
- Never auto-send email.
- Never auto-fill or auto-submit LinkedIn applications.
- Easy Apply may open one tab or an explicitly requested next-three batch, but submission remains manual and APPLIED requires explicit confirmation.
- Never auto-DM or auto-connect.
- Follow-up tracking is intentionally out of scope.
- Do not expose actions in the UI that bypass lifecycle guards already implemented in the backend.

## Fidelity rules

- **Dashboard:** match `job-command-center.webp` as closely as practical at the 1536×1024 reference viewport.
- **Other pages:** derive from the master board while preserving the exact Dashboard design language.
- Sidebar width, topbar height, content gutters, card radius, table row height, and typography should remain consistent across pages.
- Responsive behavior may reflow panels on smaller screens, but desktop is the primary reference.
- Real application state must replace mock labels/content during implementation.

## Implementation status

### Phase 1 — complete

The repository now contains the first implementation of the complete page family under the existing `serve` command:

- `/app/dashboard`
- `/app/jobs`
- `/app/jobs/<job_id>`
- `/app/applications`
- `/app/applications/<job_id>`
- `/app/cv-profiles`
- `/app/collect`
- `/app/settings`

Dashboard, Jobs, Applications, CV Profiles, and Settings are populated from the existing SQLite/config data. Phase 3 wiring now covers collector, preparation, Gmail draft creation, CV/file management, manual review, application batch processing, and explicit Gmail sending behind a final human confirmation gate.

The former server-rendered jobs browser is retained at `/legacy` during migration so existing filtering/status/delete regression coverage is not discarded.

## Phase 3 — Functional wiring

### Collect Jobs — live validated

The Collect Jobs page now submits to a real CSRF-protected backend endpoint:

```text
POST /app/collect/run
```

The web action and CLI both use the same shared collector runner. The browser workflow is intentionally bounded:

- anonymous public LinkedIn collection only;
- keywords are required;
- maximum 100 results per web run;
- posted-within is validated through the existing collector parser;
- existing LinkedIn job IDs are skipped;
- structural dedup remains active;
- full detail/application extraction still runs;
- results are persisted to the existing SQLite store;
- session fallback and force-overwrite remain CLI-only;
- duplicate submissions are serialized by the local web server.

After completion, the page reports searched, new-candidate, persisted, exact-duplicate, and likely-repost counts.

This stage has been live-validated in the user's local browser and persists collected jobs into the existing SQLite-backed UI.

### Queue Application — live validated

Job Detail now exposes a real CSRF-protected `Queue Application` action:

```text
POST /app/jobs/<job_id>/queue
```

It delegates to the existing `Store.QueueApplication` lifecycle logic:

- explicit EMAIL + extracted recipient → `READY_EMAIL`;
- otherwise → `NEED_REVIEW`;
- existing `DRAFT_CREATED`, `APPROVED`, and `SENT` states remain protected by the store;
- no prepare, draft creation, approval, or send happens automatically.

After queueing, the UI redirects to Application Detail for the queued job. Job Detail changes from `Queue Application` to `View Application` once a lifecycle record exists.

`NEED_REVIEW` is now surfaced in application filters, pipeline counts, and status badges. The queue action has been live-validated in the user's local browser with both READY_EMAIL and NEED_REVIEW records visible in the application pipeline.

### Easy Apply manual workflow — implemented

LinkedIn / Easy Apply records use a channel-specific lifecycle:

```text
READY_EASY_APPLY → IN_PROGRESS → APPLIED
```

The queue is available at:

```text
/app/applications/easy-apply
```

It provides one-at-a-time navigation, deterministic CV recommendation, **Open LinkedIn Easy Apply ↗**, explicit **Open Next 3**, and **Mark Applied & Next**. Open actions only launch LinkedIn job URLs and record local progress; they do not fill or submit forms. Marking APPLIED requires a human-confirmation checkbox after manual submission.

This workflow has regression coverage and is pending live browser validation.

### Prepare Application — live validated

Application Detail now exposes a real preparation action for `READY_EMAIL` records:

```text
POST /app/applications/<job_id>/prepare
```

It delegates to the existing deterministic Application Engine:

- only `READY_EMAIL` is accepted by the web action;
- optional CV profile override can be selected from configured profiles;
- leaving the selector on Auto uses existing keyword scoring/default fallback;
- subject and email body are generated by the same `application.Prepare` logic as the CLI;
- preparation persists subject, body, and CV profile through `SaveApplicationPreparation`;
- lifecycle state remains `READY_EMAIL`;
- no Gmail draft is created;
- no email is sent;
- `NEED_REVIEW` records are blocked until recipient/contact data is resolved.

Re-preparation remains available while a record is still `READY_EMAIL`. Once it reaches `DRAFT_CREATED`, `APPROVED`, or `SENT`, existing backend lifecycle guards prevent silent re-preparation. The prepare action has been live-validated in the user's local browser.

### CV Profiles file management — live validated

File management stays inside `/app/cv-profiles`; there is no separate Documents page.

Implemented actions:

```text
POST /app/cv-profiles/upload
POST /app/cv-profiles/<id>/update
POST /app/cv-profiles/<id>/default
POST /app/cv-profiles/<id>/delete
POST /app/cv-profiles/attachments/upload
POST /app/cv-profiles/attachments/<id>/delete
```

Uploaded CVs and supporting files are stored under the local `~/.linkedin-jobs/files/` directory. Application Detail exposes configured supporting files as optional checkboxes and includes only the selected files when creating the Gmail draft.

CV upload/replace, additional supporting-file upload, the per-application optional attachment picker, and Gmail draft creation with selected optional attachments have all been live-validated in the user's local browser.

### Native Gmail OAuth + Draft Creation — live validated

The local web application now supports native Gmail OAuth and draft creation without depending on the ChatGPT Gmail connector.

Routes:

```text
POST /app/gmail/connect
GET  /app/gmail/oauth/callback
POST /app/gmail/disconnect
POST /app/applications/<job_id>/draft
```

Implementation boundaries:

- OAuth Desktop/loopback flow with PKCE and state;
- minimum Gmail scope: `gmail.compose`;
- credentials default to `~/.linkedin-jobs/gmail-credentials.json`;
- tokens default to `~/.linkedin-jobs/gmail-token.json` and are forced to `0600`;
- access tokens are refreshed using the stored refresh token;
- Gmail draft content is RFC/MIME with the configured CV attached;
- Gmail API `drafts.create` is called only from an explicit user action;
- successful Gmail draft IDs are persisted through `MarkApplicationDraftCreated`;
- state advances from `READY_EMAIL` to `DRAFT_CREATED`;
- no send occurs;
- the UI blocks draft creation if the selected CV file is missing;
- Gmail OAuth is restricted to a local loopback host.

Gmail OAuth connection and native Gmail draft creation have been live-validated in the user's local browser. Multi-attachment Gmail draft creation has also been live-validated with a CV plus portfolio attachment.

Draft polish now keeps managed storage IDs out of the outgoing MIME filename. Optional supporting files use a human-readable Gmail filename derived from the candidate name + attachment label, and the email body changes from “CV attached” to “CV and portfolio/supporting documents attached” when optional files are selected. This polish is implemented and pending live re-validation.

See `docs/GMAIL_SETUP.md` for setup instructions.

### Gmail Draft Recovery — implemented, pending live validation

Application Detail now handles the case where a Gmail draft was deleted outside the app while SQLite still stores the old draft ID.

Route:

```text
POST /app/applications/<job_id>/recreate-draft
```

Rules:

- available only for `DRAFT_CREATED` and `APPROVED`;
- requires CSRF and an explicit recreation confirmation checkbox;
- reuses the existing prepared recipient/subject/body/CV profile;
- supporting attachments are selected again; Portfolio is preselected when configured;
- the replacement Gmail draft is created before the local draft reference changes;
- successful recovery always ends in `DRAFT_CREATED`;
- recovery from `APPROVED` clears `reviewed_at` and `review_note` so the replacement must be reviewed again;
- `SENT` is never eligible;
- no email is sent by recovery.

This recovery flow is covered by regression tests and is pending live browser validation.


### Manual Review / Approval UI — live validated

Application Detail now exposes real review actions backed by the existing store lifecycle:

```text
POST /app/applications/<job_id>/approve
POST /app/applications/<job_id>/unapprove
```

Behavior:

- only a real `DRAFT_CREATED` application can be approved;
- the UI requires an explicit “I reviewed the Gmail draft…” confirmation;
- an optional review note is persisted (maximum 500 characters);
- approval records `reviewed_at` and advances `DRAFT_CREATED → APPROVED`;
- approval does not send email;
- an approved application can be explicitly reopened with `Unapprove & Reopen Review`;
- unapprove keeps the existing Gmail draft ID and returns `APPROVED → DRAFT_CREATED`;
- approval itself exposes no send side effect; sending remains a separate explicit confirmation flow.

This UI wiring is covered by regression tests and has been live-validated in the user's local browser.

### Jobs Database Bulk Intake — implemented, pending live validation

The collected Jobs page now supports direct multi-record intake into the application workflow.

Routes:

```text
POST /app/jobs/bulk/queue
POST /app/jobs/bulk/process-to-draft
```

Behavior:

- **Select all visible** selects only records currently rendered after filters;
- **Queue Selected** creates application records without opening Job Detail one by one;
- **Process Selected to Draft** chains Queue → deterministic Prepare → Gmail Draft for eligible records;
- Gmail-facing processing is capped at 25 selected jobs, while queue-only batches allow up to 50;
- jobs without an explicit email are skipped by default and stay in Jobs;
- Queue Selected exposes an explicit opt-in checkbox to add non-email jobs as `NEED_REVIEW`;
- Process Selected to Draft always skips newly selected non-email jobs rather than creating queue noise;
- existing `DRAFT_CREATED` applications are reused and included in the resulting Review Queue instead of creating duplicate drafts;
- `APPROVED` and `SENT` records are skipped;
- optional supporting-file selection is shared across drafts created by the batch;
- the browser redirects directly into Review Queue when at least one selected item is reviewable;
- no approval or email send occurs in this workflow;
- `READY_EMAIL` and `NEED_REVIEW` applications can be removed from queue while keeping the collected Job; draft/review/sent states remain protected.

This phase is covered by regression tests and is pending live browser validation.

### UI Hierarchy & Usability Audit — implemented, pending live validation

The shared UI was audited for information hierarchy, density, card sizing, action placement, and responsive behavior.

Implemented refinements:

- page titles now use a stronger visual level than page subtitles, with subtitles constrained to readable width;
- success/error feedback appears after the page heading so the page identity remains the first visual anchor;
- card headings, detail headings, labels, and metadata now use distinct type scales rather than competing sizes;
- content cards and panels use consistent padding, radius, border treatment, and restrained shadow depth;
- CV cards no longer enforce an unnecessarily tall minimum height;
- two-column detail pages and supporting grids align to the top instead of stretching cards vertically;
- tables allow job/company text to wrap while keeping operational metadata compact;
- selected batch rows receive a visible selected state;
- Jobs and Applications batch actions are grouped into a primary action row with secondary explanatory text below;
- filter toolbars wrap predictably at narrower widths;
- mobile/tablet layouts stack bulk actions and review navigation rather than compressing controls;
- Review Queue progress/navigation is visually separated from email content;
- duplicated page/card naming on CV Profiles was reduced (`CV Profiles` page → `Primary CV Library` content section).

These refinements keep the existing dark command-center visual system; they do not introduce a new visual identity.

### Application Workbench + Batch Workflow — batch flow live validated / explicit send pending live validation

The Applications page is now the high-throughput operating surface for multiple records.

Batch actions:

```text
POST /app/applications/bulk/prepare
POST /app/applications/bulk/draft
POST /app/applications/bulk/review
POST /app/applications/send-confirm
POST /app/applications/bulk/send
```

Review queue:

```text
GET /app/applications/review
```

Behavior:

- **Select all visible** plus per-row checkboxes avoid opening application details one by one;
- **Prepare Selected** runs the existing deterministic prepare engine across selected `READY_EMAIL` records;
- **Create Drafts** creates native Gmail drafts sequentially for selected prepared records;
- the batch toolbar exposes supporting-file checkboxes; Portfolio is preselected but remains user-controllable;
- **Review Selected** opens a sequential Review Queue;
- **Review Draft Queue** opens all current `DRAFT_CREATED` records;
- Review Queue supports Previous, Skip/Next, `Approve & Next`, and J/K or arrow-key navigation;
- approval stays a human decision and never sends email;
- approved rows can enter **Confirm Send**;
- the final confirmation page lists recipient, subject, job/company, and Gmail draft ID;
- an explicit final checkbox is required before calling Gmail `drafts.send`;
- successful provider sends are persisted as `SENT` with message/thread IDs;
- Gmail-facing draft/send batches are capped at 25 records; preparation/review selection is capped at 50;
- lifecycle writes are serialized in the local server to reduce conflicting updates.

There is still no auto-apply, auto-approval, or auto-send.

See `docs/APPLICATION_WORKBENCH.md` for the operating workflow and safety boundaries.

The batch workbench flow (selection, bulk prepare/draft controls, Review Queue, and Approve & Next) has been live-validated in the user's local browser. The final Gmail `drafts.send` action remains pending live validation because it sends a real email.



## Reference assets

- Detailed Dashboard: `docs/ui-reference/job-command-center.webp`
- Full application master board: `docs/ui-reference/full-application-ui-reference.svg`

Treat this suite as the source of truth for future UI implementation unless the user explicitly approves a visual change.
