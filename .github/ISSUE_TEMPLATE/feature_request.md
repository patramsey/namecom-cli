---
name: Feature request
about: Suggest an addition or change to namecom
title: ''
labels: enhancement
assignees: ''
---

**What's the problem this solves?**

Describe the situation where namecom's current behavior falls short —
concrete examples (the command you wanted to run, the script you're
working around it with) help more than abstract descriptions.

**What would you like to see?**

**Alternatives you've considered**

Including `namecom api <METHOD> <path>` — the raw passthrough already
reaches any Core API endpoint, so if this is about an endpoint that simply
isn't wrapped yet, say so; that's a smaller ask than a new subsystem.

**Is this in scope?**

Check [CONTRIBUTING.md](../../CONTRIBUTING.md#scope) first. The interactive
TUI lives in a separate repository, and a few things (a persistent daemon,
declarative config-file sync, multi-registrar provider plugins) are bigger
conversations than a typical fix. If your request is one of these, the
issue is still welcome — just flag it as such so the discussion starts from
the right place.
