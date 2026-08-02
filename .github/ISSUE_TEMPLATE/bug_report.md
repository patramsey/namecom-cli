---
name: Bug report
about: Something namecom did wrong or crashed
title: ''
labels: bug
assignees: ''
---

**Command run**

```
namecom ...
```

**What happened**

**What you expected instead**

**`--dry-run` output**, if this is a command that writes (register, renew,
DNS/email/URL/vanity create-update-delete, transfer, refund):

```
namecom ... --dry-run
```

**`--debug` output**, if you can share it. It redacts the API token, but
read it before pasting — it contains request and response bodies, which
may include your domains and contact details.

```
namecom ... --debug
```

**Environment**
- `namecom` version: `namecom version`
- OS/platform:
- Install method: brew / go install / prebuilt binary
- Target: production or `--sandbox`
