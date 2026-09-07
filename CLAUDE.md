# DRS — project instructions

## Keep the design docs current

`DRS docs/` holds living documentation that must track the code:

| Doc | Covers |
|---|---|
| `SYSTEM_DESIGN.md` | The whole system as built: components, endpoints, flows, data model, security, config, deployment, and what is still deferred. |
| `AGENT_WINDOWS.md` | The Windows agent: its tech stack, how it works, its limits. |
| `AGENT_ANDROID.md` | The Android agent: same. |

**Whenever a change lands that any of these describe, update the affected doc in the same
change.** That includes:

- a new or changed protocol message, endpoint, or WebSocket route
- a schema migration
- a new configuration variable, or a changed default
- a new capability, permission, or consent flag
- a change to the session, enrollment, presence, or teardown flow
- a new agent, or a new dependency/technology in an existing one
- anything that moves an item out of "deferred" in `SYSTEM_DESIGN.md` §9

Match the existing style: state what the code actually does now, explain *why* where the
reasoning is non-obvious (the codebase's own comments are the reference for tone), and keep
"as built" separate from "as planned". `DRS_Phase1_SRS_FR_NFR.md` and `DRS_Phase1_SDS.md` are
the original **plan** — they are historical and should not be edited to match reality; where
they disagree with the code, `SYSTEM_DESIGN.md` is authoritative.

If a change makes something in a doc wrong, fix it rather than appending a correction.
