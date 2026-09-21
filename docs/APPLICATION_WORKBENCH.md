# Application Workbench

The **Applications** page is the operational workbench for processing many job applications without opening every record one by one.

The workbench keeps the existing lifecycle and human-review gates:

```text
READY_EMAIL
    ↓
Prepare
    ↓
Create Gmail Draft
    ↓
DRAFT_CREATED
    ↓
Human Review
    ↓
APPROVED
    ↓
Final Send Confirmation
    ↓
SENT
```

There is no automatic LinkedIn submission, automatic approval, or automatic email send.

Easy Apply uses a separate manual lifecycle:

```text
READY_EASY_APPLY
    ↓
Open LinkedIn Easy Apply
    ↓
IN_PROGRESS
    ↓
Human submits on LinkedIn
    ↓
Mark Applied & Next
    ↓
APPLIED
```

Opening the LinkedIn page never marks an application as APPLIED. APPLIED requires an explicit local confirmation after the user submits the form manually.

## Database Jobs → Review Queue

The Jobs database is now the intake surface for high-volume application processing. You no longer need to open and queue each collected job individually.

Routes:

```text
POST /app/jobs/bulk/queue
POST /app/jobs/bulk/process-to-draft
```

### Queue Selected

- select up to 50 visible jobs from the filtered Jobs table;
- only jobs without an existing application record are newly queued;
- explicit EMAIL + recipient becomes `READY_EMAIL`;
- LinkedIn / Easy Apply jobs become `READY_EASY_APPLY`;
- unsupported jobs without an email or Easy Apply path are skipped by default and remain only in Jobs;
- an explicit **Include unsupported jobs as NEED_REVIEW** checkbox is required to queue unsupported records into `NEED_REVIEW`;
- existing application records are skipped rather than overwritten;
- the current Jobs filters are preserved after the action.

### Process Selected

`Process Selected` is the fast path from the collector database into the correct channel-specific queue:

```text
selected Jobs
    ↓
route by application method
    ├── EMAIL
    │    ↓
    │ deterministic Prepare
    │    ↓
    │ Gmail drafts.create
    │    ↓
    │ DRAFT_CREATED → Review Queue
    │
    ├── EASY_APPLY
    │    ↓
    │ READY_EASY_APPLY
    │    ↓
    │ Easy Apply Queue
    │
    └── unsupported
         ↓
       skipped
```

Rules:

- maximum 25 selected jobs per Process Selected batch;
- existing `DRAFT_CREATED` records are not duplicated; they are added directly to the resulting Review Queue;
- existing `READY_EASY_APPLY` / `IN_PROGRESS` records are reused rather than duplicated;
- `APPROVED`, `SENT`, and `APPLIED` records are skipped;
- unsupported jobs are skipped unless explicitly queued through the NEED_REVIEW opt-in path;
- already-queued `NEED_REVIEW` records remain unchanged;
- the app never guesses a missing email recipient;
- supporting-file selections apply only to newly created Gmail drafts;
- Portfolio is preselected when configured, but remains user-controllable;
- Gmail is required only for EMAIL records. Easy Apply records can still be processed when Gmail is disconnected;
- the action never approves, sends email, fills LinkedIn forms, or submits LinkedIn applications.

If email drafts are present, the browser opens the Review Queue first and provides a link to the Easy Apply items from the same batch. If the batch contains only Easy Apply work, it opens the Easy Apply Queue directly.

## Easy Apply Queue

Route:

```text
GET  /app/applications/easy-apply
POST /app/applications/<job_id>/easy-apply/open
POST /app/applications/<job_id>/easy-apply/applied
```

The queue processes one LinkedIn Easy Apply record at a time without requiring the user to open each Application Detail page.

Each queue item shows:

- job title and company;
- lifecycle state;
- LinkedIn apply URL availability;
- opened timestamp;
- deterministic recommended CV profile and file readiness;
- Previous / Skip / Next navigation.

Actions:

- **Open LinkedIn Easy Apply ↗** opens the LinkedIn job in a new browser tab and changes `READY_EASY_APPLY → IN_PROGRESS`;
- **Open Next 3** is an explicit convenience action that opens up to the next three queue items in new tabs and marks those items IN_PROGRESS locally;
- **Mark Applied & Next** requires a confirmation checkbox stating that the user manually submitted the application on LinkedIn, stores the selected CV profile, changes `IN_PROGRESS → APPLIED`, removes that item from the active queue, and advances to the next original item;
- **Skip / Next** does not alter submission state.

Browser popup settings may block some tabs opened by **Open Next 3**. The application does not bypass popup controls.

Security and control boundaries:

- only LinkedIn job URLs are accepted by the Easy Apply open endpoint;
- no LinkedIn form fields are populated by this workflow;
- no submit button is clicked automatically;
- opening a tab is not treated as proof of submission;
- `APPLIED` is protected from Remove from Queue.

## Batch actions

The Applications table supports multi-select with **Select all visible**.

### Prepare Selected

Endpoint:

```text
POST /app/applications/bulk/prepare
```

Behavior:

- accepts up to 50 selected application records;
- processes only `READY_EMAIL` records;
- uses the existing deterministic CV-selection and email-preparation engine;
- stores subject, body, and CV profile;
- does not call Gmail;
- does not change the state beyond `READY_EMAIL`;
- skips lifecycle states that cannot be prepared;
- reports prepared / skipped / failed counts.

