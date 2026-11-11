# SiteTrack API

## Authentication

Create an account.
`POST /auth/signup`

Verify an email address using its OTP.
`POST /auth/verify-email`

Request another verification code.
`POST /auth/resend-verification`

Log in.
`POST /auth/login`

Refresh your session.
`POST /auth/refresh`

Read your profile.
`GET /auth/me`

Log out.
`POST /auth/logout`

Request a password reset code.
`POST /auth/forgot-password`

Reset your password using the OTP.
`POST /auth/reset-password`

## Projects

Create a project.
`POST /projects`

List projects.
`GET /projects`

View a project.
`GET /projects/:id`

Replace a project's name and planned dates.
`PUT /projects/:id`

Remove a project.
`DELETE /projects/:id`

List project members.
`GET /projects/:id/members`

Change a member's project admin status.
`PATCH /projects/:id/members/:user_id`

Remove a project member.
`DELETE /projects/:id/members/:user_id`

## Tasks

Create a project task as a project admin.
`POST /projects/:id/tasks`

List project tasks.
`GET /projects/:id/tasks`

Read a task and its allowed actions.
`GET /projects/:id/tasks/:task_id`

Update a task’s name and dates as a project admin.
`PUT /projects/:id/tasks/:task_id`

Remove a task as a project admin.
`DELETE /projects/:id/tasks/:task_id`

Start a task as a project admin.
`POST /projects/:id/tasks/:task_id/start`

Mark a task as done as a project admin.
`POST /projects/:id/tasks/:task_id/done`

Reopen a task as a project admin.
`POST /projects/:id/tasks/:task_id/reopen`

List tasks this task depends on.
`GET /projects/:id/tasks/:task_id/dependencies`

Add a task dependency as a project admin.
`POST /projects/:id/tasks/:task_id/dependencies`

Remove a task dependency as a project admin.
`DELETE /projects/:id/tasks/:task_id/dependencies/:required_task_id`

List all tasks this task depends on, or tasks depending on it.
`GET /projects/:id/tasks/:task_id/relationships`

## Invitations

Invite a registered user to a project.
`POST /projects/:id/invitations`

List project invitations.
`GET /projects/:id/invitations`

Withdraw a pending invitation.
`DELETE /projects/:id/invitations/:invitation_id`

Accept or reject your invitation.
`POST /invitations/:invitation_id/respond`

## Notifications

List your notifications.
`GET /notifications`

Count your unread notifications.
`GET /notifications/unread-count`

Mark a notification as read.
`PATCH /notifications/:id/read`

Mark all your notifications as read.
`PATCH /notifications/read-all`

Stream your notification updates.
`GET /notifications/stream`

## Health

Report whether the API process is serving requests.
`GET /health/live`

Check database readiness.
`GET /health/ready`

## API reference

View the interactive API reference.
`GET /docs`

Download the OpenAPI specification.
`GET /openapi.json`
