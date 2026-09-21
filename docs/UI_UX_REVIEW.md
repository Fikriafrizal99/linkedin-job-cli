# UI/UX review and implementation — September 21, 2026

Scope: every major `/app` route, both application channels, list selection/filtering, review/send confirmation, missing records, empty queues, collector results/errors, CV readiness, and desktop/mobile navigation. Reviewed `UI_REFERENCE.md`, `APPLICATION_WORKBENCH.md`, the UI handlers, lifecycle store, and existing UI tests before implementation.

## Audit findings and decisions

| Priority | Finding | Implemented decision |
| --- | --- | --- |
| P0 | Sidebar disappeared on mobile with no replacement. Settings kept a 210px rail and squeezed its content. | Native disclosure navigation, full-width stacked Settings, wrapping controls and header actions. |
| P0 | Workbench promoted Confirm Send with zero or ineligible selections. | Selection-aware eligibility, limits, disabled reasons, and one primary action corresponding to selected lifecycle states. |
| P0 | Opening LinkedIn left the current page in its old state; next-three errors were swallowed. | Explicit opening records progress through the existing CSRF handler, updates the page after success, and explains popup/network failures. Manual confirmation remains required. |
| P0 | Unsupported records showed empty email/editor/provider panels before their blocker. | Display verified-destination guidance first and render email/provider content only when it exists. |
| P1 | Detail tabs suggested nonexistent views. Detail/queue pages had weak exits. | Remove fake tabs; add explicit workflow links and filter-preserving back navigation. |
| P1 | Long lists, anonymous checkboxes and equally prominent batch explanations hindered scanning. | 50-row pages, sticky table headers/selection column, row labels, count/selection feedback, date filters, and progressive batch options. |
| P1 | Queue Selected versus Process Selected required documentation. | Short before-click explanation: save for later versus create email drafts/queue manual LinkedIn work. Preview selected channel counts and disclose preselected portfolio files. |
| P1 | Review controls competed with reading; provider identifiers dominated. | Reading column plus decision rail, explicit Gmail attachment verification, optional note disclosure and quieter Previous/Skip controls. |
| P1 | Easy Apply resembled email automation; next-three competed with the single job. | Four manual steps, early Open LinkedIn action, CV recommendation, gated completion and optional next-three disclosure. |
| P1 | Dashboard showed a long selected email and an email-only “sent” KPI that undercounted completed Easy Apply work. | Replace the email preview with actionable queues and use a completed total that combines email SENT + LinkedIn APPLIED, with channel breakdown. |
| P1 | Collector showed green “progress” before any work ran. | Honest ready/running/result text, duplicate-submission lock, result counts and direct Browse Jobs link. |
| P1 | Read-only settings looked editable; CV paths dominated. | Render preferences as read-only text, support URL-addressable settings sections, distinguish default/missing CVs and disclose storage paths. |
| P2 | Technical lifecycle labels, low-contrast secondary text, unlabeled inputs and inconsistent focus. | Shared friendly labels, explicit state titles, stronger text contrast, focus outlines, skip link, associated labels and live action feedback. |

## Running decision log

1. Preserve the dark navy/blue command-center identity and server-side rendering.
2. Keep persisted states, channel routing, limits, confirmation fields and lifecycle store logic intact.
3. Extract static HTML/CSS/JS into embedded `cmd/templates/` files; retain lightweight Go templates and native browser controls.
4. Use URLs for filter/page context and Settings navigation. Batch redirects preserve the active list filters.
5. Show only relevant batch actions after selection; mixed-state selections still use the existing backend skip behavior.
6. Treat LinkedIn opening and successful submission as separate events. Never infer submission from an opened tab.
7. Verify real-data pages read-only; perform mutation checks only in an isolated temporary fixture with external tabs stubbed and no send route.
8. Browser testing caught and fixed double-encoding of detail-link query parameters. Add a specific regression test.

## Intentional presentation/interaction changes

