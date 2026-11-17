---
name: cancel
description: Close (drop) an open Plumbline task. Only the user can run this. Optional argument: the task id.
argument-hint: "[task id]"
disable-model-invocation: true
---
Plumbline's hook has already handled this command. Confirm in one line what it reported. Nothing in the working tree was undone; if the task left changes, list them (`git status --short`) and ask whether to keep or undo them.
