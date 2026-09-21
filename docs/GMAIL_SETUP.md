# Gmail Setup for LinkedIn Job CLI

The local Job Command Center can create Gmail drafts natively through the Gmail API.

It does **not** ask for or store your Gmail password.

## What the app uses

- OAuth 2.0 Desktop application flow.
- Loopback callback on the local UI server.
- Scope: `https://www.googleapis.com/auth/gmail.compose`.
- Gmail API endpoint: `users.me.drafts.create`.
- Draft creation only at this stage; sending remains a separate explicit lifecycle action.

## 1. Enable Gmail API

In Google Cloud Console:

1. Create or select a project.
2. Enable the **Gmail API**.
3. Configure the OAuth consent screen.
4. If the app is still in testing mode, add the Gmail account you will use as a test user.

## 2. Create OAuth credentials

Create an OAuth client with application type:

```text
Desktop app
```

Download the client credentials JSON.

## 3. Save the credentials file

Default path:

```text
~/.linkedin-jobs/gmail-credentials.json
```

Optional override:

```bash
export LJ_GMAIL_CREDENTIALS_FILE=/custom/path/credentials.json
```

The OAuth token is stored at:

```text
~/.linkedin-jobs/gmail-token.json
```

Optional override:

```bash
export LJ_GMAIL_TOKEN_FILE=/custom/path/token.json
```

The token file is written with private `0600` permissions.

## 4. Connect Gmail from the UI

Start the local server:

```bash
go run . serve
```

Open:

```text
http://127.0.0.1:8080/app/settings?tab=email
```

Then:

1. Confirm that the OAuth credentials file is detected.
2. Click **Connect Gmail**.
3. Complete Google consent in the browser.
4. Google redirects back to the local loopback callback.
5. The Settings page should show **CONNECTED**.

## 5. Ensure the CV path is real

Every prepared application references a configured CV profile. Native Gmail draft creation attaches the actual local file.

Example:

```yaml
application:
  candidate_name: "Mochamad Fikri Afrizal"
  default_cv_profile: general
  cv_profiles:
    - id: general
      path: "/home/fikri/Documents/CV_Mochamad_Fikri_Afrizal.pdf"
      priority: 1
```

Do not leave a placeholder such as:

```text
/path/to/CV.pdf
```

The CV Profiles page shows:

- `FILE READY` when the configured file is accessible.
- `FILE MISSING` when the path cannot be used.

Application Detail will not expose **Create Gmail Draft** while the selected CV file is missing.

## 6. Create a draft

A record must satisfy all of these conditions:

```text
State       = READY_EMAIL
Recipient   = present
Subject     = prepared
Body        = prepared
CV profile  = configured
CV file     = accessible
Gmail       = connected
```

Then Application Detail exposes:

```text
Create Gmail Draft
```

On success:

```text
READY_EMAIL
    ↓
Gmail drafts.create
    ↓
gmail_draft_id persisted
    ↓
DRAFT_CREATED
```

The email is **not sent**.

## Security boundaries

- Gmail password is never requested.
- OAuth state and PKCE are used during browser authorization.
- OAuth authorization is restricted to the local loopback UI.
- Token files are stored locally with private permissions.
- Draft creation is explicit.
- Sending remains a separate explicit action.
- No auto-send or follow-up automation is enabled.

## Official Google references

- OAuth 2.0 for Desktop Apps:
  https://developers.google.com/identity/protocols/oauth2/native-app
- Gmail scopes:
  https://developers.google.com/workspace/gmail/api/auth/scopes
- Gmail draft creation:
  https://developers.google.com/workspace/gmail/api/guides/drafts
