---
name: wrong
description: Mark the last Plumbline block as wrong, so rules that cause friction without catching anything get fixed. Only the user can run this. Optional argument: a short note.
argument-hint: "[why it was wrong]"
disable-model-invocation: true
---
Plumbline's hook has already recorded this. Confirm in one line which rule was marked. If the block stopped real work, ask the user how to go on (for example /plumbline:pause for this step). Never suggest marking blocks wrong yourself.
