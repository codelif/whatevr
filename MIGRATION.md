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
| A3 | Conformance harness + example frontend: `scripts/conformance` asserting grammar invariants against a live daemon (response→upserts→`ready` ordering, `sort` on every upsert, single response per id, hello negotiation, unknown-method error); `examples/` shell frontend (socat+jq) started | done | `scripts/conformance` starts `whatevrd/cmd/protocol-fixture` and asserts hello/errors/response ordering/`sort`/`item.id`/extend; `examples/shell-frontend.sh` starts the socat+jq chat-list frontend. |

### Phase B — views (read path over the existing store/wa layers)

| id | step | status | notes |
| --- | --- | --- | --- |
| B1 | `connection`, `sync`, `login` views (login subscribe attaches/starts QR flow) | done | `protocol.RegisterDaemonViews` serves daemon-backed object views with raw-socket tests; `connection` includes store-backed pending outgoing count, `login` attaches to QR events/expiry, `sync` maps history progress. |
| B2 | `chats` view: filters, archived, windowing with remove-on-fall-out, pinned+recency sort keys | done | `chats_view.go` (chatsView over `store.ListChatsForView`, `chatSort` = pinned/recency sections, invalidates on chat-affecting daemon events); `RegisterDaemonViews` now takes a `DaemonStore` (pending + chat lister). Sort inverts the timestamp so recency renders newest-first under ascending bytewise order (PROTOCOL example digits are illustrative — see Decision log). Item field names are idiomatic (`preview`/`unread`/`pinned`/`avatar_path`), `kind_hint` from the example omitted (no last-message-kind stored). Store + raw-socket tests. |
| B3a | `messages` view — `latest` anchor: live-edge windowing, extend-older, revoke-as-upsert, delete-as-remove; `kind` + `fallback` + full item shape on every stored message | done | `messages_view.go` (messagesView over `store.ListMessages`; `DaemonStore` gains `MessageLister`). Live-edge fit onto A2's prefix engine: `Items` returns the newest N **slice-ordered newest-first** (engine keeps the prefix = newest) while each item carries an **ascending** `%020d-%020d` timestamp/seq sort key (client renders oldest→newest) — slice order picks the window, sort key picks render order. Invalidates on new/updated/deleted/backfilled/cleared events filtered by `chat_id` (+ any avatar update). Revoke rides in as an ordinary upsert (`revoked:true`, content dropped); delete-for-me drops the row so the engine emits `remove`. `unread`/message-id anchors reject with `invalid_params` until B3b. Store + raw-socket tests; hand-verified over socat (fill newest-first, live-edge fall-out remove, extend-older, ready exhausted flip). |
| B3b | `messages` view — `unread` + `{message_id}` anchors: `anchor_id` subscribe meta, around-anchor (mid-sequence) windows (needs A2 engine support beyond the prefix window) | done | `messages_view.go`: anchored windows reuse A2's prefix engine **unchanged** — the session returns items ordered by *proximity to the anchor* (engine keeps the closest `window` as its prefix), each carrying the ascending timestamp sort key, so `extend` widens the contiguous neighborhood both directions. Reuses store `ListMessagesAround` (balanced split) + `ListMessagesAroundUnread` (resolves oldest-unread anchor from the chat's unread count via `GetChat`); anchor pinned once at `Open` so `anchor_id` never drifts. Message-id anchor not in chat → `not_found`; `unread` with nothing unread degrades to the live edge with no `anchor_id`. `MessageLister` widened (+`ListMessagesAround`/`ListMessagesAroundUnread`/`GetChat`). Store + raw-socket tests; hand-verified over socket (anchor_id meta, balanced window, ascending render keys, bidirectional extend, not_found). Anchored `extend` is **symmetric** here; **B3c** makes it directional (Decision log 2026-07-07). |
| B3c | `messages` directional `extend`: a **required** `direction` (`older`\|`newer`) on `extend`; anchored windows grow one frontier at a time (supersedes B3b's symmetric growth); `direction:"newer"` on a `latest`-anchored window errors | done | `extend` now requires `direction` (`older`\|`newer`) — missing/other → `invalid_params`. New engine capability `DirectionalSession` (`view.go`): when Open returns one, the engine hands it window ownership — `extend` routes `(direction,count)` to `ExtendWindow`, `Items(0)` is the whole current window (no prefix trim), `Exhausted()` (per the frontier last extended) drives `ready`. `messagesSession` split into `latestMessagesSession` (unchanged prefix window) and `anchoredMessagesSession` (DirectionalSession: independent `olderReach`/`newerReach`; older frontier via `ListMessages` before the anchor, newer via new store `ListMessagesAfter`, anchor via `GetMessage`). Prefix/live-edge windows (latest messages, chats, object views) reject `newer` in `subscription.validateExtend` — **resolves the open sub-question**: there is no per-view special-casing, a prefix window's newer edge is simply the live edge. Store `ListMessagesAfter` + `MessageLister` (+`ListMessagesAfter`/`GetMessage`); store + protocol + conformance tests; hand-verified over socket (one-frontier growth, per-direction exhaustion, newer-on-latest + missing-direction errors). PROTOCOL.md amended to match (2026-07-07): `extend` verb row → `sub, count, direction`, and the Windows section rewritten for directional extend + Model A. | Planned in the B3b discussion (Harsh); required direction, newer-on-`latest` is an error. Engine-adjacent: the session must track older-reach + newer-reach independently — today `Items(max)` receives a single count, so `handleExtend`/the A2 engine must route direction (or hand window ownership to the session). `direction:"newer"` on `latest` → `invalid_params` (the newer edge *is* the live edge; new messages arrive unsolicited). Amends PROTOCOL.md `extend` verb row (`sub, count` → `sub, count, direction`; direction required) — sign-off at implementation. **Open sub-question to settle here:** what a required `direction` means for prefix/collection views (`chats`, `starred`, blocklist…) that have no older/newer axis. Independent of B4–B7; sequence anytime. |
| B4 | `typing`, `presence` (subscription-driven upstream WA presence subscribe), `receipts` | todo | |
| B5 | `self`, `contact`, `group`, `group_members` — two-phase local→network upserts | todo | |
| B6 | `privacy`, `preferences`, `blocklist`, `starred`, `pinned` | todo | |
| B7 | `stickers`, `sticker_packs`, `sticker_pack`, `transfers` | todo | |

### Phase C — commands & queries

| id | step | status | notes |
| --- | --- | --- | --- |
| C1 | Session/chat commands: `session.update`, `daemon.reconnect`, `account.logout`, `chat.*` (mark_read, pin, archive, mute, typing, request_older, ensure_direct) | todo | `chat.mark_read` carries an `up_to_message_id` watermark (Decision log 2026-07-07); dumb frontends pass the newest held message id. Applying it amends PROTOCOL.md's `chat.mark_read` row (currently `chat_id` → `{}`) at C1. |
| C2 | `send.*`, `message.*` (react, edit, revoke, delete, star, pin, forward), `media.download` (+`transfers` wiring), `media.fetch_profile_picture` | todo | |
| C3 | `privacy.set`, `preferences.set`, `self.set_about`, `contact.block`, sticker commands, queries (`search.chats`, `search.messages`, `contacts.check_phone`), `open_chat` connection-directed routing | todo | |
| C4 | **Daemon audit milestone:** finish `examples/` shell frontend as a real usable client; run full conformance; line-by-line diff of PROTOCOL.md vs daemon; fix drift or log `needs-decision` items | todo | Model A Windows-section + directional-`extend` PROTOCOL.md amendments already landed with B3c (2026-07-07); the audit re-checks PROTOCOL.md vs daemon end to end. |

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
- 2026-07-06 — A3 conformance uses an internal fixture server until real
  protocol views exist; no conformance-only view is registered in production.
- 2026-07-06 — B2 `chats` sort direction: PROTOCOL.md's *prose* ("ascending
  bytewise", "pinned section then recency") is authoritative and was
  implemented as most-recent-first via timestamp inversion. The example
  session's sort digits read as plain non-inverted timestamps (Aditi's key
  *grows* after a send), which is illustrative only. PROTOCOL.md left
  unchanged; flag if the example digits should be corrected to match a real
  most-recent-first key. No decision blocking B2.
