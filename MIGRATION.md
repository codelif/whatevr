# Protocol Migration Ledger

**End goal (definition of finished):** `whatevrd` serves the full
[PROTOCOL.md](PROTOCOL.md) surface (except the explicitly deferred
`notifications` view), `whatkevr` runs entirely on it, the gRPC server,
`proto/`, and the qt6-grpc/protobuf dependencies are deleted from the tree,
build files, packaging, and README, `examples/` contains the ~30-line shell
frontend, the conformance script passes, and PROTOCOL.md is promoted from
DRAFT to stable. When every step below is `done`, the migration is over —
there is no step N+1.

**Strategy:** strangler fig. The new socket server is built *alongside* the
existing gRPC stack; whatkevr keeps working on gRPC until Phase D ports it
page by page; teardown (Phase E) deletes the old stack. At no point is the
app broken for daily use.

This file is the **only** progress tracker. Every work session updates it in
the same commit as the code it describes. If this file and reality disagree,
fixing that is the first order of business.

## Session protocol

One session = one step. Each session (see `.claude/commands/migrate.md`):

1. Read `PROTOCOL.md` and this file. Report to the user: current step,
   previous session's notes, open blockers/decisions.
2. If the next step is `blocked` or `needs-decision`, resolve that first
   (ask the user); otherwise take the **first step not marked `done`**.
3. If the step is too big for one session, **split it in the table** (e.g.
   B3 → B3a/B3b) instead of pushing through — then do the first half.
4. Implement only that step. Write/extend tests as part of it.
5. Verify: `just build`, tests, and the conformance script (once A3 exists)
   must pass; walk the Spirit checklist below.
6. Update this file — status, one-line note on what/where, any new blockers
   or decisions — and commit code + ledger together.
7. Stop. Do not start the next step.

**Amending the protocol:** if implementation reveals PROTOCOL.md is wrong or
incomplete, do not silently drift. Mark the step `needs-decision`, describe
the proposed spec change in the Decision log, and get the user's sign-off
before changing PROTOCOL.md.

## Spirit checklist (audited every step, mechanically where possible)

- Frontend code gained **no** sorting, merging, dedup, or cache-invalidation
  logic — ordering comes only from daemon `sort` keys.
- Command handlers return acks/ids only; anything renderable reaches clients
  only through a view.
- Every upsert carries `id` + `sort`; every message item carries `fallback`;
  media crosses only as file paths.
- Grammar unchanged: `subscribe`/`extend`/`unsubscribe`,
  `upsert`/`remove`/`ready`/`reset`. An item that feels too big to upsert
  means *split the view*, never patch the grammar.
- Nothing added is tailored to one specific frontend.
- The wire stayed hand-usable: the step's feature can be exercised via
  `socat` by hand.

## Steps

Status: `todo` | `doing` | `done` | `blocked` | `needs-decision`

### Phase A — foundations (daemon, Go)

| id | step | status | notes |
| --- | --- | --- | --- |
| A1 | Wire core: NDJSON framing, per-connection read/dispatch/write loops, `hello` (protocol negotiation), error envelope + core codes, socket setup alongside gRPC (own path per PROTOCOL.md), unit tests over a raw socket | done | `whatevrd/internal/protocol/` (protocol.go, conn.go, server.go) + raw-socket tests; socket at `$XDG_RUNTIME_DIR/whatevr/whatevrd.sock` next to gRPC. Protocol mismatch rejects with core `invalid_params` then closes (spec names no dedicated code). systemd activation for the new socket lands with the packaging flip at teardown |
| A2 | View engine: subscription registry, generic view interface (initial fill → ready, live upsert/remove, opaque sort keys, windows + extend), unsubscribe/connection-drop cleanup, per-connection outbound queue with upsert coalescing + `reset` fallback; tested against a dummy in-memory view | done | `queue.go` (coalescing outbound queue, per-sub overflow→purge+`reset`), `sub.go` (window diff engine, refill+`ready` after reset), `view.go` (View/ViewSession interface, `Server.RegisterView`, subscribe/extend/unsubscribe). Views expose ordered `Items(max)` + invalidate; engine owns diffing/windows, so B-phase views stay dumb. Item `id`-inside-`item` is a View-contract obligation the engine doesn't enforce — A3 conformance should assert it. Readies coalesce across rapid extends (spec: ready covers the *latest* subscribe/extend) |
| A3 | Conformance harness + example frontend: `scripts/conformance` asserting grammar invariants against a live daemon (response→upserts→`ready` ordering, `sort` on every upsert, single response per id, hello negotiation, unknown-method error); `examples/` shell frontend (socat+jq) started | todo | |

