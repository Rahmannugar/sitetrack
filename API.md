# SiteTrack API

## Authentication

Register an account and queue its verification email.
`POST /auth/signup`

Verify an email address using its OTP.
`POST /auth/verify-email`

Request another verification code.
`POST /auth/resend-verification`

Log in and replace the previous session.
`POST /auth/login`

Renew JWT access and rotate the refresh token.
`POST /auth/refresh`

Read your profile.
`GET /auth/me`

Revoke your current session.
`POST /auth/logout`

## Health

Report whether the API process is serving requests.
`GET /health/live`

Check whether PostgreSQL is reachable through the API's connection pool.
`GET /health/ready`

## API reference

View the interactive API reference.
`GET /api/docs`

Download the OpenAPI specification.
`GET /api/openapi.json`
