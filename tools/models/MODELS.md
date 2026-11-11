# State models

Run `task models` with Java 21 or newer available as `java`, or set `REGEL_JAVA` to
its executable path. The task downloads TLC 2.19 once, verifies its SHA-256 and runs
Regel. Generated `.tla`/`.cfg` files and expected counterexample `.trace.txt` files go
to `bin/models`, which is gitignored. Regel is pinned in the tools module; the TLC
release and checksum are pinned in setup.sh. A failed download/checksum stops the task.
The API and worker do not import Regel or require Java.

| Model | Bounds | Invariants |
| --- | --- | --- |
| Deliveries | Company, two projects, two materials, three delivery slots; store capacity 3; initial stock 2 per material; up to two supplier additions | Nonnegative stock/transit; conservation per material; transit matches pending deliveries; at most one terminal action; stock plus outbound transit fits capacity |
| TaskDependencies | One project with three tasks, every directed dependency pair except self, three task states; paths through three edges | No cycles; stored waiting/satisfied state matches the required task; started/done tasks have only completed dependencies, including the dependency chain |
| ProjectMembership | Three existing members, initially two admins; promotion, demotion and removal of other members | At least one admin; every admin remains a member |

Each step represents one committed transaction. Rejected requests leave the state
unchanged. A delivery slot in `new` represents an absent record, not another stored
application status. The models explore repeated requests and all reachable action orders within
these bounds, rather than one scripted lifecycle. They do not assert that people will
eventually receive deliveries or finish tasks.

Delivery transitions correspond to [the service](../../internal/deliveries/service.go)
and [stock SQL](../../internal/deliveries/queries/deliveries.sql). The small capacity
stands for the numeric quantity bound. Two material lines move together. The project
and ordered material locks justify the transaction-level serialization assumption;
this model does not simulate lock acquisition or prove freedom from SQL deadlocks.

Task transitions correspond to [the database guards](../../internal/infra/database/migrations/010_domain_enums.sql).
Membership transitions correspond to [the locked membership service](../../internal/projects/membership_service.go).
Task/store actors are assumed to have passed current access checks. Membership actions
require a current admin and a different target. Access isolation, role revocation,
SQL constraints, rollback, decimal precision and actual locking are covered separately
by the PostgreSQL integration tests, not proved by these models.

The same task checks six intentionally broken variants. It requires a specific invariant
to fail, with a counterexample: split stock check/debit, repeated receipt, stock additions
ignoring return space, missing dependency-state synchronization, unchecked reopening,
and stale admin checks. Tool errors and missing TLC/Java fail the task; they are never
accepted as counterexamples or silently skipped.

These are bounded design checks, not a proof that the Go/SQL implementation is correct
for every input or scale. Full-scale seeds, query plans and k6 remain separate work.

Regel API: [sodiqscript111/Regel](https://github.com/sodiqscript111/Regel).
TLC: [TLA+ tools v1.7.4](https://github.com/tlaplus/tlaplus/releases/tag/v1.7.4).

Verified locally using the bundled Temurin Java 25 runtime: the correct delivery,
task-dependency and membership models explore 105, 267 and 19 distinct states respectively.
All six broken variants produce their named counterexamples. These counts describe the
current bounds and transition definitions only.