### Phase B — views (read path over the existing store/wa layers)

| id | step | status | notes |
| --- | --- | --- | --- |
| B1 | `connection`, `sync`, `login` views (login subscribe attaches/starts QR flow) | todo | |
| B2 | `chats` view: filters, archived, windowing with remove-on-fall-out, pinned+recency sort keys | todo | |
| B3 | `messages` view: anchors (`latest`/`unread`/message id), extend older, live edge, revoke-as-upsert, delete-as-remove; `kind` + `fallback` on every stored message | todo | |
| B4 | `typing`, `presence` (subscription-driven upstream WA presence subscribe), `receipts` | todo | |
| B5 | `self`, `contact`, `group`, `group_members` — two-phase local→network upserts | todo | |
| B6 | `privacy`, `preferences`, `blocklist`, `starred`, `pinned` | todo | |
| B7 | `stickers`, `sticker_packs`, `sticker_pack`, `transfers` | todo | |

### Phase C — commands & queries

| id | step | status | notes |
| --- | --- | --- | --- |
| C1 | Session/chat commands: `session.update`, `daemon.reconnect`, `account.logout`, `chat.*` (mark_read, pin, archive, mute, typing, request_older, ensure_direct) | todo | |
| C2 | `send.*`, `message.*` (react, edit, revoke, delete, star, pin, forward), `media.download` (+`transfers` wiring), `media.fetch_profile_picture` | todo | |
| C3 | `privacy.set`, `preferences.set`, `self.set_about`, `contact.block`, sticker commands, queries (`search.chats`, `search.messages`, `contacts.check_phone`), `open_chat` connection-directed routing | todo | |
| C4 | **Daemon audit milestone:** finish `examples/` shell frontend as a real usable client; run full conformance; line-by-line diff of PROTOCOL.md vs daemon; fix drift or log `needs-decision` items | todo | |

### Phase D — whatkevr port (C++/QML, page by page; gRPC stays alive until D7)

| id | step | status | notes |
| --- | --- | --- | --- |
| D1 | Qt client core: socket transport + dispatcher, generic keyed/sorted `QAbstractListModel` over a collection view, object-view wrapper; no UI changes yet | todo | |
| D2 | Port connection/status/login pages + chat list (incl. `typing` overlay) | todo | |
| D3 | Port conversation view: messages, receipts dialog, presence header, jump-to-message anchors | todo | |
| D4 | Port composer + all send paths, media download/`transfers` progress, image viewer | todo | |
| D5 | Port info dialogs (contact/group/members), starred/pinned pages, unified + in-chat search | todo | |
| D6 | Port settings pages (privacy/prefs/blocklist/profile) + sticker/emoji pickers | todo | |
| D7 | Remove all gRPC client code + qt6-grpc from whatkevr; whatkevr runs 100% on the new protocol | todo | |

### Phase E — teardown & flagship polish

| id | step | status | notes |
| --- | --- | --- | --- |
| E1 | Delete daemon gRPC server + `proto/`; purge protobuf/grpc from go.mod, CMake, justfile, packaging, README dependency lists | todo | |
| E2 | **Final audit + release polish:** full PROTOCOL.md vs implementation pass; promote PROTOCOL.md DRAFT → v1 stable; rewrite README around the daemon/protocol as the flagship feature (30-line-frontend pitch, `examples/` front and center) | todo | |

## Blockers

_None._

## Decision log

- 2026-07-06 — Envelope: bespoke minimal (no `jsonrpc` field, string error
  codes). Decided by Harsh.
- 2026-07-06 — Granularity rule: split views, never extend the grammar;
  `group`/`group_members` and `chats`/`typing` splits. Decided by Harsh.
- 2026-07-06 — `notifications` view deferred; explicitly **not** part of
  this migration's end goal (PROTOCOL.md open question).
