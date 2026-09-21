# UI Reference

This document defines the canonical visual target for the local LinkedIn Job CLI web application.

![Job Command Center](./ui-reference/job-command-center.webp)

## Fidelity target

Implement the dashboard to match this reference **1:1 visually** at the 1536×1024 desktop reference viewport, including:

- overall layout and proportions;
- sidebar and top navigation;
- spacing, padding, radii, borders, and shadows;
- dark navy surfaces and contrast hierarchy;
- typography scale and information hierarchy;
- KPI cards;
- recent-jobs table density and status badges;
- right-side Application Detail panel;
- quick-action cards and button placement.

The committed image is a high-quality, same-dimension WebP reference copy of the approved 1536×1024 design.

## Functional mapping

The visual design must be backed by the existing repository workflow rather than mock-only data:

- **Jobs Collected** → collector / SQLite jobs;
- **With Email Contact** → explicit extracted application emails;
- **Application Pipeline** → `applications` lifecycle;
- **Application Detail** → selected job + application record;
- **CV Profile** → configured deterministic CV profile;
- **Open in Gmail** → persisted Gmail draft when available;
- **Collect Jobs** → existing collector workflow;
- **Applications** → `READY_EMAIL`, `DRAFT_CREATED`, `APPROVED`, and `SENT`.

## Safety / product constraints

- `Send (Optional)` remains an explicit, manual action.
- Never auto-send email.
- Never auto-apply on LinkedIn.
- Never auto-DM or auto-connect.
- Follow-up tracking is intentionally out of scope.

## Reference integrity

- Reference viewport: **1536×1024**
- Repository asset: `docs/ui-reference/job-command-center.webp`
- SHA-256: `a7d334448937971455bec92e7dd43b3991a6b368b79bf936b543d38f920ad2f3`

Treat this image as the source of truth for future UI implementation unless the user explicitly approves a visual change.