### Create Drafts

Endpoint:

```text
POST /app/applications/bulk/draft
```

Behavior:

- accepts up to 25 selected records per batch;
- processes only prepared `READY_EMAIL` records;
- creates Gmail drafts sequentially through the native Gmail client;
- uses the configured CV for each application;
- applies the supporting attachments selected in the Applications batch toolbar to every draft in that batch;
- Portfolio attachments are preselected in the UI and can be unchecked before running the batch;
- successful records advance to `DRAFT_CREATED`;
- failures remain unsent and are reported without stopping the rest of the batch;
- no email is sent.

## Review Queue

Routes:

```text
GET  /app/applications/review
POST /app/applications/bulk/review
POST /app/applications/<job_id>/approve
```

The queue removes the need to open Application Detail for every draft.

It shows one `DRAFT_CREATED` record at a time with:

- job title and company;
- recipient;
- subject;
- email body;
- CV profile;
- Gmail draft ID;
- link to Gmail Drafts;
- optional review note;
- explicit review-confirmation checkbox.

Actions:

- **Approve & Next** → stores the human review and moves to the next remaining draft;
- **Skip / Next** → leaves the record in `DRAFT_CREATED`;
- **Previous** → navigates within the same queue.

Keyboard navigation:

```text
J / →  next
K / ←  previous
```

The queue wraps so skipped drafts remain reachable.

Approval advances:

```text
DRAFT_CREATED → APPROVED
```

Approval does not send email.

## Remove from Queue

Applications that have not reached Gmail can be removed from the application pipeline without deleting the collected Job.

Routes:

```text
POST /app/applications/<job_id>/remove
POST /app/applications/bulk/remove
```

Rules:

- allowed for `READY_EMAIL`, `NEED_REVIEW`, `READY_EASY_APPLY`, and `IN_PROGRESS`;
- the collected job remains in the Jobs database and returns to `NOT_APPLIED` in the Jobs view;
- prepared subject/body/CV metadata is discarded with the application record where applicable;
- `DRAFT_CREATED`, `APPROVED`, `SENT`, and `APPLIED` are protected because provider/review/submission history already exists;
- bulk removal skips protected records rather than deleting them.

## Recover a deleted Gmail draft

If a Gmail draft is deleted directly in Gmail, the local application may still be `DRAFT_CREATED` or `APPROVED` with the old draft ID.

Open that Application Detail and expand **Draft missing or deleted? Recreate it**.

The recovery action:

```text
POST /app/applications/<job_id>/recreate-draft
```

reuses the saved prepared content and creates a replacement draft. Supporting attachments can be selected again.

Lifecycle:

```text
DRAFT_CREATED + deleted provider draft
        ↓ Recreate Gmail Draft
new Gmail draft
        ↓
DRAFT_CREATED
```

For an approved application:

```text
APPROVED + deleted provider draft
        ↓ Recreate Gmail Draft
new Gmail draft
        ↓
DRAFT_CREATED
        ↓
manual review required again
```

The old approval is deliberately cleared because it applied to the old draft, not the replacement. `SENT` records cannot use this recovery action.

## Explicit Send

The application can send an existing Gmail draft only after the record is already `APPROVED`.

Confirmation route:

```text
POST /app/applications/send-confirm
```

Actual send route:

```text
POST /app/applications/bulk/send
```

The confirmation page lists every send-ready item with:

- job/company;
- recipient;
- subject;
- Gmail draft ID.

The final form requires:

```text
☑ I confirm these approved applications are ready to be sent.
```

Only after that checkbox and the final **Send** button are submitted does the app call Gmail `drafts.send`.

Successful sends persist:

- Gmail message ID;
- Gmail thread ID when returned;
- sent timestamp;
- state `SENT`.

The Gmail provider action is executed sequentially and is limited to 25 applications per batch.

If Gmail sends a message successfully but the local SQLite state update fails, the batch reports that condition explicitly. Do not retry that item blindly because the provider may already have sent it.

## Bulk result summaries

After a batch operation the Applications page reports counts such as:

```text
Bulk prepare finished: 18 prepared, 2 skipped, 0 failed.
Bulk draft creation finished: 17 created, 1 skipped, 0 failed.
Explicit send finished: 12 sent, 0 skipped, 1 failed.
```

A first error is also surfaced when failures occur.

## Safety boundaries

- All write routes require the existing CSRF token.
- Lifecycle rules remain enforced by the store layer.
- Gmail draft creation is explicit.
- Approval requires a human confirmation checkbox.
- Sending requires a second, separate final confirmation checkbox.
- Bulk send only accepts `APPROVED` records with a Gmail draft ID.
- No auto-send.
- No LinkedIn form auto-fill.
- No auto-apply or auto-submit.
- No auto-DM or LinkedIn connection requests.
- Follow-up automation remains out of scope.
- Batch lifecycle operations are serialized by the local server to reduce conflicting updates.

## Current validation status

Backend/unit regression coverage is included for:

- Gmail draft send provider request;
- multi-select parsing;
- review queue filtering/navigation;
- Applications batch controls;
- Review Queue rendering;
- final send confirmation rendering.

The Applications batch workbench has been live-validated. The Jobs database bulk intake is implemented with regression coverage. The Easy Apply manual workflow is implemented with regression coverage and is pending live browser validation on the user's machine.
