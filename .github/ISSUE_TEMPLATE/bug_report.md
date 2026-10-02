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

**`--debug` output**, if you can share it. It never prints the API token,
and it shows transfer auth codes and password- or token-like fields as
`[redacted]`. Read it before pasting anyway — it contains request and
response bodies, which include your domains and contact details, and the
redaction matches field names, so it cannot know about every secret.

```
namecom ... --debug
```

**Environment**
- `namecom` version: `namecom version`
- OS/platform:
- Install method: brew / go install / prebuilt binary
- Target: production or `--sandbox`