- 2026-07-06 — B3 split into B3a (`latest` anchor, done) and B3b (`unread` +
  message-id anchors). Reason: `unread`/message-id anchors need a window
  positioned *around* an anchor (mid-sequence, growing both directions),
  which the A2 engine's single-integer **prefix** window cannot express;
  `latest` is a pure live-edge prefix window and fits as-is. B3b will extend
  the engine (or add view-specific anchored windowing) + return `anchor_id`
  subscribe meta. PROTOCOL.md unchanged; the temporary `invalid_params` on
  non-`latest` anchors is a migration state, not a spec change. Flag if B3b
  reveals the engine needs an anchored-window primitive worth its own step.
- 2026-07-06 — B3b resolved the above flag: **no new engine primitive was
  needed.** The B3a slice-order/sort-key split generalizes — ordering the
  session's slice by *proximity to the anchor* (instead of recency) makes the
  A2 single-integer prefix window express an around-anchor window: the prefix
  is the closest-`window` neighborhood the anchor sits inside, and `extend`
  widens it both directions. The prefix trim only ever drops the farthest
  end(s), so the window stays a contiguous run (no render gaps). The single
  `exhausted` boolean reads as "the whole local chat is in the window," which
  is the meaningful signal. A2 engine untouched; B3b landed in one view file.
