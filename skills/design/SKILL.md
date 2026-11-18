---
name: design
description: Plumbline's design workflow. Use when the user types /plumbline:design, or asks for new or changed screens, flows or components whose look must be agreed before they are built.
argument-hint: "what to design (a screen, a flow, a component), or a ticket id"
---
# Design workflow

Design: $ARGUMENTS

Designs are agreed in mockups before any real screen is built. Plumbline holds that: a design task can't reach the checkpoint until the user has picked a direction on the question prompt, so no real screen is built before the pick. Everything else is the task workflow ([task skill](${CLAUDE_PLUGIN_ROOT}/skills/task/SKILL.md)); this skill adds the design steps in front of it.

## 1. Start
- Read the task (Jira ticket and every comment, as in the task workflow) and ask what's unclear in one message, each question with your suggestion.
- Designs from Figma: ask whether the user will share screenshots or connect the Figma MCP. Never guess what an image shows.
- Check the setup and the tools as in the task workflow, then start a design task: `plumbline task start --design --title "..." [--ticket X] --size small|normal|big --why "..."`, on its own line. A project with `"parts": {"design": false}` refuses it; use /plumbline:task then.

## 2. The brand kit
- The setup's `brand_kit` first, then the repo (design tokens, theme files, a style guide), then ask.
- A new project with no kit: decide on one together first (colours, type, spacing, corner radius, voice), each choice with your suggestion. Building the kit's files goes in the plan like any other file.
- Write it to `design.md` in the notes folder (`plumbline docs-dir`) under `## Brand kit`: where it is, or what was agreed.

## 3. Check the code
Trace the screens, components, styles and data the design touches (SocratiCode, Serena, open the files), as in the task workflow. Reuse the project's own components and tokens; note any that don't fit.

## 4. Three directions
- Make at least 3 different directions as mockups. Different means a different idea (layout, density, hierarchy), not three colourways.
- Where they go: the setup's `mockups` (Claude artifacts by default; a company project that doesn't allow them: local HTML in the scratchpad, opened in a browser).
- Use the ui-ux-pro-max skill if it's installed. If not, look up current design guidance online first (official platform guidelines, well-known design systems), without sending company code, ticket text or names.
- Phone first: each mockup shows the phone layout (about 360px wide) first, then tablet and desktop. No sideways page scroll, finger-sized tap targets, nothing that depends on hover, dialogs that fit with the keyboard up.
- Real content from the product (real labels, realistic names and numbers, long text), and the states: loading, empty, error, long text.
- Quality bar: Linear, Stripe, Notion, Arc. The brand kit sets the look; these set the level of care.
- Never (AI slop): purple and blue gradients, cards-in-a-grid on every screen, piles of emojis and icons, stock-looking illustrations, bland safe fonts, template layouts, generic wording ("Unlock your potential", "Seamlessly manage", "Elevate your workflow"), em dashes in UI text.
- List them in `design.md` under `## Directions`, one bullet each, straight under the heading: `- Direction A: <short name>, <link>, <what it's best at and what it gives up>`.

## 5. The pick
- Show the directions in chat (links, a line each, your recommendation and why).
- Ask with AskUserQuestion, header `Direction`, one option per direction with the label starting `Direction` (`Direction A: dense and fast`, `Direction B: calm and editorial`, ...). The user can type changes instead of picking. Never pre-answer it.
- Changes asked: refine the mockups (in place, same links), update `design.md`, and ask again. A pick counts only when it's exactly one of the direction options; the latest pick wins until the plan is approved.

## 6. Plan and checkpoint
Write `understanding.md` and `plan.md` as in the task workflow, plus:
- `## Design`: starts with the picked direction by its short name (`Direction B (link): ...`), then its mockup link, the screens, the sizes (phone, tablet, desktop) and the states. The checkpoint is refused if the section starts with anything else.
- `## Real-world proof`: `- ui: ...` is required: screenshots of the real screens at phone, tablet and desktop sizes, compared to the mockup.
- `## Checks`: the frontend checks that apply (the setup's `frontend_checks`; by default all): a visual check in a browser with screenshots against the mockup, screen sizes phone first, the states, accessibility basics (keyboard, contrast, labels), and end-to-end tests that click through the UI with Playwright (in the repo when allowed, local only when not).
- `## Team`: `ux-reviewer` (the checkpoint asks for it when the files are frontend).
Then the challenge (skipped for small tasks) and the checkpoint, as in the task workflow. To change the direction after approval: `plumbline task replan`, ask the direction again, then the checkpoint.

## 7. Build, prove, review, report
As in the task workflow. The `ui` proof runs against the real screens after the last edit (`plumbline run proof ui -- npx playwright test ...`). In the report, show the screenshots next to the mockup and list anything that differs from it, and why.