- Lists display 50 records per page. Select all visible applies only to that page; process/draft/send caps remain 25 and queue/prepare/review caps remain 50.
- Jobs can filter by collected-since date; Applications can filter by updated-since date. Existing search covers company; method/state values stay unchanged.
- Status text is human readable (for example `DRAFT_CREATED` → Draft Ready); the technical state remains in badge titles and backend values.
- Supported batch actions are disabled until eligible records are selected. Draft creation becomes primary for prepared selections when Gmail is connected.
- Easy Apply records local opening only after a tab was reserved successfully; failed/blocked opens are reported. APPLIED still requires IN_PROGRESS plus explicit local confirmation.
- Approval/send/recreation buttons become available after their existing confirmation checkbox is checked. All server checks remain authoritative.
- Missing routes/records return a navigable 404 page rather than raw 500 text.
- Dashboard's full email preview is replaced by the operational next-actions panel.

## Safety and validation boundaries

No lifecycle/store changes. CSRF, recipient requirements, deterministic CV selection, optional attachment selection, Gmail draft/recreation guards, approval, final send confirmation and manual Easy Apply confirmation remain in place. No email was sent and no LinkedIn application was filled or submitted during this review. Existing staged changes in `cmd/jobs_batch_ui_test.go` were preserved; only a separate unstaged copy assertion was updated.

The baseline and each implementation stage were checked with `go test ./...`. Final verification includes `go test ./...`, `go vet ./...`, `go build ./...`, and opt-in Chromium browser tests. Tests requiring loopback sockets run outside the restricted sandbox.

Browser coverage at **1536×1024**, **1366×768**, and **390×844** includes all major routes plus email preparation/draft/approved/sent variants, manual applied/in-progress variants, missing CVs, empty search results, collector success/error pages, invalid LinkedIn URL, missing-page errors and final send confirmation. The sweep checks page overflow, associated form labels, duplicate IDs and JavaScript exceptions, and captures screenshots when requested.

Interactive checks cover selection limits, mixed-state action eligibility, filter/page return navigation, review keyboard editing, Approve & Next, explicit send gate (without sending), blocked popup feedback, local Easy Apply opening and confirmation, mobile menu dismissal, and duplicate collector submission prevention. External LinkedIn tabs are stubbed in interaction tests.

### Reproduce browser verification

Run the fixture in one terminal; it uses temporary SQLite/settings/CV files and exposes **no send or collector execution route**:

```sh
go test ./cmd -run TestUIVisualFixture -v -timeout 2h \
  -args -ui-fixture-addr=127.0.0.1:18083
```

Run the browser checks in another terminal with Chromium available:

```sh
go test ./cmd -run TestUIBrowserWorkflows -v -timeout 3m \
  -args -ui-browser-url=http://127.0.0.1:18083 \
  -ui-chrome=/path/to/chromium \
  -ui-screens=/tmp/job-ui-audit/browser-check
```

Start a fresh fixture for each interactive run because the tests approve and mark fixture records applied. Both tests skip in normal `go test ./...` runs.

Screenshots from this session are in `/tmp/job-ui-audit/`: `before/`, `final-real/`, and `browser-check/`. They are local review artifacts, not committed binaries.

## Remaining UX debt

- Collector exposes a synchronous result, not streamed per-stage progress. The UI deliberately reports running work without inventing percentages.
- Gmail attachment inventory and edits are not synced back into SQLite; human review must check the actual Gmail draft.
- Candidate preferences remain read-only in the web UI. Editing the existing settings file is still required.
- List paging currently slices the existing in-memory store results; database-level pagination is a separate performance improvement for very large collections.
- Chromium was verified; native popup behavior and assistive-technology behavior should also be checked in other browsers/platforms before broader distribution.
- Live Gmail creation/sending and live LinkedIn submissions were intentionally not exercised by this UI review; existing provider/lifecycle regression coverage remains intact.


## Multi-query / multi-location collection

The collector supports batch discovery without concatenating unrelated job titles into one LinkedIn query. The browser accepts one query and one location per line, previews the query × location search count, and blocks batches above 50 combinations. The CLI preserves the existing positional keyword syntax while adding repeatable `--query` and `--location` flags. `--top` is explicitly per search combination. Existing LinkedIn-ID and structural duplicate handling remains authoritative, so repeated results across queries/locations do not create duplicate stored jobs.
