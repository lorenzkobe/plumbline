---
name: standup
description: Standup notes on what the user did since their last standup (or on a day they name), by ticket, across the repos that work touched; what's next from open tasks and Jira; and updates for their sprint file. Use when the user types /plumbline:standup or asks for standup notes.
argument-hint: "[day: since the last standup (default), or yesterday, Monday, Nov 12, 2026-11-12]"
---
# Standup notes

Day: "$ARGUMENTS" (empty means since the user's last standup here, up to now).

Only the user's own work, starting from the repo this session is in. A ticket that spans several repos (a backend, a frontend, another service) is one item, with each repo it touched or read. Repos with nothing to do with this work (a personal project the same day) stay out. Read-only: no commits, no Jira posts.

## 1. The time it covers
- No day given: `plumbline standup` covers the time since the last standup in this project, up to now, and remembers this one for the next. Asked again within 12 hours, it keeps the same start (the same standup redone). The first time it starts at the beginning of yesterday, and it never goes back more than 7 days. Run it once per standup.
- A day given: work out the date. "Yesterday" is the day before today, a weekday name is the most recent past one, and a date without a year is this year's (last year's if that date is still ahead). Run `plumbline standup --date YYYY-MM-DD`, which covers that whole day and doesn't change where the next standup starts.
Say in one line the time the listing covers (its first line).

## 2. What Plumbline and git know
The listing from step 1 shows, for that time:
- your tasks in this repo, and in other repos only the tasks on the same tickets;
- your commits (your git email only, so teammates' commits you pulled aren't yours), grouped by ticket;
- repos a task opened files in without changing them ("read in <repo>");
- tasks still open (what's next) and the tickets involved.

## 3. What MemPalace knows
`mempalace_search` with the `since` and `before` from the listing's "MemPalace window" line, once per ticket from the listing (and once for this repo's work if there's no ticket). It fills in what git doesn't show: what was investigated, decided or found. Keep only what belongs to those tickets or this repo; drop anything from other projects.

## 4. What's next
- Open tasks from the listing, with their step.
- Company project with the Jira part on: read the user's open assigned tickets, and comments that mention them or wait on their reply, with the Jira tools. Read only; never post, comment or move a ticket. Jira not connected: say so in one line and go on.

## 5. The sprint file
- `plumbline standup tracker` prints this project's sprint file, or "none".
- None: ask in one line where it is (or whether there is one). If the user gives a path, run `plumbline standup tracker --set <path>` (a confirm prompt appears). A file inside the repo must be ignored by git, so it stays local.
- Set: read it, then suggest updates for this work (tick what's done, add a line for work that isn't there, the ticket on each line). Show the change, then make it with the Edit tool; each edit asks the user first. Never add work that isn't in the listing or MemPalace.

## 6. The notes
A few plain lines the user can read out:
- **Since the last standup** (or the day): one line per ticket, with the ticket id when there is one, what was done and where (`PROJ-123: fixed the order filter (orders-service) and checked how the web app calls it`). Personal projects have no Jira: name the task instead.
- **Today**: what's next, from step 4.
- **Blockers**: anything waiting on someone (a task's "For you", a dispute, a question to a teammate), or "none".
No commit hashes or task ids unless asked. No AI markers, no em dashes, no filler.
