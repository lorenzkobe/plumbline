---
name: stats
description: Summarize Plumbline's local task logs, such as blocks per rule, send-backs, waivers and blocks marked wrong. Optional argument: number of months.
argument-hint: "[months]"
---
Run `plumbline stats` (add `--months N` if the user gave a number of months: "$ARGUMENTS") and explain it in a few plain lines. Point out rules that block often but were marked wrong a lot: those are candidates to rewrite (replace, don't patch). Logs hold no code, secrets or ticket text.