- 2026-07-06 — B3b anchored-window semantics (implementation reading, spec
  latitude, PROTOCOL.md unchanged — flag if you disagree): (1) A brand-new
  live message far past a *mid-history* anchor does **not** auto-arrive —
  delivering it would leave a render gap between the loaded contiguous range
  and a lone new row. PROTOCOL.md's "new messages always arrive regardless of
  window size" is read as describing the live-edge (`latest`) window; for the
  `unread` anchor the anchor sits near the edge, so new messages do arrive
  within a normal window. (2) `extend` widens the anchored window
  symmetrically around the anchor (the store's balanced split pours growth
  into whichever side still has local history); the `extend` verb carries no
  direction. (3) `unread` with a zero/unresolvable unread count degrades to
  the live-edge window with no `anchor_id`, indistinguishable from `latest`.
- 2026-07-07 — `chat.mark_read` gains an `up_to_message_id` watermark
  (C1 concern, decided by Harsh). "Read" is a function of scroll position,
  which rule 1 keeps frontend-side, so the frontend must emit the one fact it
  alone holds — how far the user has actually seen — and the daemon owns
  everything downstream (recompute `unread`, send per-message read receipts
  upstream, cross-device read sync, notification clearing). One command
  serves both smart and dumb frontends: a smart frontend passes a true
  watermark from its scroll position; a dumb frontend passes the newest
  message id it holds and gets whole-chat "caught up" behavior with no
  separate code path. Not changing PROTOCOL.md now; the `chat.mark_read` row
  is amended when C1 lands (see C1 note).
- 2026-07-07 — **Problem 1 (mid-history anchor vs. new messages) resolved as
  Model A, "transient peek"** (decided by Harsh; ratifies the tentative item
  (1) in the 2026-07-06 B3b entry). An `unread`/`{message_id}` anchored
  `messages` window is bounded on **both** sides; a message outside it is
  never pushed in (that would render as a gap between the loaded block and a
  lone row). Awareness of out-of-window activity comes from **other views** —
  the `chats` row (preview/unread/recency) and frontend-computed jump-to-bottom
  pill counts — never the messages view. To follow the live edge a frontend
  subscribes `latest` ("swap to latest on reaching bottom"). Jump-to-bottom is
  a re-anchor in *either* model and costs one indexed tail fetch
  (`idx_messages_chat_timestamp`, ≈ single-digit ms over the local socket), so
  no tail preloading is required for a seamless UX. Reference frontend flow:
  the page is in *live* mode (sub = `latest`) at/near the bottom and *history*
  mode (anchored) when navigated away; it swaps the subscription only when the
  user crosses that boundary — a small scroll-up never leaves live mode, so
  new messages keep streaming into the local item map and jump-to-bottom is a
  pure scroll. PROTOCOL.md Windows section **amended 2026-07-07** (with B3c,
  signed off by Harsh): the unconditional "new messages always arrive
  regardless of window size" is now scoped to live-edge windows, and anchored
  windows are documented as bounded both sides with out-of-window awareness
  coming from `chats` ("to follow the live edge, subscribe `latest`").
- 2026-07-07 — **Problem 2 (`extend` direction) resolved** (decided by Harsh);
  **supersedes** the symmetric-growth reading in the 2026-07-06 B3b entry
  (item 2). `extend` gains a **required** `direction` field (`older`|`newer`).
  `latest`-mode extend is always `older` — the newer edge is the live edge and
  new messages arrive unsolicited — so `direction:"newer"` on a
  `latest`-anchored window is an **error** (`invalid_params`). Anchored windows
  grow only the chosen frontier, removing the ~2× over-fetch and the muddy
  count semantics of symmetric growth. Implemented as new step **B3c**
  (engine-adjacent: the session tracks two frontiers). Open sub-question
  deferred to B3c: what a required `direction` means for prefix/collection
  views with no older/newer axis — **resolved in B3c**: prefix/collection/
  object views are all live-edge windows, so they take `older` (grow away from
  the edge) and reject `newer`; no per-view special-casing. PROTOCOL.md's
  `extend` verb row **amended 2026-07-07** (with B3c, signed off by Harsh).
