# SiteTrack API

## API reference

Download the OpenAPI specification.
`GET /openapi.json`

View the interactive API reference.
`GET /docs`

## Authentication

Create an account.
`POST /auth/signup`

Log in.
`POST /auth/login`

Log out.
`POST /auth/logout`

Read your profile.
`GET /auth/me`

Refresh your session.
`POST /auth/refresh`

Request a password reset code.
`POST /auth/forgot-password`

Request another verification code.
`POST /auth/resend-verification`

Reset your password using the OTP.
`POST /auth/reset-password`

Verify an email address using its OTP.
`POST /auth/verify-email`

## Company store

Record stock received from a supplier.
`POST /company-store/:material_id/add-stock`

Set a material’s company reorder point.
`PUT /company-store/:material_id/reorder-point`

View company stock for a material.
`GET /company-store/:material_id`

View the company store.
`GET /company-store`

## Health

Check database readiness.
`GET /health/ready`

Report whether the API process is serving requests.
`GET /health/live`

## Invitations

Accept or reject your invitation.
`POST /invitations/:invitation_id/respond`

Invite a registered user to a project.
`POST /projects/:id/invitations`

List project invitations.
`GET /projects/:id/invitations`

Withdraw a pending invitation.
`DELETE /projects/:id/invitations/:invitation_id`

## Materials

Create a material as a company store manager or SuperAdmin.
`POST /materials`

Create a material category as a company store manager or SuperAdmin.
`POST /material-categories`

List material categories.
`GET /material-categories`

List materials.
`GET /materials`

Remove a material as a company store manager or SuperAdmin.
`DELETE /materials/:id`

Remove a material category as a company store manager or SuperAdmin.
`DELETE /material-categories/:id`

Update a material as a company store manager or SuperAdmin.
`PUT /materials/:id`

Update a material category as a company store manager or SuperAdmin.
`PUT /material-categories/:id`

View a material category.
`GET /material-categories/:id`

View a material.
`GET /materials/:id`

## Notifications

Count your unread notifications.
`GET /notifications/unread-count`

List your notifications.
`GET /notifications`

Mark a notification as read.
`PATCH /notifications/:id/read`

Mark all your notifications as read.
`PATCH /notifications/read-all`

Stream your notification updates.
`GET /notifications/stream`

## Project store

View a project store.
`GET /projects/:id/store`

View project stock for a material.
`GET /projects/:id/store/:material_id`

## Projects

Change another member's project admin status.
`PATCH /projects/:id/members/:user_id`

Create a project.
`POST /projects`

List project members.
`GET /projects/:id/members`

List projects.
`GET /projects`

Remove another project member.
`DELETE /projects/:id/members/:user_id`

Remove a project.
`DELETE /projects/:id`

Replace a project's name and planned dates.
`PUT /projects/:id`

View a project.
`GET /projects/:id`

## Tasks

Add a task dependency as a project admin.
`POST /projects/:id/tasks/:task_id/dependencies`

Create a project task as a project admin.
`POST /projects/:id/tasks`

List all tasks this task depends on, or tasks depending on it.
`GET /projects/:id/tasks/:task_id/relationships`

List project tasks.
`GET /projects/:id/tasks`

List tasks this task depends on.
`GET /projects/:id/tasks/:task_id/dependencies`

Mark a task as done as a project admin.
`POST /projects/:id/tasks/:task_id/done`

Read a task and its allowed actions.
`GET /projects/:id/tasks/:task_id`

Remove a task as a project admin.
`DELETE /projects/:id/tasks/:task_id`

Remove a task dependency as a project admin.
`DELETE /projects/:id/tasks/:task_id/dependencies/:required_task_id`

Reopen a task as a project admin.
`POST /projects/:id/tasks/:task_id/reopen`

Start a task as a project admin.
`POST /projects/:id/tasks/:task_id/start`

Update a task’s name and dates as a project admin.
`PUT /projects/:id/tasks/:task_id`
