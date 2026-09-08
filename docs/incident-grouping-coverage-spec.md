# Why related alerts are not grouped, and what to do about it

Part of epic #34655. Written 2026-09-08, after checking real data on the Rackspace tenant.

## What is broken

When several alerts fire on the same machine, we should show them as one incident. Mostly
we do not.

In the last 24 hours on Rackspace, 19 machines had more than one kind of alert firing at
the same time. For 16 of them we grouped nothing at all.

Here is one case. EC2 instance `i-0dcee3621b8456783` has two alerts firing:

| alert | started | times it has fired |
|---|---|---|
| order service down | 4 Sep, 13:46 | 21 |
| order service CPU high | 5 Sep, 09:02 | 9 |

Both are still firing today. They are clearly the same problem on the same machine. They
are not grouped, and they show up as two separate rows in the inbox.

## Why it happens

Three separate reasons, and a fourth covered in the next section. Fixing any one of them
alone does not help, because each is enough to block grouping on its own.

**1. We only try to group an alert once, the first time we ever see it.**

After that we never try again. These two alerts first fired on 4 and 5 September, so the
grouping check ran days ago and will never run again. This is what blocks 10 of the 16
ungrouped machines.

The check also never gets a second chance, because we only treat an alert as "new" again
after it has been silent for a full day. These alerts fire every few minutes, so they never
go quiet long enough.

**2. The two alerts started 19 hours apart.**

We only group alerts that start within 15 minutes of each other. So even on that first
attempt, days ago, these two would not have been grouped. They were never eligible.

**3. The machine an alert points at can change while the alert is still firing.**

