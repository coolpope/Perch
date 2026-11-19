---
name: perch
description: Work with the person through Perch, the code viewer open beside this terminal. Use when they refer to code they are looking at ("this", "here", "these lines", "这里", "这段"), ask to be shown or walked through code or changes, or when you finish a change they will want to read.
---

<!-- Managed by Perch. Perch replaces this file when it updates. -->

# Working with Perch

Perch is the person's screen for code. The conversation stays in this terminal. Perch shows the code you are talking about.

## Read what they are looking at

Call `perch_context` before answering whenever they point at code without naming it. Treat the returned selection as the subject of their message. If nothing is selected, use the file and caret. If the selection is truncated, read the file for the rest. During a walkthrough, `tour` says which stop they are on.

## Show one place

Call `perch_show` when a single place in the code would make your answer easier to follow.

- Select the smallest range that makes the point.
- Write the note in the person's language, in one or two sentences. Say why this code matters, not what it literally does.

## Walk through a change

When they ask to be walked through code or a change, read the code first, then call `perch_tour` once with every stop.

- Order the stops so the change is easiest to understand, starting with the behaviour that changed.
- Keep to the places that matter, usually three to eight.
- Give every stop a note that says what to notice.
- Then tell them in one sentence that the walkthrough is open in Perch. They move through it there with Next and Previous, and come back here with questions.

After you finish a change that spans several files, offer a walkthrough. Do not start one unasked.

## Limits

Perch does not edit files through these tools. Make edits the usual way, and Perch updates by itself.
