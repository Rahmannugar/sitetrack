# Load testing

## How to run

Run from the project folder with k6 installed.

```sh
# Verify all 65 endpoints and analyze their SQL.
task verify-endpoints DATABASE=sitetrack_scale

# Start the seeded application and test GET requests.
task load-up DATABASE=sitetrack_scale
task load

# Test delivery writes.
MODE=deliveries RATE=5 VUS=10 task load

# Analyze SQL separately.
task query-plans DATABASE=sitetrack_scale

# Restore normal operation.
task up
```

## Results

65 endpoints passed. 124 SQL statements analyzed.

| Load test | Requests | Expected p95 | Measured p95 | Expected p99 | Measured p99 |
| --- | ---: | ---: | ---: | ---: | ---: |
| GET requests | 2,113 | < 500 ms | 101 ms | < 1,000 ms | 119 ms |
| Delivery writes | 1,070 | < 1,000 ms | 26 ms | — | 39 ms |

Both tests ran for 60 seconds. Zero failed requests or dropped iterations.

Endpoint timings below are recorded verification times with SQL analysis enabled.

| Endpoint | HTTP status | Measured time |
| --- | ---: | ---: |
| `POST /auth/forgot-password` | 202 | 10.67 ms |
| `POST /auth/login` | 200 | 225.21 ms |
| `POST /auth/logout` | 200 | 4.65 ms |
| `GET /auth/me` | 200 | 2.69 ms |
| `POST /auth/refresh` | 200 | 55.20 ms |
| `POST /auth/resend-verification` | 202 | 2.81 ms |
| `POST /auth/reset-password` | 200 | 260.42 ms |
| `POST /auth/signup` | 201 | 268.12 ms |
| `POST /auth/verify-email` | 200 | 12.40 ms |
| `POST /company-store/:material_id/add-stock` | 200 | 12.04 ms |
| `PUT /company-store/:material_id/reorder-point` | 200 | 4.66 ms |
| `GET /company-store/:material_id` | 200 | 2.05 ms |
| `GET /company-store/deliveries` | 200 | 21.43 ms |
| `GET /company-store` | 200 | 7.74 ms |
| `POST /deliveries/:delivery_id/cancel` | 200 | 8.33 ms |
| `POST /deliveries/:delivery_id/receive` | 200 | 88.06 ms |
| `POST /deliveries/:delivery_id/reject` | 200 | 11.04 ms |
| `GET /deliveries/:delivery_id` | 200 | 8.65 ms |
| `POST /deliveries` | 201 | 21.39 ms |
| `GET /docs` | 200 | 0.31 ms |
| `GET /health/live` | 200 | 1.89 ms |
| `GET /health/ready` | 200 | 19.99 ms |
| `POST /invitations/:invitation_id/respond` | 200 | 19.54 ms |
| `DELETE /material-categories/:id` | 204 | 8.13 ms |
| `GET /material-categories/:id` | 200 | 3.47 ms |
| `PUT /material-categories/:id` | 200 | 3.82 ms |
| `GET /material-categories` | 200 | 5.19 ms |
| `POST /material-categories` | 201 | 8.95 ms |
| `DELETE /materials/:id` | 204 | 4.80 ms |
| `GET /materials/:id` | 200 | 2.33 ms |
| `PUT /materials/:id` | 200 | 7.84 ms |
| `GET /materials` | 200 | 7.31 ms |
| `POST /materials` | 201 | 14.70 ms |
| `PATCH /notifications/:id/read` | 204 | 39.04 ms |
| `PATCH /notifications/read-all` | 204 | 16.08 ms |
| `GET /notifications/stream` | 200 | 8.63 ms |
| `GET /notifications/unread-count` | 200 | 2.81 ms |
| `GET /notifications` | 200 | 14.03 ms |
| `GET /openapi.json` | 200 | 1.30 ms |
| `GET /projects/:id/deliveries` | 200 | 11.96 ms |
| `DELETE /projects/:id/invitations/:invitation_id` | 200 | 5.61 ms |
| `GET /projects/:id/invitations` | 200 | 8.07 ms |
| `POST /projects/:id/invitations` | 201 | 6.15 ms |
| `DELETE /projects/:id/members/:user_id` | 204 | 5.99 ms |
| `PATCH /projects/:id/members/:user_id` | 204 | 5.98 ms |
| `GET /projects/:id/members` | 200 | 5.64 ms |
| `GET /projects/:id/store/:material_id` | 200 | 8.09 ms |
| `GET /projects/:id/store` | 200 | 17.77 ms |
| `DELETE /projects/:id/tasks/:task_id/dependencies/:required_task_id` | 204 | 6.21 ms |
| `GET /projects/:id/tasks/:task_id/dependencies` | 200 | 6.14 ms |
| `POST /projects/:id/tasks/:task_id/dependencies` | 201 | 9.18 ms |
| `POST /projects/:id/tasks/:task_id/done` | 200 | 4.92 ms |
| `GET /projects/:id/tasks/:task_id/relationships` | 200 | 8.37 ms |
| `POST /projects/:id/tasks/:task_id/reopen` | 200 | 6.99 ms |
| `POST /projects/:id/tasks/:task_id/start` | 200 | 8.26 ms |
| `DELETE /projects/:id/tasks/:task_id` | 204 | 4.37 ms |
| `GET /projects/:id/tasks/:task_id` | 200 | 5.19 ms |
| `PUT /projects/:id/tasks/:task_id` | 200 | 7.78 ms |
| `GET /projects/:id/tasks` | 200 | 10.19 ms |
| `POST /projects/:id/tasks` | 201 | 5.58 ms |
| `DELETE /projects/:id` | 204 | 11.00 ms |
| `GET /projects/:id` | 200 | 2.21 ms |
| `PUT /projects/:id` | 200 | 15.58 ms |
| `GET /projects` | 200 | 92.63 ms |
| `POST /projects` | 201 | 37.99 ms |