The "order service down" alert first pointed at a service called `order`. On 6 September it
started pointing at the EC2 instance instead. That change was deliberate and correct: we
made AWS alerts point at the real resource so they could be correlated (PR #37758).

But grouping looks at what the alert pointed at when it *first* fired, which is the old
value. The inbox shows the current value. So on screen two alerts look like they are on the
same machine, while grouping thinks they are on different ones.

## Connected services are barely grouped at all

We already know which services talk to each other. Every alert carries that map with it.
For the five machines in the scenario lab it says:

- order, payment and inventory all call **database** (port 5432)
- payment and inventory also call **order** (port 8081)
- the **load balancer** routes to order

All five are alerting right now, and we show them as nine separate rows.

We do have a rule that groups alerts on connected machines, and it has worked here. On
7 September the database alert joined the order alert's group, and the reason recorded on
the link reads "calls edge between i-0150dbd583caa0e69 and i-0dcee3621b8456783 within
incident attach window".

That is the only connection-based link ever created for this account. It worked because the
database alert was new at that moment. Everything else has been firing for days, so reason 1
above blocks it too.

Three further limits apply even when it does run:

1. **Connections are only checked if same-machine grouping found nothing.** It is a
   fallback, not a rule in its own right.
2. **Only one hop is followed.** If a calls b and b calls c, a and c are never grouped with
   each other.
3. **Only one neighbour is joined** — whichever alerted most recently. Nothing takes a set
   of connected services that are all alerting and makes them one incident.

So if a, b and c are connected and all three are alerting, they do not reliably end up in
one group today. Whether they do is luck.

## Making the time window bigger will not work

The obvious fix is to widen the 15-minute window. The data says no.

Over 14 days on Rackspace there are 1.84 million pairs of different alerts on the same
machine. Grouped by how far apart they started:

| started within | pairs |
|---|---|
| 15 minutes (what we use today) | 2,632 |
| 90 minutes | 16,052 |
| 6 hours | 64,641 |
| 24 hours | 252,483 |

Going from 15 minutes to 24 hours pulls in 96 times more pairs. The typical gap between two
alerts on the same machine is 4 days, so most of those pairs have nothing to do with each
other. A wider window would group unrelated things.

There is a much better test. Pairs where **both alerts are still firing right now**: 17.
That is the set an operator actually means when they say "these are happening together".

## Decided: the noise rate no longer decides who is in a group

We mark an alert as "routine noise" when it fires 10 or more times a week. Today a routine
alert can neither start a group nor join one.

We first proposed letting it join but not start. Checking the real firing rates killed that
idea. Per machine, right now:

| machine | its two alerts | could anything start a group? |
|---|---|---|
| payment | 17 and 11 a week, both routine | no |
| inventory | 15 and 11 a week, both routine | no |
| order | 16 routine, 9 not routine | yes, barely |

Two of the three machines in the screenshot would not have changed at all. The third works
only because one counter sits at 9 against a threshold of 10, and would stop working after
one more firing.

**Decided: any alert that is currently firing can be in a group.** The noise rate is still
used, but only to decide which alert leads the group and how high the group ranks. A noisy
alert will not become the headline. It will no longer stop the incident existing.

## Rejected: treating an alert as new when its machine changes

This was going to fix the machine-name drift. It would flood the inbox instead.

Over 7 days, 22 alerts changed machine 2,091 times. The worst is an ECS task alert that
changes on *every single firing* — 603 changes across 603 different machines — because the
alert is defined per task family while the machine is the individual task. Today that
collapses into one row. Under this change it becomes 603 separate new alerts.

Rejected. See `docs/architecture-decisions.md`.

## What we would change

Stop asking "did these two alerts start close together". Start asking "are these two alerts
both firing right now".

1. **Check for grouping every time an alert fires, not only the first time.** The link is
   recorded against the alert's original entry, so we still keep one row per alert rather
   than one per firing. *Survived both review passes.*
2. **Look for partners that fired recently, instead of alerts that started recently.**
   Today we search the last 90 minutes for alerts that *began* in that period. Instead we
   search the last 15 minutes for any firing at all, then treat the alert it belongs to as
   the candidate. This is a smaller search than today's, not a bigger one, and an alert can
   join however long ago it began. *Survived both review passes.*
3. **Group on the machine's stable identity, not its name.** *Agreed.* We look the machine
   up in the graph and group on the graph's id for it. That id stays the same across the
   `order` to instance-id rename, and across ECS task churn. It fixes the drift without
   touching how we collapse repeat alerts. There is already a shared lookup for this, and
   its own comment warns that the same mismatch has been fixed three times in three places
   by three one-offs — we use it rather than writing a fourth.
4. **Group the whole connected set, not one neighbour.** *Agreed, and redesigned — see
   below.* Include a connected service only if it is also alerting inside the same window,
   follow at most two hops, and cap group size.

## Solved: read the graph from the database, not from the alert

The problem was that each alert carries its own copy of the graph, built around its own
machine, so two alerts could disagree about who is connected and there would be no single
answer for a group.

The fix is to stop reading the copy attached to the alert. The graph is already in the same
database. Every alert reads the same graph, so every member of a group works out the same
set of connected services, and the group has one answer regardless of which alert is
processed first.

**Start from what is alerting, not from the graph.** A review pass found that walking the
graph and then filtering by what is alerting is the wrong way round. Two hops limits how far
we go but not how much we touch: one machine in this estate has 1,047 connections, and two
hops from it reaches 1,108 others — of which 2 were alerting. We would look at a thousand
machines to find two, on every alert.

Inverted, it costs almost nothing. Take the machines with an alert in the last 15 minutes,
which is a small list, then check which of those are within two hops of the machine we
started from. Both versions were run against the live data and return the same four
machines.

Result for the order machine:

| distance | service | alerts firing |
|---|---|---|
| itself | order | 2 |
| one hop | payment | 2 |
| one hop | inventory | 2 |
| one hop | database | 1 |

The load balancer, two network interfaces and two external IP addresses are also one hop
away and are correctly left out, because nothing is alerting on them.

That is exactly the group that should exist, and it is the nine rows in the screenshot
reduced to one incident.

**Which alert leads the group.** Every member now works out the same set, but that is not
the same as agreeing who leads it, and the set grows as more services start alerting. If
each new member picked a leader from the current set, a group could end up with two. So a
new member always joins the group that already exists, and only elects a leader when there
is none. This is what the existing code already does, and we keep it rather than replacing
it.

**Size cap: 20 machines.** Across 14 days the number of machines alerting at once in a
15-minute window averages 5, with 25 at the 99th percentile and 29 at worst — and that is
the whole account, not one connected set. A group that would exceed 20 machines is a
fleet-wide event rather than an incident, so we stop adding rather than let it grow.

## Three things found while testing this

1. **The load balancer would be missed.** Its alert calls it
   `app/nudgebee-scenario-lab-alb/68aa7cf1396199f4`, while the graph knows it by a short
   name and by its full ARN. Matching an alert to a graph entry has to go through the shared
   lookup, which handles both forms, and load balancers need checking specifically.
2. **The same machine can appear twice in the graph.** Walking out and back reached a stale
   second entry for the order machine. Grouping must treat entries as the same machine when
   they share a resource id, not just when they share a graph id.
3. **The lookup that finds a machine in the graph has no index.** It searches a JSON field
   over roughly 33,000 rows. That is acceptable today because it runs occasionally; running
   it on every alert firing needs an index first. Note that this index has to be created by
   hand on each database before the change ships, because our migrations cannot create
   indexes without locking the table.

## When a group ends

A group is over when every alert in it has been quiet for longer than the 15-minute window.
A firing after that starts a new group.

This replaces the current rule, which closes a group 90 minutes after it started regardless
of what is still happening.

## How we will know it worked

- The three EC2 instances in the example each show their "down" and "CPU" alerts as one
  incident.
- The count of "machines with more than one alert firing and nothing grouped" over a rolling
  24 hours drops from 16 towards 0.
- The scenario lab's connected machines (order, payment, inventory, database, and the load
  balancer once its naming is handled), all alerting at once, appear as one incident rather
  than nine rows.
