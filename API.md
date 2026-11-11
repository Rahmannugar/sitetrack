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

View the company store.
`GET /company-store`

Filter, sort and paginate company store stock.
`GET /company-store?search=cement&category_id=:category_id&status=low_stock&sort=quantity&order=desc&limit=20&cursor=:cursor`

View company stock for a material.
`GET /company-store/:material_id`

Record stock received from a supplier.
`POST /company-store/:material_id/add-stock`

Set a material’s company reorder point.
`PUT /company-store/:material_id/reorder-point`

## Deliveries

List company deliveries.
`GET /company-store/deliveries`

Filter and paginate company deliveries.
`GET /company-store/deliveries?project_id=PRJ-0001&direction=company_to_project&status=in_transit&from=2026-11-01&to=2026-11-30&limit=20&cursor=:cursor`

Send materials between the company store and a project store.
`POST /deliveries`

View a delivery.
`GET /deliveries/:delivery_id`

Cancel a delivery as its sender.
`POST /deliveries/:delivery_id/cancel`

Receive a delivery into your store.
`POST /deliveries/:delivery_id/receive`

Reject a delivery as its receiver.
`POST /deliveries/:delivery_id/reject`

List project deliveries.
`GET /projects/:id/deliveries`

Filter and paginate project deliveries.
`GET /projects/:id/deliveries?direction=project_to_company&status=delivered&from=2026-11-01&to=2026-11-30&limit=20&cursor=:cursor`

## Health

Check database readiness.
`GET /health/ready`

Report whether the API process is serving requests.
`GET /health/live`

## Invitations

Invite a registered user to a project for seven days.
`POST /projects/:id/invitations`

List project invitations.
`GET /projects/:id/invitations`

Filter and paginate project invitations.
`GET /projects/:id/invitations?status=pending&limit=20&cursor=:cursor`

Withdraw a pending invitation.
`DELETE /projects/:id/invitations/:invitation_id`

Accept or reject your unexpired invitation.
`POST /invitations/:invitation_id/respond`

## Materials

Create a material category as a company store manager or SuperAdmin.
`POST /material-categories`

List material categories.
`GET /material-categories`

Search and paginate material categories.
`GET /material-categories?search=cement&limit=20&cursor=:cursor`

View a material category.
`GET /material-categories/:id`

Update a material category as a company store manager or SuperAdmin.
`PUT /material-categories/:id`

Remove a material category as a company store manager or SuperAdmin.
`DELETE /material-categories/:id`

Create a material as a company store manager or SuperAdmin.
`POST /materials`

List materials.
`GET /materials`

Search, filter and paginate materials.
`GET /materials?search=cement&category_id=:category_id&limit=20&cursor=:cursor`

View a material.
`GET /materials/:id`

Update a material as a company store manager or SuperAdmin.
`PUT /materials/:id`

Remove a material as a company store manager or SuperAdmin.
`DELETE /materials/:id`

## Notifications

List your notifications.
`GET /notifications`

Paginate your notifications.
`GET /notifications?limit=20&cursor=:cursor`

Count your unread notifications.
`GET /notifications/unread-count`

Mark all your notifications as read.
`PATCH /notifications/read-all`

Stream your notification updates.
`GET /notifications/stream`

Mark a notification as read.
`PATCH /notifications/:id/read`

## Project store

View the project store.
`GET /projects/:id/store`

Search, filter, sort and paginate project store stock.
`GET /projects/:id/store?search=cement&category_id=:category_id&sort=last_delivery&order=desc&limit=20&cursor=:cursor`

View project stock for a material.
`GET /projects/:id/store/:material_id`

## Projects

Create a project.
`POST /projects`

List projects.
`GET /projects`

Search, filter, sort and paginate projects.
`GET /projects?search=bridge&late_only=true&sort=name&limit=20&cursor=:cursor`

View a project.
`GET /projects/:id`

Replace a project's name and planned dates.
`PUT /projects/:id`

Remove a project.
`DELETE /projects/:id`

List project members.
`GET /projects/:id/members`

Paginate project members.
`GET /projects/:id/members?limit=20&cursor=:cursor`

Change another member's project admin status.
`PATCH /projects/:id/members/:user_id`

Remove another project member.
`DELETE /projects/:id/members/:user_id`

## Tasks

Create a project task as a project admin.
`POST /projects/:id/tasks`

List project tasks.
`GET /projects/:id/tasks`

Search, filter, sort and paginate project tasks.
`GET /projects/:id/tasks?search=slab&status=not_started&late=true&blocked=true&from_date=2026-11-01&to_date=2026-11-30&sort=start_date&order=asc&limit=20&cursor=:cursor`

Read a task and its allowed actions.
`GET /projects/:id/tasks/:task_id`

Update a task’s name and dates as a project admin.
`PUT /projects/:id/tasks/:task_id`

Remove a task as a project admin.
`DELETE /projects/:id/tasks/:task_id`

Mark a task as done as a project admin.
`POST /projects/:id/tasks/:task_id/done`

Reopen a task as a project admin.
`POST /projects/:id/tasks/:task_id/reopen`

Start a task as a project admin.
`POST /projects/:id/tasks/:task_id/start`

Add a task dependency as a project admin.
`POST /projects/:id/tasks/:task_id/dependencies`

List tasks this task depends on.
`GET /projects/:id/tasks/:task_id/dependencies`

Paginate the tasks this task depends on.
`GET /projects/:id/tasks/:task_id/dependencies?limit=20&cursor=:cursor`

Remove a task dependency as a project admin.
`DELETE /projects/:id/tasks/:task_id/dependencies/:required_task_id`

List all tasks this task depends on, or tasks depending on it.
`GET /projects/:id/tasks/:task_id/relationships`

Choose a dependency direction and paginate the results.
`GET /projects/:id/tasks/:task_id/relationships?direction=dependents&limit=20&cursor=:cursor`
