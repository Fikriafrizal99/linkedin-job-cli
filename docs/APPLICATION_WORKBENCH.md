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

There is no automatic LinkedIn apply, automatic approval, or automatic email send.

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
- No auto-apply.
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

The new workbench flow is **implemented and pending live browser validation**.