- During a multi-service incident no group exceeds 20 machines, and the number is reached by
  stopping rather than by truncating an existing group.
- No group contains two different machines, and no group contains alerts that were not both
  firing at the same time.
- Grouping stays inside the time budget for processing an alert, including on the noisiest
  alerts.

## Technical notes

- Grouping runs in `ProcessEvent` step 3b (`api-server/services/triage/processor.go`), gated
  on `occurrence == 1`.
- The attach rule is `decideSameSubjectAttach` in
  `api-server/services/triage/incident_group.go`. `IncidentAttachWindow` is 15 minutes and
  `IncidentAbsorptionCap` is 90 minutes. Note that the cap is also the range bound on the
  candidate query, so removing it needs a replacement bound, not just deletion.
- The chain only resets after a silent gap longer than `DefaultDedupWindow` (24 hours), in
  `detectAndRecordDuplicate`.
- Routine classification is `ChronicWeeklyThreshold = 10` in
  `api-server/services/triage/chronic.go`. The spike escape is `IsBursting`, floored at
  `chronicBurstMinCount = 3` firings in the trailing hour.
- Chronic candidates are dropped from `active` inside `decideSameSubjectAttach`, which is
  why a non-chronic alert finds nothing to attach to.
- The subject change came from commit `c7eab5cef4` (PR #37758, 5 Sep). The fingerprint is
  the CloudWatch alarm ARN and did not change, which is why one chain spans three different
  subjects.
- The connection rule is `tryTopologyAttach` in `incident_group.go`. It runs only when the
  same-subject path returns no attach, requires `getDependencyDistance == 1` in either
  direction, and picks the single most recently active neighbour subject.
- The graph reaches it through `parseServiceMapFromEvent`, which reads the
  `knowledge_graph` evidence card. That card is populated correctly on this account: nodes
  for all four scenario instances plus the ALB, and CALLS edges between them from VPC flow
  logs.
- Account used for all figures: `00666e9f-3774-4f5d-b86c-60b201ae18c5`.
