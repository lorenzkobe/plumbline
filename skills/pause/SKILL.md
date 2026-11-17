---
name: pause
description: Pause Plumbline's task gates in this project. Only the user can run this.
disable-model-invocation: true
---
Plumbline's hook has already handled this command (it runs only when the user types it). Confirm in one line what the hook reported. While paused, the safety rules stay on: no production changes, no secrets printed, no pushes, no Jira posts without the user's OK. Remind the user that /plumbline:resume turns the gates back on.
