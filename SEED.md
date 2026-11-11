# Seed data

The seed creates the full assessment dataset in an empty database. It does not
remove existing data. Run these commands from the project folder:

```sh
task db-up
export SITETRACK_SEED_DATABASE_URL='postgres://sitetrack:sitetrack@127.0.0.1:5433/sitetrack?sslmode=disable'
task seed
```

Use your configured port if it differs from 5433. Every seeded user is verified
and uses **SiteTrack123!**. No emails are sent.

| Data | Count |
| --- | ---: |
| Users | 5,000 |
| Projects | 2,000 |
| Invitations | 50,000 |
| Tasks | 500,000 |
| Dependencies | 1,000,000 |
| Materials | 20,000 |
| Deliveries | 400,000 |
| Delivery items | 3,000,000 |

Teams have up to 50 members, and two projects have 3,000 tasks each. Tasks,
dependencies, deliveries and stock balances are checked before the data is saved.
`bin/seed-manifest.json` records the counts and sample IDs used by k6.

The sample accounts are `superadmin@example.test`, `manager@example.test`,
`admin@example.test`, `member@example.test` and `outsider@example.test`.
The admin and member belong to the first project; the outsider has an invitation.

To see the counts, this command works from any folder. It checks `sitetrack_scale`,
the database used for our load tests. Replace it with `sitetrack` to check the
default database from the seed command above.

```sh
docker exec -i sitetrack-postgres-1 psql -U sitetrack -d sitetrack_scale <<'SQL'
SELECT 'users' AS entity, count(*) FROM sitetrack.users
UNION ALL SELECT 'projects', count(*) FROM sitetrack.projects
UNION ALL SELECT 'invitations', count(*) FROM sitetrack.project_invitations
UNION ALL SELECT 'tasks', count(*) FROM sitetrack.tasks
UNION ALL SELECT 'dependencies', count(*) FROM sitetrack.task_dependencies
UNION ALL SELECT 'materials', count(*) FROM sitetrack.materials
UNION ALL SELECT 'deliveries', count(*) FROM sitetrack.deliveries
UNION ALL SELECT 'delivery_items', count(*) FROM sitetrack.delivery_items;
SQL
```

The delivery counts increase when load tests create new deliveries. For an
interactive SQL session, run:

```sh
docker exec -it sitetrack-postgres-1 psql -U sitetrack -d sitetrack_scale
```
