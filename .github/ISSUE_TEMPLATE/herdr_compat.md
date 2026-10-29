---
name: herdr version compatibility
about: herdeck misbehaves with a newer or older herdr
labels: compat
---

herdr version / protocol (`herdr status`):

What breaks:

If you can, attach the output of
`printf '{"id":"1","method":"agent.list","params":{}}\n' | nc -U ~/.config/herdr/herdr.sock`
(scrub paths and names you don't want to share).
