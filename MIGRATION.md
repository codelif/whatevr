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
page by page; **Phase DeezNuts** then shakes the ported client out in real
daily use; teardown (Phase E) deletes the old stack. At no point is the app
broken for daily use.

**Phase DeezNuts gates the teardown.** Phase D is done, so the frontend now
runs purely on the protocol — but nothing about it has been exercised against
a live WhatsApp account (see the D-phase notes: no session was available in
any D session). Deleting the gRPC stack is the one irreversible move in this
plan, and doing it while the replacement is unproven would leave no fallback.
So Phase E does not start until Harsh closes Phase DeezNuts.

This file is the **only** progress tracker. Every work session updates it in
the same commit as the code it describes. If this file and reality disagree,
fixing that is the first order of business.

## Session protocol

One session = one step. Each session (see `.claude/commands/migrate.md`):

1. Read `PROTOCOL.md` and this file. Report to the user: current step,
   previous session's notes, open blockers/decisions.
2. If the next step is `blocked` or `needs-decision`, resolve that first
   (ask the user); otherwise take the **first step not marked `done`**.
   **While Phase DeezNuts is open, "first step not marked `done`" means the
   first open row in *its* table** — Phase E steps are not eligible, however
   inviting they look. If that table is empty, there is no step to take: say
   so and stop.
3. If the step is too big for one session (and really make sure it needs 
   another session, don't split willy nilly), **split it in the table** 
   (e.g. B3 → B3a/B3b) instead of pushing through — then do the first half.
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
| B4a | `typing` view: global unwindowed collection, one item per composing chat, live upsert/remove | done | `typing_view.go` (typingView/typingSession over `DaemonEventChatPresence`; item id = chat_id, `senders` [jid+name]). Daemon gained `ComposingChats()` snapshot for the initial fill (TTL-filtered). Discriminator: composing events carry a `SenderID`, availability events never do — a missing SenderID is how the view ignores availability churn (the same overloaded event feeds B4b's `presence`). Mirrors the daemon's single-slot presence model (≤1 sender per chat today; `senders` is a list for forward-compat). Sender names via new `SenderDisplayer` (added to `DaemonStore`; *store.DB.SenderDisplay). Store + raw-socket tests; hand-verified over socat (initial fill+ready, live upsert, availability event ignored, stop→remove). |
| B4b | `presence` view: per-chat, one item per participant, subscription-driven upstream WA presence subscribe, availability/last_seen | done | `presence_view.go` (presenceView/presenceSession over the SenderID-empty half of `DaemonEventChatPresence` — the counterpart to B4a's discriminator; item id = participant jid, == chat_id for a direct chat, `availability`/`last_seen_unix`). New `PresenceActions` interface (first view that *drives* upstream, not just reads store/events): subscribing calls `Client.SubscribeChatPresence` — `RegisterDaemonViews` gained an `actions` param (main passes `waClient`, fixture/tests pass nil). Initial fill from a new **synchronous** daemon snapshot `ChatAvailability` (mirrors B4a's `ComposingChats`) instead of the async `PublishCachedChatPresence` replay the plan named — cleaner, ready reflects cached state, no re-broadcast to unrelated subs (Decision log 2026-07-07). last_seen carried only while offline. Store-free raw-socket tests; hand-verified over socat (empty→ready + upstream subscribe, live online/offline upserts, cached initial fill before ready, composing-event ignored, other-chat filtered, missing chat_id → invalid_params). |
| B4c | `receipts` view: per-message, one item per participant, live re-derive on status updates | done | `receipts_view.go` (receiptsView/receiptsSession; unwindowed, re-derives `GetMessageInfo` on every relevant event, no cached state — store is authoritative). Group → one item per member (incl. not-yet-delivered) keyed by member jid with name/avatar; direct → single aggregate item under sentinel id `"peer"` (GetMessageInfo carries no jid for the 1:1 recipient), shown once delivery begins. New `MessageInfoActions`; B4b's actions param widened to a combined `DaemonActions` (`PresenceActions`+`MessageInfoActions`), main passes `waClient`, fixture/tests nil. **Required a new daemon event** `DaemonEventMessageReceipt` (`daemon.go` + `PublishMessageReceipt`), fired per recorded receipt in `applyParticipantReceipt` (`send.go`, gated `!offlineSync`): a group member's receipt usually does *not* advance the message's aggregate status, so the plan's `DaemonEventMessageUpdated`-only trigger would miss it (Decision log 2026-07-07). View triggers on receipt+updated+deleted filtered by message id; delete/not-found → empty. Not a wire-grammar or PROTOCOL.md change; frozen gRPC ignores the new kind. Store-free raw-socket tests; hand-verified over socat (group fill+ready, non-aggregate per-member read re-derives live, direct delivered→read, delete→remove, scoped, not_found, missing message_id). |
| B5a | `self`, `contact` — two-phase local→network upserts (ContactInfo object views) | done | `contact_view.go` (selfView/contactView + sessions over new `ContactActions` seam = `GetContactInfo`/`SelfProfile`; `DaemonActions` widened to embed it, main passes `waClient`, fixture/tests nil). Both are object views (id `"self"` / the normalized jid) filled from local data at subscribe; the network "about"/status text arrives as `DaemonEventContactInfoUpdated` (jid+status only) and overlays onto the held `app.ContactInfo` via a shared `overlayContactStatus`; the avatar refresh overlays via `overlayContactAvatar` (matches `Kind==Sender && ID==jid`). `self` also re-fetches `SelfProfile` on `DaemonEventSelfProfileChanged` and, while still unloaded (logged out), on `DaemonEventConnectionChanged` so it fills after login. Store-free raw-socket tests; hand-verified over a raw socket (initial local fill+ready, live about overlay for self+contact preserving phase-one fields, avatar overlay, self refetch, jid scoping, missing/invalid jid → invalid_params, nil-actions → internal). |
| B5b | `group`, `group_members` — two-phase local→network upserts (GroupInfo-based) | done | `group_view.go` (groupView/groupMembersView + sessions over new `GroupActions` seam = `GetGroupInfo`; `DaemonActions` widened). Both call `wa.Client.GetGroupInfo` at subscribe (stored members now) and replace their held card wholesale on `DaemonEventGroupInfoUpdated` (the live fetch's full enriched card). `group` is an object view (id = chat_id) carrying subject/description/avatar/created/owner/`member_count`(=len members)/`my_role`/announce/locked, **no** member array; chat-kind avatar refresh overlays. `group_members` is an unwindowed collection, one item per member keyed by jid, sorted superadmin<admin<member then name then jid (`memberSortKey`, `\x1f`-delimited) — joins/leaves/promotions fall out of the engine's roster diff (upsert/remove); sender-kind avatar refresh overlays the matching member. **The spec-vs-data gap was not real:** `owner`/`my_role`/announce/locked are all in whatsmeow `types.GroupInfo`, so I plumbed them — `app.GroupInfo` gained `OwnerJID`/`MyRole`/`IsAnnounce`/`IsLocked`, populated in `wa.refreshGroupInfoLive` (my_role via `ownParticipantJIDs()` matched against the same canonical form the member list uses; new shared `app.GroupRoleString`). Two-phase by nature: roles/owner/flags are live-only, so phase one shows members as plain "member" with empty my_role/owner/flags. Frozen gRPC `GetGroupInfo` mapper ignores the new fields (stays compiling). Store-free raw-socket tests; hand-verified over a raw socket (group+members phase-one fill+ready, live enrichment with roles/owner/flags, role-rank reordering, join as upsert, leave as remove, chat scoping, missing/invalid chat_id → invalid_params, nil-actions → internal). |
| B6a | `privacy`, `preferences`, `blocklist` — settings views | done | `settings_view.go` (privacyView/preferencesView/blocklistView over new `SettingsActions` seam = `GetPrivacySettings`/`GetAppPreferences`/`GetBlocklist`; `DaemonActions` widened to embed it, main passes `waClient`, fixture/tests nil). `privacy`/`preferences` are object views (id `"self"`); `blocklist` is an unwindowed collection keyed by jid, sorted name-then-jid (`blocklistSortKey`, `\x1f`-delimited). `privacy` fills from the live connection (empty until login, fills on `DaemonEventConnectionChanged` while unloaded) and replaces wholesale on `DaemonEventPrivacySettingsChanged`'s carried snapshot. `blocklist` re-reads on `DaemonEventBlocklistChanged`, fills-after-login on `ConnectionChanged`, and overlays a held row's avatar on `DaemonEventAvatarUpdated` (Sender-kind, matching jid — same LID caveat as the contact card). **Required a new daemon event** `DaemonEventPreferencesChanged` (fired from `SetAppPreferences`): app preferences had no live-update path, and this makes the `preferences` view correct when they change via any caller (Decision log 2026-07-07). Daemon-event-surface addition, not a wire/PROTOCOL.md change; frozen gRPC ignores the kind. Store-free raw-socket tests; hand-verified over a raw socket (three initial fills+ready, live privacy snapshot, live prefs toggle, blocklist roster diff upsert+remove with name sort, avatar overlay, logged-out→login fill, nil-actions → internal). |
| B6b | `starred`, `pinned` — windowed message-row views | done | `starred_pinned_view.go` serves `starred` (live-edge prefix window over `store.ListStarredMessages`, newest-first sort, optional `chat_id`, `chat_name`, live star/unstar via `MessageUpdated`) and `pinned` (per-chat unwindowed `store.ListPinnedMessages`, oldest-pin-first sort, live pin/unpin plus expiry timer). Both reuse the B3 message item shape; `DaemonStore` widened with `StarredPinnedLister`; store-backed raw-socket tests cover fill, scoped/windowed extend, live add/remove, expiry, and missing `chat_id`. No PROTOCOL.md change. |
| B7a | `stickers`, `sticker_packs`, `sticker_pack` views | done | `sticker_view.go` serves source-filtered sticker library rows, pack rows, and async pack contents over the store/actions seam; registered in `RegisterDaemonViews`. Store-backed raw-socket tests cover source windows/extend, favorite live updates, pack rows, async contents, download-path upserts, and param errors. No PROTOCOL.md change. |
| B7b | `transfers` view | done | `transfers_view.go` serves active media transfers from daemon download events (initial replay + live progress, remove on terminal success/failure); message media rows now carry durable `media.download_error` via store `media_download_error` and `wa.DownloadMessageMedia` clears/sets it around retries/failures. PROTOCOL.md amended: `transfers` is active-only; terminal failure is rendered through message upserts. Store + raw-socket tests; hand-verified over a raw socket (subscribe, ready, progress upsert, remove). |

### Phase C — commands & queries

| id | step | status | notes |
| --- | --- | --- | --- |
| C1 | Session/chat commands: `session.update`, `daemon.reconnect`, `account.logout`, `chat.*` (mark_read, pin, archive, mute, typing, request_older, ensure_direct) | done | `commands.go` registers C1 acks/results over the protocol; `session.update` maps each socket to a frontend session, chat commands call the WA action seam, and `chat.mark_read` now requires/applies `up_to_message_id` via `wa.MarkChatReadUpTo`. PROTOCOL.md amended accordingly; fixture + raw-socket tests cover the surface. |
| C2 | `send.*`, `message.*` (react, edit, revoke, delete, star, pin, forward), `media.download` (+`transfers` wiring), `media.fetch_profile_picture` | done | `commands.go` now registers the C2 command surface with ids/acks only; `wa.SendMediaWithMentions` plumbs protocol media mentions while legacy gRPC keeps its old signature; fixture + raw-socket command tests cover send/message/media commands. |
| C3 | `privacy.set`, `preferences.set`, `self.set_about`, `contact.block`, sticker commands, queries (`search.chats`, `search.messages`, `contacts.check_phone`), `open_chat` connection-directed routing | done | `settings_commands.go`, `sticker_commands.go`, and `query_commands.go` register the new commands/queries (acks only; rows reuse daemon shapes); `open_chat.go` routes connection-directed events to protocol sessions and `main.go` fans notification clicks to both gRPC + protocol during migration. |
| C4 | **Daemon audit milestone:** finish `examples/` shell frontend as a real usable client; run full conformance; line-by-line diff of PROTOCOL.md vs daemon; fix drift or log `needs-decision` items | done | Delivered as the whole-migration audit remediation (2026-07-07): a single branch off `main` folding ~30 findings across phases A/B/C into focused commits (startup race, daemon-event resync, command correctness, error segregation + `commands.go` split, view-invalidation completeness, wire/connection hardening, message-anchor & view polish). `examples/shell-frontend.sh` is now a real send + read + chat-list client; `scripts/conformance` passes; PROTOCOL.md reconciled with the daemon (WS8 amendments below); regression tests added (empty-`sort` fallback, conn-frame cap, async `media.download`, all-or-nothing `forward`, sticker favorite/contents invalidation, presence renewal-on-reconnect, backgrounded privacy/blocklist load). `internal/protocol` no longer imports `google.golang.org/grpc`. |

### Phase D — whatkevr port (C++/QML, page by page; gRPC stays alive until D7)

| id | step | status | notes |
| --- | --- | --- | --- |
| D1 | Qt client core: socket transport + dispatcher, generic keyed/sorted `QAbstractListModel` over a collection view, object-view wrapper; no UI changes yet | done | New `whatkevr/src/protocol/`: `ProtocolClient` (QLocalSocket + NDJSON framing, `hello` handshake, id-correlated request/response, `sub`-routed view events, `open_chat`, auto-reconnect + resubscribe), `Subscription` (owns the daemon `sub`, `extend(count,direction)`, exposes subscribe meta e.g. `anchor_id`), `CollectionViewModel` (generic keyed list; orders **only** by bytewise `sort`, id tiebreak; ItemRole=whole QVariantMap), `ObjectViewModel` (single-item view). No UI wiring, no QML registration, gRPC untouched — pure scaffolding for D2+. Built into the main target (`just build` green); `WHATEVR_BUILD_TESTS=ON` adds `whatkevr/tests/tst_protocolcore` (13 QtTest cases driving the real client over a real socket via an in-process fake daemon — fill/sort/replace/move/remove/reset, ready-exhausted, directional extend, subscribe meta, object view, open_chat, pre-hello request queue). |
| D2a | Port connection/status/login pages onto the protocol (`connection`+`login` views) | done | New `whatkevr/src/app/protocolcontroller.{h,cpp}`: a QML singleton owning the D1 `ProtocolClient` + two `ObjectViewModel`s (`connection`, `login`), deriving every string the status/login/splash pages bind to (phase, `statusTitle/Text`, `detailText`, QR + countdown, `primaryAction*`) plus `startDaemon`/`triggerPrimaryAction`(→`daemon.reconnect`)/`copyToClipboard`. Runs **alongside** the gRPC `AppController` (strangler): `Main.qml` `appMode()` + the rebuild/logout gate now key off `ProtocolController`; `StatusPage`/`LoginPage` bindings repointed; `AppController` keeps serving the chat shell + deep-link signals until later D-steps. Transport phase = client-ready + cold-start grace + socket-exists (the client's own auto-reconnect subsumes AppController's channel/probe/retry). Socket-path seam added for tests; `daemonSocketExists()` now checks the path actually used (was a latent bug). New QtTest `tst_protocolcontroller` (fake daemon over a real socket: not-running-after-grace, online→shell, need_login→QR+countdown, live state flip, reconnect command). `just build` green; whatkevr tests + conformance pass; hand-verified `connection`/`login` over socat and a headless app launch against the live online daemon (no QML errors, reaches chat mode). No PROTOCOL.md change. |
| D2b1 | Port chat list core: `chats` view active list, filters (all/direct/groups) as subscribe params, row/field mapping, list commands (pin/archive/mute), selection routing to gRPC conversation, loading/empty | done | `ProtocolController` grew a generic `chatsModel` (`CollectionViewModel`) subscribed to `chats {filter, archived:false}`, a `chatFilter` int that **re-subscribes** (no frontend filtering — the D1 `ChatListFilterModel` proxy is gone from the pane), derived `chatsLoading`/`chatsEmpty`, and `setChatPinned/Archived/Muted` mapping to `chat.*` acks. `ChatListPane.qml` binds the delegate to `model.item.<daemon field>` with pure-presentation string→int status/direction + initials helpers; context-menu/loading bindings repointed to `ProtocolController`. Selection still routes to the gRPC `AppController.selectChat` (conversation is D3); drafts read from gRPC `AppController.chatDraft` (frontend state, allowed; composer is D4). Archived-section QML left inert (D2b2). `tst_protocolcontroller` +3 cases (fill/order, filter re-subscribe, command mapping) via a collection-serving fake daemon; `just build` + whatkevr tests + conformance pass; hand-verified over socat (fill+ready, daemon-side groups/direct filters, `chat.pin` ack) and a headless app launch (no QML errors, reaches chat shell). No PROTOCOL.md change. |
| D2b2 | Port archived chats section (second `chats` subscription) + `typing` overlay + `sync` history strip | done | `ProtocolController` grew `archivedChatsModel` (a second `chats` sub, `archived:true`, same filter, re-subscribed with the active list) + `archivedCount`; a `typing` collection with `chatTyping(chatId)` + a `typingRevision` tick; and a `sync` object view feeding derived `historySyncVisible/Percent/Title/Detail` (names mirror `AppController`). `ChatListPane.qml`: the inline row became a shared `chatRowDelegate` `Component` used by both the active `ListView` and a collapsible **`footer`** (header + `Repeater` over `archivedChatsModel`) — the old `section.property="chatSection"` machinery is gone; `ChatListDelegate` width gained a `parent.width` fallback for the footer. `isTyping` binds `chatTyping(chatId)` keyed on `typingRevision`; `HistorySyncStrip` + placeholder repointed to `ProtocolController.historySync*`. Sync display policy is simplified vs the gRPC cursor (renders the single current `sync` item; drops cross-event type-dedup) — see Decision log. `tst_protocolcontroller` +3 (archived separate model, typing live start/stop, sync derivation incl. complete/on-demand hiding); `just build` + all whatkevr tests (13) + conformance pass; hand-verified over socat (archived count, `typing` ready, `sync` item) and a headless app launch (no QML errors). No PROTOCOL.md change. **D2b complete.** |
| D3a | Protocol message presentation model over the generic daemon-sorted collection; expose every `ready` completion for directional pagination; no UI wiring | done | New `ProtocolMessageModel` mirrors `CollectionViewModel` rows unchanged (ascending daemon `sort`) and maps whole message items into the timeline's presentation roles/helpers, including unknown-kind `fallback`, nested sender/media/reply/reactions, ascending day/sender grouping, markup/layout memoization, snapshots/copy/selection helpers. `CollectionViewModel::readyReceived` exposes every `ready`; its move mutation now obeys Qt's begin/end contract. New model/core tests include `QAbstractItemModelTester`; no UI wiring or PROTOCOL.md change. |
| D3b | Port conversation selection + timeline: `messages` latest/unread/message-id anchors, directional extend, jump navigation, read watermark, session/open-chat routing | done | `ProtocolController` now owns selected-chat `messages` subscriptions (latest/unread/message-id), independent directional pagination/exhaustion, jump/re-anchor, exact read watermarks, visible-session updates, and `open_chat`; `MessageView.qml`/`RowScrollBar.qml` render the ascending daemon-sorted model and preserve prepend viewport anchors. Protocol core hardens delayed subscribe/extend replies and exposes extend failures. Controller/core/model tests cover anchors, resets, jumps, pagination, phone-history, session/read routing; `just build`, all Qt tests, conformance, live raw-socket exercise, and offscreen launch pass. No PROTOCOL.md change. |
| D3c | Port conversation chrome: selected-chat `presence` header + dialog-scoped live `receipts` view | done | `ProtocolController` grew a `presence` subscription scoped to the *displayed* conversation (subscribed/dropped alongside the messages window, so the upstream WA presence demand tracks what is on screen) and `selectedChatPresenceText`, which composes the global `typing` view over the per-chat availability/last-seen (typing wins, then `online`, then a mirrored `formatLastSeen`); `ConversationPane`'s header subtext repointed to it. `MessageInfoDialog` now lives entirely on the `receipts` view: `openMessageReceipts`/`closeMessageReceipts` make the dialog's lifetime the subscription's lifetime, rows come from a `CollectionViewModel` read through `messageReceipts()` + `messageReceiptsRevision`, direct chats read the daemon's `"peer"` aggregate via `directMessageReceipt()`, `is_group` comes from the `chats` row and the Sent time from the `messages` row (no dialog-side copies) — the gRPC `requestMessageInfo` round-trip, its manual avatar patching, and its `info` snapshot are gone. `tst_protocolcontroller` +3 (presence fill/live flip/typing precedence/visibility scoping, group receipt roster live-update + dialog-scoped unsubscribe, direct aggregate + `not_found` error); the fake daemon gained per-view subscribe params/counts and unsubscribe tracking. `just build`, all 26+16+7 Qt tests, `go test -tags sqlite_fts5 ./...`, `scripts/conformance` and `qmllint` (no new warnings) pass; `presence`/`receipts` hand-exercised over a raw socket. **Live-account check not performed** — see the D-phase note. No PROTOCOL.md change. |
| D4a | Port the composer's own send paths: `send.text`, `send.media` (image attach + clipboard/drag-drop paste), `chat.typing` composing indicator | done | `ProtocolController` gained `composerEnabled`/`sendInFlight`/`composerErrorText` plus `sendText`/`sendMedia`/`sendClipboardImage`/`setSelectedChatComposing` (protocolcontroller.{h,cpp}); `ConversationPane.qml`'s composer signal handlers and `MessageComposer.qml`'s clipboard-paste call repointed from `AppController` to `ProtocolController`. A send never applies the command result locally — the daemon delivers the sent message through the ordinary `messages` view upsert (rule 2), so there is no `applyMessageEvent`-equivalent; a send while an unread anchor is showing dismisses that divider (the user has now seen past it), mirroring `AppController::dismissUnreadAnchor`. `setSelectedChatComposing` keeps the gRPC version's per-chat dedupe (a "stop" only sends for the chat a "start" actually went to). Mentions/emoji picker/group-info-driven `@`-autocomplete and drafts stay on `AppController` (unchanged — mentions need `group_members`, not yet subscribed by `ProtocolController`; drafts are frontend-only state rule 1 already allows reading cross-stack, same as D2b1). `just build`, all three Qt suites (`tst_protocolcontroller` 29, `tst_protocolcore` 16, `tst_protocolmessagemodel` 7), `go test -tags sqlite_fts5 ./...`, and `scripts/conformance` pass; `send.text`/`send.media`/`chat.typing` hand-exercised over a raw socket against a throwaway daemon (params accepted, reached `not_logged_in` rather than `invalid_params` — confirms the wire shape matches the daemon's C2 handlers) plus a headless app launch (no QML errors). No PROTOCOL.md change. **Live-account send/receive not verified** — same environment gap as D3c (no logged-in WhatsApp session available here); see D-phase notes. |
| D4b | Port message actions: react/edit/revoke/delete/star/pin/forward (context menu, `ReactionDetailsDialog`, `PinnedMessagesBanner`) | done | `ProtocolController` gained the seven `message.*` commands (`sendReaction`/`editMessage`/`revokeMessage`/`deleteMessageForMe`/`setMessageStarred`/`pinMessage`/`unpinMessage`/`forwardMessage`) plus `canEditAt`, all **ack-only**: the gRPC path's optimistic apply-and-rollback (`applyOptimisticEdit`/`Reaction`/`Star`, the pin's cached-message round-trip) is **deleted**, since the reaction pill, star, pin, edited body and revoke tombstone all arrive back as ordinary `messages`/`pinned` upserts (rule 2) — the same simplification D4a got for sends. Failures surface through ported `messageActionFailed`/`messageForwarded` signals (`MessageView`'s `Connections` repointed); a forward batch still reports once for the whole multi-message selection. `PinnedMessagesBanner` now reads the per-chat **`pinned` view** (subscribed/dropped with the displayed conversation, like D3c's `presence`) through `pinnedMessagesCount`/`pinnedMessageAt(i)`/`pinnedMessagesReady` — `PinnedMessagesModel`, its insertion-position ordering and `AppController::loadPinnedMessages` are off the render path. `ForwardChatPickerDialog` holds its **own dialog-scoped `chats` subscription** (`filter:"all"`, `archived:false`) read via `forwardChatTargets(query)` + `forwardTargetsRevision`; this **deleted the last `ChatListFilterModel` proxy from the UI** (the search box is now presentation-side filtering over rows it already has, per PROTOCOL's `group_members` precedent). Net frontend deletion of ordering/caching logic. `just build`, all three Qt suites (`tst_protocolcontroller` 32, `tst_protocolcore` 16, `tst_protocolmessagemodel` 7), `go test -tags sqlite_fts5 ./...`, `scripts/conformance` and `qmllint` (six warnings **removed**, none added) pass; the seven commands + the `pinned` subscribe hand-exercised over a raw socket against a throwaway daemon, plus an offscreen app launch. No PROTOCOL.md change. **Live-account check not performed** — same environment gap as D3c/D4a; **and** the picker now lists active chats only (see Decision log). |
| D4c | Port `media.download` + the `transfers` progress view + the message image viewer (`ChatBubble` download UI, `ProfilePictureViewer` reuse) | done | `ProtocolController` gained `downloadMessageMedia` (ack-only `media.download`) and a session-long **global `transfers`** subscription, handed to `ProtocolMessageModel::setTransfersSource`. The timeline's two download roles (`mediaDownloading`/`mediaDownloadProgress`, stubbed `false`/`-1` since D3a) now **read through** to that view by message id — a keyed lookup at render time, no copy, no cache, the same compose-two-views shape as D2b2's typing-in-chat-rows and D3c's typing-over-presence; `direction` discriminates so a future upload row cannot light a download spinner. `ChatBubble`'s three `AppController.downloadMessageMedia` call sites (auto-download-on-viewport + the two manual buttons) repointed; the gRPC `m_mediaDownloadingMessageIds`/`m_mediaDownloadReplies` optimistic set is off the render path. Durable failure was already right: `media.download_error` rides the message row (B7b) and the bubble renders it. **The image viewer needed no work** — `ConversationPane`'s `ProfilePictureViewer` lightbox has been fed by the protocol model's `media.path` since D3b. `just build`, all three Qt suites (`tst_protocolcontroller` 33, `tst_protocolcore` 16, `tst_protocolmessagemodel` 8), `go test -tags sqlite_fts5 ./...`, `scripts/conformance` and `qmllint` (clean) pass; `transfers` + `media.download` hand-exercised over a raw socket against a throwaway daemon. No PROTOCOL.md change. **Live-account check not performed** — same environment gap as D3c/D4a/D4b; **and** two reads stay on `AppController` by design (see Decision log). |
| D5 | Port info dialogs (contact/group/members), starred/pinned pages, unified + in-chat search | done | `ProtocolController` gained four dialog/page-scoped surfaces and their views. **Info card**: `openContactCard`/`openGroupCard`/`closeInfoCard` hold the `contact` **or** `group` object view (one `ObjectViewModel`, whichever the dialog asked for) plus `group_members` and — for a contact — `blocklist`; `ContactInfoDialog` renders the daemon item directly, so its snapshot/restore card cache, its member `ListModel` + `applyMemberFilter` rebuild, its hand-patched avatar merging and its blocklist snapshot are **deleted** (net −180 QML lines): two-phase enrichment is just a second upsert, back-navigation is a re-subscribe, blocked-ness is membership in `blocklist`, member search is presentation-side filtering (`groupMembers(query)` + revision tick). `contact.block`, `media.fetch_profile_picture` (→ `profilePictureReady`) and `chat.ensure_direct` (→ select + `openChatRequested`) ported with it. **Starred page**: `StarredMessagesPage` owns a windowed `starred` subscription for exactly as long as it is on screen (`openStarredMessages`/`closeStarredMessages`, `loadMoreStarredMessages` = `extend older` at the list end) and binds the generic collection, deriving row strings through one pure `messageRowDisplay(item)` helper (shared `util/messagerow` `messageRowPreview`, so `fallback` still covers unknown kinds). **Search**: unified chat-list search and in-chat search both run the daemon *queries* (`search.chats`/`search.messages`/`contacts.check_phone`) into a new `ProtocolSearchModel` (`models/protocolsearchmodel.*`, same roles as the frozen `SearchResultsModel` so `SearchResultDelegate` was a one-line repoint); a generation counter drops superseded replies, the in-chat match cursor stays frontend state and a chat switch ends the search. **Also ported the D4a leftover it unblocked**: the composer's `@`-mention roster is now a `group_members` subscription on the *displayed* conversation (`chatMembers(query)`), deleting MessageComposer's per-chat member cache and its `openGroupInfo` fetch path. **The row's "pinned page" was already done** — whatkevr has no separate pinned page; the `pinned` view landed with D4b's banner. `just build`, all three Qt suites (`tst_protocolcontroller` 40, `tst_protocolcore` 16, `tst_protocolmessagemodel` 8), `go test -tags sqlite_fts5 ./...`, `scripts/conformance` and a qmllint before/after diff (164 warnings before, 164 after — none added) pass; the three queries, the four views and the three commands hand-exercised over a raw socket against a throwaway daemon, plus an offscreen app launch. No PROTOCOL.md change. **Live-account check not performed** — same environment gap as D3c/D4a/D4b/D4c. |
| D6 | Port settings pages (privacy/prefs/blocklist/profile) + sticker/emoji pickers, incl. `send.sticker` (moved here from D4, see decision log) | done | `ProtocolController` now owns session-long `self`/`preferences`, page-scoped `privacy`, and shared contact-card/settings `blocklist` views plus ack-only settings/profile/logout commands; all active settings/sidebar/bubble bindings use daemon snake-case rows. Emoji remains frontend presentation state but moved off the gRPC controller. New `ProtocolStickerController` owns picker-scoped `stickers`/`sticker_packs`/`sticker_pack` views, transient daemon-ordered `search.stickers`, bounded download requests, favorite/install/refresh actions, and ack-only `send.sticker` (messages still arrive only through `messages`). PROTOCOL.md adds the approved search query + forced-refresh command; daemon fixes include complete sticker invalidation/unbounded omitted-limit semantics, deterministic search ties, truthful refresh errors, and backgrounded missing-file sticker sends. `just build`, all three Qt suites, full Go tests, conformance, qmllint (161 warnings, down from D5's 164), raw-socket exercise, and offscreen launch pass. No Spirit-checklist bend. |
| D7 | Remove all gRPC client code + qt6-grpc from whatkevr; whatkevr runs 100% on the new protocol | done | **whatkevr is now a pure protocol client.** Deleted `app/appcontroller.{h,cpp}` (5.9k lines), `app/stickercontroller.*` and the eight proto-typed models (`chatlist`, `chatlistfilter`, `messagelist`, `pinnedmessages`, `searchresults`, `starredmessages`, `sticker`, `stickerpack`) — ~10.4k lines net deletion; dropped `qt_add_protobuf`/`qt_add_grpc` and `Qt6::Grpc`/`Qt6::Protobuf` from `whatkevr/{,src/}CMakeLists.txt`, and `qt6-grpc` from the README build deps + all three AUR packages (the binary links neither library now: `ldd` is clean). `main.cpp` constructs only `ProtocolController`. The eighteen remaining `AppController.*` QML call sites fell into three buckets: **already ported** (`selectChat` mirroring in Main/ChatListPane/StarredMessagesPage, `bannerText`, `composerEnabled` — deleted as duplicates), **gRPC-only machinery with no protocol counterpart** (`populateSelectedChat`/`uiTransitionActive`, the deferred-load gate — the messages subscription already starts at `selectChat`; and `requestChatAvatar`, which PROTOCOL deliberately has no command for), and **frontend-only helpers with no daemon behind them**, which moved onto `ProtocolController` (drafts `chatDraft`/`setChatDraft`, `toCommonMark`, `previousGraphemeBoundary`, `copyImageToClipboard`, `saveMediaAs`, `perfLogging`) alongside `handleCommandLine` + a new `activateWindowRequested` signal for single-instance/deep-link routing. Drafts are a plain `QHash` over the same `settings/drafts` QSettings key (read directly, the way EmojiModel reads its own state) — the gRPC version's draft-floats-the-row-to-the-top ordering is **gone**, since the daemon owns `chats` sort. `tst_protocolcontroller` +4 (deep link held until `shellVisible` then applied once, plain/malformed command line raises only, drafts round-trip across a controller restart, grapheme-cluster Backspace); `just build`, all four Qt tests (46 controller cases), `go test -tags sqlite_fts5 ./...`, `scripts/conformance` and qmllint (161 warnings, same as D6) pass; the startup subscription set hand-exercised over a raw socket and an offscreen `whatkevr` verified to hold a live protocol session (daemon fd count) with no QML errors. No PROTOCOL.md change. **Phase D complete.** **Live-account check not performed** — same environment gap as D3c–D6; **and** two behaviour narrowings are logged below (dead-avatar re-fetch, transition gate). |

### Phase DeezNuts — field testing (status: **open**, indefinite)

Harsh uses whatkevr as his daily WhatsApp client and reports whatever is
broken, half-working, or worse than the gRPC build was. Each report becomes a
row below and is fixed one per session, exactly like a migration step. The
phase has no step count and no end date: it closes when Harsh says it closes,
and Phase E stays untouchable until then.

**Why a phase and not a checklist:** the whole D phase was verified against
fake daemons, raw sockets and offscreen launches, because no logged-in
WhatsApp session was ever available (D3c through D7 all say so). Everything
that only appears with a real account — history sync, media round trips,
group rosters, notifications, presence, receipts, reconnects — is still
unproven. That is not a small residue; it is most of the app.

**Reporting.** A report can be as loose as "the chat list flickers when I
archive something". The session that picks it up is responsible for turning
it into a specific defect first (reproduce, or say plainly that it could not
be reproduced) and only then fixing it.

**Where fixes go.** The daemon owns state, so most fixes belong on the daemon
side of the socket — resist fixing a frontend symptom with frontend state.
If a fix would need sorting, merging, dedup or caching in whatkevr, that is
the signal the daemon or PROTOCOL.md is wrong; raise it rather than write it.
A report that reveals PROTOCOL.md itself is wrong follows the usual rule:
`needs-decision`, Decision log, sign-off.

Status: `todo` | `doing` | `done` | `blocked` | `needs-decision` |
`wontfix` (agreed not a defect) | `deferred` (real, but post-migration)

| id | report | status | notes |
| --- | --- | --- | --- |
| DN1 | Launching whatkevr always shows the splash spinner forever, with or without `whatevrd` running | done | Not a protocol defect — a QML singleton one. Both `ProtocolController` and `Settings` declared `explicit T(QObject *parent = nullptr)`, and `QQmlPrivate::singletonConstructionMode()` (`qqmlprivate.h`) tests `std::is_default_constructible` *before* `HasSingletonFactory`, so the engine built its own second instance and never called `create()`. QML therefore bound to a controller `main()` never `start()`s: no subscriptions, no `stateChanged`, `starting` stuck true, splash forever. Fix: drop the defaulted `parent` on both constructors so the factory path is taken (`whatkevr/src/app/protocolcontroller.h`, `app/settings.h`, `main.cpp` passing `nullptr`). Second bug found on the way and fixed with it: `filterKirigamiNullPropertyWarnings()` chained to the handler `qInstallMessageHandler()` returns, which is `nullptr` when the *default* handler was in place — every warning, QML error and `console.log` was being dropped, which is why an app hung on its own splash produced not one line of output. Verified live (first run of this client against a real `whatevrd`): no daemon → status page; daemon up → login page with a live, counting-down QR; `ctest` 4/4. |
| DN2 | The composer stays disabled ("Select a chat to message") even with a chat open | done | Frontend-only, and a plain missing-notify bug. `composerEnabled` is `hasSelectedChat() && m_clientReady`, but its `NOTIFY` is `composerChanged`, which only the send paths ever emitted — neither `setSelectedChat` nor `onClientReady`/`onClientDisconnected` did. The C++ getter was always right (which is why `tst_protocolcontroller` never caught it: it *calls* the getter), but the QML binding in `ConversationPane` had nothing to re-evaluate on, so whatever the composer was at creation time it stayed. Fix: emit `composerChanged` alongside `selectionChanged` and on both connection transitions (`whatkevr/src/app/protocolcontroller.cpp`). New case `composerEnabledNotifiesOnSelectionAndConnection` watches the signal rather than the getter, and `FakeDaemon` gained `dropClients()` to exercise the disconnect half; confirmed it fails with the emit removed. `ctest` 4/4. |
| DN3 | An edited message shows neither the new text nor the edited pencil | done | Daemon-side, and not the mark: the edit never landed at all — the store had `is_edited = 0` on every one of 53k rows. `handleEditMessage` only recognised a bare `MESSAGE_EDIT` protocol message, but current WhatsApp clients seal an edit in a `SecretEncryptedMessage` (`SecretEncType = MESSAGE_EDIT`) keyed by the *target* message's secret, and whatsmeow deliberately hands that over still encrypted (`DecryptSecretEncryptedMessage` is the client's job). So the event fell through to `ingestMessage`, which found no text in it and dropped it silently — no update, no log, no trace. Fix: `editPayload` (`whatevrd/internal/wa/messages.go`) recognises both shapes, decrypts the sealed one, and takes the target id from the envelope's `TargetMessageKey` (a protocol key in the plaintext still wins); an edit that cannot be opened is swallowed rather than ingested. New case `TestSecretEncryptedEditIsRoutedAsAnEdit` covers the routing either side of the crypto (opening the envelope needs a real message secret, so that part is live-only). Go suite green. |
| DN4 | Jumping to a message (goto, or clicking a quoted/tagged reply) should land it **centered** in the viewport whenever the chat is long enough to allow it | done | Frontend-only. Two defects behind one symptom. (1) `jumpToLoadedMessage` short-circuited whenever the target was *already* on screen — it glowed the row where it stood, so clicking a reply whose target was two rows up never moved the viewport at all. (2) The centring itself was one-shot `positionViewAtIndex(…, ListView.Center)`, which for a row that is still unmaterialised centres against the view's *estimated* content height; once the real delegate exists with its real height the row has drifted off centre, and nothing corrected it. Fix: a single `centerOnIndex()` (`whatkevr/src/qml/components/MessageView.qml`) that positions, `forceLayout()`s, then writes `contentY` directly from the now-materialised item, clamped to the scroller's own bounds — which is also what makes "when possible" degrade cleanly at either end of a chat rather than fighting the edge. Both the initial jump and `settlePendingJump` (after the anchored window lands) go through it, so the row is re-centred once its height is no longer a guess. `itemIsVisible` had no other caller and went with the short-circuit. |
| DN5 | A chat sitting at the bottom does not auto-scroll to new messages; it should follow the live edge with a few px of slack | done | Frontend-only, three separate ways for follow-mode to die, all in `MessageView.qml`. (1) **No pixel tolerance.** `atNewest`/`followNewest` came from an `indexAt` probe at the bottom viewport pixel, falling back to bare `list.atYEnd`; `indexAt` returns -1 whenever that pixel lands in the inter-row `spacing` gap, which at the very bottom dropped follow-mode outright. Now geometry decides first: `distanceFromBottom() <= followPixelSlack` (one grid unit — the report's "few pixels buffer") counts as parked at the newest message, with the row-threshold test kept as an additional way to say yes. (2) **`openingChat` could latch on forever.** It gates *both* live-edge follow and older-history prefetch, and two paths in the `onUnreadAnchorChanged` handler returned without clearing it — the ones taken when the user scrolls, or starts a jump, before the unread anchor resolves. A chat opened with unread and scrolled early therefore never followed again, and never prefetched history again either. Every bail-out now ends the open. A `positionAtUnreadAnchor()` that fails (anchor row not in the model) also falls back to the newest message instead of latching. (3) `onRowsInserted` tested `last === list.count - 1` to spot an append; `list.count` is refreshed by QQuickListView's *own* handler for the same signal and the order of the two is an implementation detail, so it now asks the model via `modelRowCount()`. |
| DN6 | The client feels sluggish overall; opening a chat takes a while. Load only enough chats to fill the screen (~10-20) and lazy-load the rest | done | **Measured against Harsh's live daemon before touching anything** (raw socket, `subscribe`→`ready`): the unbounded `chats` subscribe returned **917 rows / 326 KB / ~30 ms**, and there were two of them (active + archived), re-issued on *every* filter switch — all to paint the dozen rows a sidebar shows. The same subscribe with `limit:24`: **24 rows / 13 KB / ~3 ms**. `limit` is already in PROTOCOL.md's `chats` row and the daemon already pushes it into SQL (`chats_view.go` `Items` → `filter.Limit`), so this needed **no protocol or daemon change**. Fix: `kChatPageSize = 24` on both subscriptions, `loadMoreChats()`/`loadMoreArchivedChats()` issuing `older` extends behind a one-in-flight guard cleared by the view's `ready`, and `ChatListPane.qml` calling them as the bottom comes into reach (a page landing re-runs the check, so a short window self-fills to a screenful and then stops). Second find, same file: the archived section's `Repeater` was bound unconditionally, so a full `ChatListDelegate` was built for **every** archived chat at startup and then collapsed to zero height behind a section nobody had opened; its model is now `null` until expanded. `archivedCount` is the loaded window rather than the total, so the header renders "N+" while more remain. **What this did not fix:** chat-*open* latency is not daemon-side — a `messages` subscribe at `limit:80` measured **4-6 ms** across five real chats. The cost is delegate construction (`ChatBubble.qml` is 2059 lines with 54 `Loader`s), which this pass did not restructure. See DN7 for the protocol gap windowing exposed. |
| DN7 | Windowing the chat list leaves no way to ask the daemon for **one** chat's row | done | PROTOCOL.md now specifies `chat`, a `chat_id`-scoped object view emitting the exact same whole row as `chats`, independent of list filters/windows. `store.GetChatForView` preserves list/search display-name normalization; `chatView` validates existence, re-reads on chat-affecting daemon events, and naturally removes on deletion. `ProtocolController` owns one selection-scoped `ObjectViewModel`, removes the interim two-list window chase, and waits for the row before choosing `unread` vs `latest` (transient `io` keeps waiting across auto-resubscribe; a definitive failure/empty fill clears the stale selection; an explicit jump wins over the pending default). Raw-socket tests cover row parity, archived lookup, live update/delete, and errors; controller tests cover an off-window unread group, live row replacement, no list extend, unsubscribe, empty-fill cleanup, and explicit-jump precedence. `just build`, full Go tests, all Qt suites, conformance, diff check, and an isolated raw-socket exercise pass. No Spirit-checklist bend. |
| DN8 | Every UI action feels sluggish; opening a chat/group takes seconds and blocks the UI, and the mobile column-slide animation drops frames | done | The follow-up DN6 deferred, done as one pass at Harsh's request rather than a row per session. `WHATKEVR_PERF=1` showed 120–255 ms frame gaps on a 1:1 open and three consecutive **3.5–4.1 s** gaps across one 12 s slide on a group. **Measured against the live daemon first: the messages path is innocent** — `messages` at `limit:80` serves in 8–10 ms, `chat`/`pinned`/`presence`/`typing` in 0–3 ms, the SQL in 2 ms on `idx_messages_chat_timestamp`, and 2.3 MB of `whatevrd.log` holds no `Slow db op` line. Four independent causes. **(1) The group roster blocked everything.** `View.Open` ran inline on the connection read loop, and `groupMembersView.Open` → `GetGroupInfo` → `resolveGroupMembers` → `participantDisplay` *per member* did cross-DB LID lookups, up to 4 `SenderDisplay` queries, a `stat()` per avatar and `EnsureAvatar` **writes on the `MaxOpenConns(1)` writer** — 505 ms warm for 1024 members, and whatkevr issued `group_members` *before* the `messages` subscribe, so the conversation could not start loading until it returned. Fixed three ways: `handleSubscribe` backgrounds `Open` with the response (`view.go`; a frontend cannot reference a `sub` it has not been given, so nothing can overtake it, and `registerSub` now reports a closed connection so a late session is released); `resolveGroupMembers` collects candidate ids, answers them with one batched `SenderDisplays` (`store/messages.go`) and hands avatar refresh to a goroutine *after* the roster is built; and the roster is no longer subscribed on open at all — `ensureChatMembers()` turns it on when the composer's `@` token opens, which is its only consumer (the info dialog has its own subscription). **(2) Chat open was two serialized round trips**, purely to read the unread count for the anchor — a row the chat list already holds. `setSelectedChat` now takes it from `knownChatRow()` and fires both subscriptions together, keeping the `chat`-view wait only for opens from outside a loaded window (notification, `whatevr://`); `selectedChatItem()` falls back to the list row until the object view answers. One of the two per-open model resets went with it. **(3) A fill was applied as one model transaction per item.** `ViewSink` gained an `onBatchBegin`/`onBatchEnd` bracket that `ProtocolClient` drives around each socket drain; inside it `CollectionViewModel` buffers and applies the run as one contiguous `beginInsertRows` per destination run — one for a fill, one for a history page — re-keying the id index once instead of per insert. Outside the bracket it applies eagerly exactly as before, so direct callers are unaffected. `conn.writeLoop` now writes through a `bufio.Writer` flushed when the queue drains, so the burst arrives in one read rather than 80, which is what lets the bracket coalesce. **(4) Amplifiers.** `emitAllRolesChanged` fired an all-roles `dataChanged` (≈45 delegate bindings × every row in a 9-viewport cache band) for the *neighbours* of every insert — narrowed to the six grouping/date roles a neighbour can actually change; `invalidateTransferRoles` swept the whole timeline on any transfer event — now per-row, diffed against the previous id set; `ProtocolMessageModel::data` re-decoded the item map plus nested sender/media/reply on *every* role read (~180 decodes per delegate) — now a single-row cache with lazy nested maps; `MessageView` called `positionViewAtEnd()` synchronously per content-height revision and `updateScrollState()` (two `indexAt` + an `itemAtIndex`, each a forced layout) per `contentY` change — both coalesced through `Qt.callLater` and skipped while `openingChat`, `cacheBuffer` halved to 2 viewports. Daemon churn too: an avatar update for *any* subject re-read and re-marshalled every window, now filtered to subjects the window actually renders, and `recompute` skips the marshal for items whose source value is unchanged. Frontend micro: `isKnownIanaTld` was a linear `contains()` over a ~15 KB blob (hash set now), and `extractMessageLinks` probed every character with no cheap rejection. New instrumentation, since the protocol layer had none: `WHATEVRD_PROTOCOL_PERF=1` times `view.Open` and `recompute`, and a slow `Open` logs unconditionally. Tests: five new `tst_protocolcore` cases pin the batching contract (fill collapses to one insert, prepend page stays one insert and keeps the index, interleaved rows stay sorted, remove+re-upsert resolves by wire order, `ready` and `reset` settle the buffer); `mentionRosterFollowsTheDisplayedGroup` rewritten for the demand-driven roster. `just build`, Go suite and all four Qt suites green. **Re-measured live afterwards.** Daemon: no `group_members` open at all, `messages` opens in 239 µs–1.7 ms, slowest `view.Open` of any kind 3.6 ms, and the log ordering shows `messages` going out alongside `chat` rather than a round trip behind it. Frontend, 120 slides vs the 34-slide baseline: **max frame gap 4086 ms → 382 ms, mean 623 ms → 102 ms, gaps over 1 s 3 → 0, gaps over 300 ms 26% → 2.3%**. **What this did not fix:** gap *frequency* is unchanged (0.68 → 0.73 per slide) — the stalls are far shorter but just as common, so the slide still reads as stuttery. That residual is delegate construction, exactly the cost DN6 flagged and deferred; see DN9. Operational note: `WHATKEVR_PERF` output goes to **journald** (`journalctl --user`) when whatkevr is launched without a controlling terminal, not to a redirected stdout — Qt's default handler picks the journal in that case. |
| DN9 | The column-slide animation still drops frames on every other slide (~100 ms each), even with the daemon and model work of DN8 done | todo | Split out of DN8, which removed the multi-second stalls but left the per-slide hitch rate untouched (87 gaps / 120 slides, mean 102 ms, max 382 ms). Everything upstream of the delegate is now cheap — a `messages` window is served in under 2 ms, arrives in one socket read, and lands as a single `beginInsertRows` — so what is left is `ChatBubble.qml` itself: 2059 lines, 12+ `Loader`s, three `Kirigami.ShadowedRectangle` (a GPU shadow pass each), and several geometry bindings that read `Loader.item` and so force the loaded item to exist for layout. DN6 identified this and did not restructure it; DN8 halved `cacheBuffer` and cut the binding churn around it but did not touch the delegate. Likely direction: make the rarely-used Loaders genuinely lazy (reply preview, sticker, read-more, selection TextEdit), collapse the shadow rectangles, and break the `Loader.item` height dependencies so a row can lay out before its optional parts exist. Worth re-measuring against a **release** build first — the field-test build is `CMAKE_BUILD_TYPE=Debug` (`-O0`), which inflates exactly this kind of per-delegate C++/QML cost. |

### Phase E — teardown & flagship polish (blocked on Phase DeezNuts)

| id | step | status | notes |
| --- | --- | --- | --- |
| E1 | Delete daemon gRPC server + `proto/`; purge protobuf/grpc from go.mod, CMake, justfile, packaging, README dependency lists | blocked | Blocked on Phase DeezNuts closing. The frozen gRPC stack is the only fallback if field testing turns up something the protocol path cannot do yet; deleting it is irreversible, so it waits. |
| E2 | **Final audit + release polish:** full PROTOCOL.md vs implementation pass; promote PROTOCOL.md DRAFT → v1 stable; rewrite README around the daemon/protocol as the flagship feature (30-line-frontend pitch, `examples/` front and center) | todo | |

## Blockers

- **Phase E is blocked on Phase DeezNuts** (2026-08-10). Field testing of the
  fully-ported client is open-ended and Harsh closes it; until then the frozen
  gRPC stack stays in the tree as the fallback. Not a defect — a deliberate
  hold on the one irreversible step.

## D-phase notes

- 2026-08-10 — **D7 verification, and the one thing it cannot cover.** No
  logged-in WhatsApp session here (unchanged since D3c), so the ported client
  was never driven against a real account. What *was* verified: `just build`
  green with `Qt6::Grpc`/`Qt6::Protobuf` gone from the target, `ldd` on the
  built binary showing neither library, the four Qt suites (46 controller
  cases including the four new ones), the full Go suite, `scripts/conformance`,
  and qmllint unchanged at 161 warnings. Against a throwaway `whatevrd`
  (isolated `XDG_*`, short socket path — note the runtime dir must be `0700`
  or Qt's `QStandardPaths::RuntimeLocation` silently falls back elsewhere and
  the client connects to the *wrong* socket, which cost time here): the whole
  startup subscription set (`connection`, `login`, `chats`, `typing`, `sync`,
  `transfers`, `self`, `preferences`) hand-driven over a raw socket with
  response→upsert→`ready` ordering intact, then an offscreen `whatkevr`
  launched against it — no QML errors, and the daemon's open-fd count rose
  while it ran, which is the proof it held a live protocol session on the one
  socket. A second launch carrying `whatevr://chat/<id>` also came up clean
  (the link stays pending against a logged-out daemon, exactly as the unit
  test asserts). **What remains genuinely unverified is everything a session
  gates** — the whole app now runs on one code path, so whoever has an account
  next should simply *use it for a day*: send/receive, react/edit/pin, download
  media, open info dialogs, change a setting, click a notification. That is the
  real D7 acceptance test and it cannot be faked here.

- 2026-08-10 — **D6 live verification gap (environment, unchanged since D3c).**
  No logged-in WhatsApp session was available, so real privacy/blocklist/about
  mutations and pack download/install/favorite/send round trips were not watched.
  Verified instead against an isolated logged-out daemon over the raw socket:
  `preferences` filled before `ready`, `preferences.set` acked and re-upserted
  the changed whole object, empty logged-out `self`/`privacy`/`blocklist` and
  sticker views reached `ready`, `search.stickers` returned its named array,
  `sticker_packs.refresh` acked, and `send.sticker` reached its handler as
  `not_logged_in` (not `invalid_params`). The real-account follow-up is to
  change one privacy value/About, block/unblock a contact, open/search/install a
  pack, favorite a sticker, and send it while watching the ordinary view upserts.

- 2026-08-10 — **D5 live verification gap (environment, unchanged since D3c) —
  and what it leaves untested.** Still no logged-in WhatsApp session here.
  Verified instead against a throwaway `whatevrd` (isolated `XDG_*`, short
  socket path) over a raw socket, with the exact params `ProtocolController`
  sends: `search.chats` / `search.messages` (global **and** `chat_id`-scoped
  with `limit:100`) answered `{"chats":[]}` / `{"messages":[],"has_more":false}`;
  `contacts.check_phone` reached the network layer (`not_connected`, not
  `invalid_params` — the shape is right); `subscribe starred` global and
  chat-scoped both `ready`+`exhausted`, `extend older` accepted and
  `extend newer` correctly refused; `contact`, `group`, `group_members` and
  `blocklist` all subscribed, with the `contact` card arriving as a **phase-one**
  local fill (`jid`+`phone`+`push_name`, no `about`) — exactly the two-phase
  shape the dialog now renders without merging; `chat.ensure_direct` returned a
  real `chat_id`; `contact.block` and `media.fetch_profile_picture` reached
  their handlers (`not_connected`), while every malformed variant
  (`contact` with no jid, `contact.block` with no `blocked`,
  `media.fetch_profile_picture` / `chat.ensure_direct` with no jid) came back
  `invalid_params`. Plus the client-side half in `tst_protocolcontroller`
  (fake daemon: two-phase contact upsert, blocklist membership flip, roster
  join/leave, windowed starred extend, query sections, match-cursor wrap) and
  an offscreen `whatkevr` launch. **What that leaves genuinely unverified is
  the second phase actually arriving**: a real contact's network `about` and a
  real group's live subject/roles/owner only land once a session exists, so
  whoever has one next should open a group card and watch the roles fill in,
  and check the `@`-mention picker in a group with a cold roster (phase one can
  legitimately be empty — the picker then shows only "Everyone" until the live
  roster upserts).

- 2026-08-10 — **D4c live verification gap (environment, unchanged since D3c) —
  and the one thing it actually leaves untested.** Still no logged-in WhatsApp
  session here, so no real byte-streaming download was watched end to end.
  Verified instead: a throwaway `whatevrd` against isolated `XDG_*` dirs, hand
  -exercised over a raw socket — `subscribe {"view":"transfers"}` →
  `{"sub":1}` then `ready`/`exhausted` (empty, as it should be with nothing
  downloading), `media.download` with the exact params `ProtocolController`
  sends → `{}` (the ack-then-lifecycle contract: it acks even for an id absent
  from that empty store, because the transfer runs in the background), a
  deliberately malformed `media.download` → `invalid_params`, and
  `extend direction:"newer"` on the transfers window correctly refused. Plus the
  client-side half against a fake daemon in `tst_protocolcontroller`
  (subscribe → transfer upsert → 25% → remove) and `tst_protocolmessagemodel`,
  the Qt/Go/conformance suites, a clean `qmllint`, and an offscreen `whatkevr`
  launch. **What that leaves genuinely unverified is the progress ring's feel**
  — the daemon throttles progress to one event per 150 ms per transfer
  (`mediaProgressReportInterval`), and whether that reads as smooth in the
  bubble is a thing you have to watch. Whoever has a session next: download a
  large photo/video and look at the ring, not just the end state.

- 2026-08-10 — **D4b live verification gap (environment, unchanged from D4a).**
  Still no logged-in WhatsApp session here, so no real react/edit/revoke/pin/
  forward round-trip was performed. Verified instead: a throwaway `whatevrd`
  against isolated `XDG_*` dirs, hand-exercised over a raw socket with the exact
  params `ProtocolController` sends — all seven `message.*` commands reached the
  daemon's C2 handlers (`not_found` for messages absent from that empty store,
  `not_logged_in` for `message.forward`), while deliberately malformed variants
  (`message.star` without `starred`, `pinned` without `chat_id`) came back
  `invalid_params`, which is what proves the shapes are right — plus a `pinned`
  subscribe (`{"sub":2}` then `ready`/`exhausted`), an offscreen `whatkevr`
  launch (no QML errors), the Qt/Go/conformance suites and a qmllint
  before/after diff. Whoever picks up D4c should do a real action once a session
  is available.

- 2026-08-10 — **D4a live verification gap (environment, same as D3c).** No
  logged-in WhatsApp session is available in this environment (see the D3c
  entry below for why), so an actual send/receive round-trip through the
  ported composer could not be exercised. Verified instead: a throwaway
  `whatevrd` against isolated `XDG_*` dirs, hand-exercised over a raw socket
  with the exact `send.text`/`send.media`/`chat.typing` params
  `ProtocolController` now sends — all three reached the daemon's C2 handlers
  (`not_logged_in`, not `invalid_params`, confirming the wire shape is
  correct) — plus a headless `whatkevr` launch against that daemon (no QML
  errors) and the full Qt/Go/conformance suites. Whoever picks up D4b/D4c or
  revisits this should do a real send once a session is available.

- 2026-08-09 — **D3c live verification gap (environment, not code).** The
  hand-verification against a live daemon could not be done: the installed
  `/usr/bin/whatevrd` refuses to start against the current data dir (`database
  schema version 6 is newer than supported version 5` — the dev build has
  migrated it), and the dev build, started against the real data dir, found the
  WhatsApp session already unlinked: it archived the store as
  `whatevrd-before-logout-*.db` and came up `need_login`. Re-pairing needs the
  user's phone, so the conversation header/receipts dialog were verified against
  a throwaway daemon over a raw socket plus the Qt tests (real client, real
  socket, fake daemon) instead. One observation worth a later look, unrelated to
  D3c: while that logged-out daemon was starting, store-backed subscribes
  (`connection`, `chats`) took >30s to answer while store-free ones (`typing`,
  `presence`, `receipts`) replied immediately (`protocol: list chats for view:
  context canceled` in the log when the probe gave up). Possibly just startup
  contention behind the logout wipe; if it reproduces on a healthy daemon it is
  a real Phase-B/store issue.

- 2026-07-18 — D2a implementation readings (no PROTOCOL.md change; flag if you
  disagree): (1) **Coexistence is two live connections.** With a monolithic
  gRPC `AppController`, "page by page" means the new `ProtocolClient` and the
  gRPC channel are both up for the whole D phase; each D-step moves one page's
  bindings from `AppController` to `ProtocolController` (which owns the client),
  and E1/D7 delete the gRPC side. Nothing is dual-*driven* dangerously: both
  merely *observe* the same daemon, so their states converge (e.g. a logout seen
  by gRPC also arrives on the `connection` view). (2) **`ProtocolController`
  drives the shell gate; `AppController` still renders the shell.** `appMode()`
  and the rebuild/logout trigger key off `ProtocolController`; once it reports
  `shellVisible`, the still-gRPC chat pages render `AppController`'s data. A brief
  race (protocol online before gRPC chats load, or vice-versa) is no worse than
  today's async chat-list fill. Deep-link (`open_chat`) / window-activation stay
  on `AppController` until D3 — `ProtocolClient::openChatRequested` is left
  unwired this step to avoid double routing. (3) **The client's auto-reconnect
  subsumes AppController's transport machinery.** No channel/probe/QFileSystem
  watcher port: transport phase is derived from client-ready + a 1s cold-start
  grace + socket-exists, and the client already retries every 1s. `startDaemon`
  just launches the unit/binary and lets that loop pick up the socket. (4)
  **Two-phase/derived strings live in C++, matching the codebase.** The i18n
  status/QR strings are computed in `ProtocolController` (mirroring
  `AppController`'s wording verbatim so either stack reads identically mid-
  migration), not in QML — chosen over QML-side derivation to keep the thin-QML
  house style. (5) `login` is subscribed for the whole session; when online it
  just reports state with no `qr` (verified over socat), so no lazy-subscribe
  dance is needed and the QR pairing flow still attaches while logged out.

- 2026-07-18 — D1 implementation readings (no PROTOCOL.md change; flag if you
  disagree): (1) **Ordering is the daemon's, not the model's.** `CollectionViewModel`
  orders rows by a bytewise compare of the opaque `sort` string's UTF-8, with `id`
  as a deterministic tiebreak only — never by any item field. Keyed upsert-by-`id`
  is the protocol's universal algorithm, not frontend dedup. (2) **Items are opaque
  QVariantMaps.** The generic models expose the whole item as `ItemRole` (QML binds
  `model.item.<field>`); they interpret no field, so a new message `kind`/`fallback`
  or any view's fields flow through with zero model changes. No per-view roles, no
  QML type registration yet — that lands page-by-page in D2+ as each UI adopts a
  view. (3) **Reconnect resubscribes.** On socket drop every live `Subscription`'s
  sink gets `onReset()` and in-flight requests fail with code `io`; after the next
  `hello` the client re-issues all subscriptions (fresh window) and flushes commands
  queued while connecting. This mirrors the daemon-side slow-consumer `reset`
  contract on the client edge; no wire change. (4) **`hello` rides the normal
  request path** (id-correlated, first on the wire); other requests queue behind it.
  (5) Tests are opt-in (`-DWHATEVR_BUILD_TESTS=ON`) so `just build` never needs
  Qt6::Test; they compile the three protocol .cpp directly (Qt6 Core/Network only,
  no GUI/gRPC) and drive the real client over a real Unix socket.

## Decision log

- 2026-08-10 — **DN7 approved by Harsh:** add a `chat` object view taking a
  required `chat_id` and emitting the same daemon-owned row as `chats`, then
  remove whatkevr's interim selected-chat window chase. PROTOCOL.md amended in
  the DN7 implementation commit.

- 2026-08-10 — D7 implementation readings (no PROTOCOL.md change; **two
  behaviour narrowings to flag**): (1) **The end state is one controller, and
  the frontend-only helpers live on it.** `ProtocolController` already owned
  pure presentation state before this step (the emoji model since D6,
  `copyToClipboard` since D2a), so the last stragglers — drafts, `toCommonMark`,
  the grapheme-cluster Backspace helper, `copyImageToClipboard`/`saveMediaAs`,
  `perfLogging` — went there rather than into a new "utils" singleton. None of
  them touches the socket; splitting them out would have meant a second
  singleton whose only unifying property is "not the daemon". A rename of the
  class (it is now simply *the* controller) is left to E2's polish.
  (2) **Narrowing: a dead avatar file no longer triggers a re-fetch.** The gRPC
  delegate called `requestChatAvatar(id, force)` when the image failed to load —
  the daemon having pruned a file behind a stale DB row. PROTOCOL deliberately
  has no such command ("the old API needed `RequestAvatars`… they are gone");
  fetching is demand-driven by the subscription. So the row now shows initials
  until the daemon refreshes the path on its own. If that turns out to be
  visible in practice the spirit-correct fix is **daemon-side** (validate the
  cached file when a `chats` row is served, or re-fetch on prune), never a new
  frontend command — flag it if you want that now. (3) **Narrowing: the
  navigation transition gate is gone.** `uiTransitionActive` +
  `populateSelectedChat` deferred gRPC model work until a column slide settled;
  the protocol path has nothing to defer — `selectChat` subscribes `messages`
  and the daemon streams the window — so both were deleted rather than
  reimplemented against a controller that does not need them. Effect: the
  subscription now starts on the click frame instead of ~150 ms later. That is
  strictly earlier work, but it is work *during* the slide, so if a slow machine
  ever shows a hitch the fix is to re-add a gate around the subscribe call, not
  to resurrect the populate machinery. (4) **Drafts lost their ordering side
  effect, deliberately.** `ChatListModel` stamped a draft with "now" so the chat
  floated to the top of its section. The daemon owns `chats` sort, so the
  protocol version stores text only — a draft marks a row, it never moves it.
  The persisted key (`settings/drafts`) and the "Save unsent drafts" preference
  are unchanged, so existing drafts survive the upgrade. (5) **`qt6-grpc` left
  the packaging with the code.** It was only ever whatkevr's dependency (the
  daemon vendors its own gRPC), so leaving it in the README/AUR `depends` after
  this step would be false; the daemon's `go.mod`/`proto/`/justfile entries stay
  for E1. (6) **The daemon still fans notification `open_chat` to both the gRPC
  session bus and the protocol server** (C3's transitional behaviour). Nothing
  listens on the gRPC side any more, so it is dead weight rather than a
  duplicate — left for E1's teardown, which deletes that server outright.

- 2026-08-10 — D6 pre-implementation decisions (approved by Harsh): (1) keep
  D6 as one step rather than splitting settings/emoji/stickers; if it proves
  too large to finish safely, the session protocol's mandatory split rule
  still applies. (2) Add `search.stickers {query, limit}` as a transient query
  returning `{stickers:[...]}` in daemon order; the legacy picker search cannot
  be reproduced from a finite `stickers` window without missing matches. (3)
  Add ack-only `sticker_packs.refresh`; it forces the existing upstream pack
  refresh and all renderable changes still land through `sticker_packs`.

- 2026-08-10 — D6 implementation readings (no further PROTOCOL.md change):
  (1) `self` and `preferences` are session-long because the sidebar and message
  auto-download policy render them outside settings; `privacy` is page-scoped,
  and one `blocklist` subscription is shared while either the contact card or
  blocked-contacts page is visible. (2) Emoji recents/search remain QSettings-
  backed presentation state allowed by rule 1; only ownership moved away from
  the gRPC controller. (3) Sticker source/pack views live only while the picker
  is visible; context-menu favorite membership has its own menu-scoped complete
  favorite view. Search is the approved transient query and preserves the
  daemon array order; bounded download request bookkeeping is in-flight UI state,
  not an authoritative sticker cache. (4) Installed pack shortcuts hide
  uninstalled rows from the already daemon-ordered pack view without reordering
  or merging it. (5) Porting exposed and fixed daemon correctness issues rather
  than compensating in the frontend: omitted view limits now mean unbounded,
  every exposed sticker-row field invalidates correctly, search ties use
  `cache_key`, forced refresh returns upstream errors, and `send.sticker` is
  backgrounded because it may fetch a missing file before enqueueing.

- 2026-08-10 — D5 implementation readings (no PROTOCOL.md change; flag if you
  disagree): (1) **Queries are the one place frontend-held result state is
  right, and the model says so.** `search.chats` / `search.messages` /
  `contacts.check_phone` are one-shot request/response, and PROTOCOL.md's
  Queries section explicitly allows the frontend to render and throw their
  results away — so unlike every other surface in this phase they land in a
  plain `QAbstractListModel` (`ProtocolSearchModel`) rather than a view sink.
  It keeps each result set in its **own section** in the daemon's own order and
  never sorts or merges across them; the phone-number row is a third section,
  not a row spliced into the chat list. The guard the async path needs is a
  **generation counter**, not a cancel: the protocol client always answers, so
  a superseded reply is dropped on arrival (the gRPC path compared reply
  pointers for the same reason). (2) **The info dialog stopped being a cache.**
  The gRPC dialog fetched a card, snapshotted every field so member drill-in
  could restore it, rebuilt a member `ListModel` on every keystroke, hand-patched
  member avatars out of a `senderAvatarUpdated` signal, and kept a blocklist
  snapshot refreshed on open. All of that is deleted: the card is the `contact`
  or `group` object view, back-navigation is a re-subscribe (the daemon still
  holds the card, so it is instant *and* current), an avatar refresh or a
  network `about` is an ordinary upsert, and blocked-ness is **membership in the
  `blocklist` view** — the same compose-two-views shape as D2b2's typing-in-chat
  -rows and D4c's transfers-in-bubbles. This is the biggest net deletion of
  frontend state in the D phase so far. (3) **`group` + `group_members` is the
  Granularity rule paying off in the UI.** A join/leave/promotion moves one row
  instead of rewriting the card, and the dialog subscribes both because it
  renders both — exactly what PROTOCOL.md's canonical split describes.
  (4) **The starred page is windowed, and that is a behaviour change for the
  better.** The gRPC page called `ListStarredMessages` once and rendered the
  whole result; the view is windowed, so the page subscribes `limit:50` and
  extends `older` at the end of the list — and stars made on another device now
  arrive live instead of needing a reopen. (5) **`media.fetch_profile_picture`
  returns a path in a command response**, which looks like a rule-2 exception
  but is not: rule 4 makes media a file path and PROTOCOL.md specifies
  `{path}` for this command, exactly as it does for the `search.*` result
  envelopes. Nothing renderable is *derived* from it — the viewer opens the
  file. (6) **The composer's `@`-mention roster was ported here too**, closing
  the D4a leftover that pointed at this step: it is a `group_members`
  subscription on the *displayed* conversation (the D3c presence / D4b pinned
  lifetime rule), so a hidden or 1:1 conversation holds no roster. It is a
  *second* `group_members` subscription when the group's info dialog is also
  open — deliberate, since the two have different lifetimes, and B5b already
  recorded that a duplicate `GetGroupInfo` is harmless. Without this, mention
  autocomplete would have been the one composer surface stranded on gRPC with
  no step owning it before D7's delete. (7) **"Starred/pinned pages" in the row
  was one page, not two.** whatkevr has no pinned page; its pinned surface is
  the conversation banner, which D4b already moved onto the `pinned` view.
  (8) `SearchResultsModel`, `StarredMessagesModel`, `PinnedMessagesModel` and
  the AppController methods behind them are now off the render path entirely
  and are left in the tree for the D7/E1 delete, like `ChatListFilterModel`.

- 2026-08-10 — D4c implementation readings (no PROTOCOL.md change; flag if you
  disagree): (1) **The bubble composes two views; neither is copied into the
  other.** A downloading bubble needs the message row (kind, thumbnail, durable
  `media.download_error`) *and* the `transfers` row (bytes so far), and PROTOCOL
  keeps them apart on purpose — one is slow-changing durable state, the other is
  fast-changing work-in-progress, the *Granularity* split. `ProtocolMessageModel`
  therefore reads `transfers` through by message id when a role is asked for and
  stores nothing; the daemon keys that view by `message_id`, which is what makes
  the join a lookup rather than a merge. Precedent: D2b2's `chatTyping(chatId)`
  and D3c's typing-over-presence header. (2) **No optimistic "downloading"
  flag** — the D4b doctrine again. The gRPC controller inserted the id into
  `m_mediaDownloadingMessageIds` on click so the spinner appeared instantly; the
  protocol version waits for the daemon, which publishes the transfer *before*
  the network leg starts (`PublishMediaDownloadChanged(..., true, ...)` is the
  first thing `DownloadMessageMedia` does), so the spinner still comes up within
  one local round trip and there is no client state that can disagree with the
  daemon. (3) **Progress invalidation is coarse on purpose.** Any change to
  `transfers` re-reads the two download roles across the whole timeline rather
  than mapping transfer rows back to timeline rows: the view is empty almost
  always, holds a handful of rows at worst, the daemon throttles to ~7 events/s
  per transfer, and a `dataChanged` only wakes delegates that are on screen. The
  bookkeeping to be precise would cost more than it saves. (4) **The image
  viewer was already ported.** D4c's row names it, but `ConversationPane`'s
  `ProfilePictureViewer` lightbox has been opening off the protocol model's
  `media.path` since D3b — nothing to do, so nothing was done. (5) **Two reads
  deliberately left on `AppController`, both for their owning step.** The
  auto-download *preferences* (`prefs.autoDownloadPhotos`…) come from the
  `preferences` view, which is D6's; porting the read now would mean two
  spellings of those keys in the tree until D6 flips the settings pages. And
  `media.fetch_profile_picture` has exactly one caller, `ContactInfoDialog`, so
  it ports with D5's info dialogs — wiring it here would be an unused invokable,
  the same call D2b1 made about `chat.ensure_direct`. The download path itself
  is fully protocol-driven either way. (6) `saveMediaAs` and
  `copyImageToClipboard` stay on `AppController`: local file utilities with no
  daemon command behind them (as noted in D4b).

- 2026-08-10 — D4b implementation readings (no PROTOCOL.md change; **one
  behaviour narrowing needs your call**): (1) **No optimistic updates, and that
  is the point.** The gRPC controller applied every action locally before the
  daemon confirmed it (`applyOptimisticEdit`/`applyOptimisticReaction`/
  `applyOptimisticStar`, a cached-message pin/unpin round-trip) and rolled back
  on error — ~120 lines of frontend state plus a rollback path per action. The
  protocol versions read only the error out of the response; the daemon re-upserts
  the row through `messages`/`pinned`. That is rule 2 taken literally, and over a
  local socket the round-trip is not perceptible. If a laggy daemon ever makes a
  reaction feel late, the spirit-correct fix is daemon-side (publish the local
  mutation before the network leg), not a frontend echo. (2) **The pinned banner
  follows the *displayed* conversation, not the selection**, matching D3c's
  presence rule — a conversation off screen holds no `pinned` subscription.
  `pinnedMessagesReady` is `no subscription || model ready`, so the layout-settle
  logic in `ConversationPane` keeps working unchanged; the one Connections handler
  moved from `onPinnedMessagesReadyChanged` to `onPinnedMessagesChanged` (the
  view's single "shape changed" signal), which fires more often but is idempotent.
  (3) **Banner previews prefer localized placeholders, then `fallback`.** Text
  renders its text, image/sticker/revoked render the same localized strings the
  gRPC banner used, anything else renders the row's mandatory `fallback` (rule 5).
  Same policy D3a set for the timeline; using `fallback` for *every* kind would
  have regressed those three strings to daemon-side English. (4) **The forward
  picker gets its own `chats` subscription** rather than reusing the sidebar's,
  because the sidebar's carries the user's all/direct/groups filter and the picker
  must offer every chat. Its lifetime is the dialog's (the D3c `receipts` shape).
  **Narrowing to flag:** that subscription is `archived:false`, so *archived chats
  are no longer forward targets* — the gRPC model held active and archived rows in
  one list. PROTOCOL's `chats` `archived` param is a plain bool with no "both", so
  the alternatives are a second subscription presented as its own section (the
  D2b2 footer shape, more UI than this step warrants) or concatenating two views
  in the frontend (merging — forbidden). Say the word and it becomes a sectioned
  picker. (5) **The last `ChatListFilterModel` is gone from the UI.** The picker's
  search is now a filter over the rows it already holds, which PROTOCOL blesses
  explicitly for `group_members` ("member search is presentation-side filtering");
  the rows keep the daemon's order, and the class itself is left in the tree for
  the D7/E1 delete. (6) Copy/markdown helpers, the emoji model, sticker favorites
  and `saveMediaAs` remain on `AppController` — they are D4c/D6 surfaces or pure
  frontend utilities with no daemon command behind them.

- 2026-08-10 — **D4 split into D4a (composer send paths, done)/D4b (message
  actions)/D4c (media download + transfers + image viewer)** (implementation
  judgement; flag if you disagree). D4 as written bundled four materially
  different UI surfaces — the composer's own text/image send paths, the
  message-bubble context menu's react/edit/revoke/delete/star/pin/forward,
  media download progress plus a real `transfers` view (the gRPC path had no
  progress at all, so this is new UI, not a straight port), and the
  full-screen image viewer — spanning roughly 7300 QML lines and ~1600 C++
  lines across nine files (survey in-session, not separately recorded). Same
  reasoning as the D2/D3 splits: doing it as one step would make regressions
  in any one surface hard to isolate, and each has its own command shape and
  verification story. **Sticker sending moved to D6, not a D4 sub-step**: the
  survey's first pass grouped `send.sticker` into D4a alongside text/media
  because MessageComposer.qml exposes it as a peer send path, but sending is
  actually issued by `StickerController` (picker/download/favorite state,
  ~800 combined lines), which is entirely D6 territory ("sticker/emoji
  pickers") and shares none of D4a's composer plumbing — MessageComposer's own
  signal contract is only `sendTextRequested`/`sendImageRequested`, no sticker
  signal. D6's row amended to say so explicitly. D4a readings (no PROTOCOL.md
  change): (1) unlike gRPC's `sendText`/`sendImage`, which read the command
  response and called `applyMessageEvent` to insert the sent message locally,
  the protocol versions read only the error out of the response — the sent
  message arrives through the ordinary `messages` view upsert like any other
  message (rule 2: command responses carry ids/errors, never render data).
  This is a simplification the port gets for free, not a design choice made
  here. (2) A send while an unread anchor is showing clears it (the user has
  now seen past it) — ported from `AppController::dismissUnreadAnchor`,
  called at the same call sites. (3) The composing-indicator dedupe (a "stop"
  only sends for the chat a "start" was actually sent for) is ported
  verbatim from `AppController::setChatComposing`'s `m_localComposingChatId`
  tracking. (4) Mentions/`@`-autocomplete, the emoji picker, and drafts stay
  on `AppController` for now: mentions need the `group_members` view, which
  `ProtocolController` does not yet subscribe (that lands with D5's info
  dialogs); the emoji picker is pure frontend state with no daemon surface;
  drafts are frontend-only state rule 1 already allows, and D2b1 already
  established the pattern of reading them cross-stack until their owning
  surface (the composer) ports — which is now, but moving them was not
  required by this step's scope, so they were left as they were.

- 2026-08-09 — D3c implementation readings (no PROTOCOL.md change; flag if you
  disagree): (1) **Presence is subscribed for what the conversation shows, not
  for what is selected.** The `presence` sub follows the same visibility gate as
  the messages window (`setConversationVisible`), because subscribing is what
  drives the upstream WhatsApp presence request — a conversation that is off
  screen should not be asking WhatsApp for availability. Selection alone (which
  survives the status page as presentation state) does not. (2) **The header
  composes two views; it does not ask the daemon for a composed string.**
  `typing` is global and unsolicited, `presence` is per-chat and demand-driven —
  PROTOCOL keeps them separate for exactly that reason — so the header reads
  membership in `typing` first and falls back to availability/last-seen. The
  wording (`typing...`, `online`, `last seen …`) is C++-side and mirrors
  `AppController` verbatim so either stack reads identically mid-migration.
  (3) **The receipts dialog holds no receipt state.** The gRPC dialog fetched an
  `info` snapshot once and then hand-patched member avatars out of a
  `senderAvatarUpdated` signal; that whole mechanism is deleted. The daemon
  re-derives `GetMessageInfo` on every relevant event, so a member reading the
  message — or their avatar refreshing — is an ordinary upsert. The dialog's
  read/delivered sections are presentation-side filtering of rows it already has
  (like `group_members` search), and each section keeps the daemon's order.
  (4) **Fields the `receipts` view legitimately lacks are read from the views
  that own them**, not cached at open: `is_group` from the `chats` row, the Sent
  time from the `messages` row. If the timeline row ever leaves the window the
  Sent time renders "—" rather than going stale. (5) The dialog reaches its rows
  through an invokable plus a `messageReceiptsRevision` tick (the `typing`
  pattern) rather than a QSortFilterProxy pair — no frontend sorting machinery.
  (6) A rejected subscribe (`not_found` for an unknown message) becomes the
  dialog's error text; an `io` failure is ignored because the client
  auto-resubscribes after reconnect, matching the messages subscription.

- 2026-07-19 — D3b implementation readings (no PROTOCOL.md change): (1) The
  conversation now renders `ProtocolMessageModel` in ascending daemon `sort`
  order; `MessageView` and its row scrollbar were converted from the legacy
  newest-first/inverted geometry rather than adding a frontend reorder layer.
  Older extends prepend and restore a presentation-only viewport anchor; phone
  backfill keeps reapplying that anchor for arbitrarily delayed older upserts
  until the user scrolls. (2) `ProtocolController` owns one authoritative
  `messages` subscription for the selected chat. Unread and message-id anchors
  keep independent older/newer frontier state; off-window jumps replace the
  subscription, and jump-to-bottom re-subscribes `latest` per Model A. Missing
  jump targets restore the prior window instead of blanking the timeline. (3)
  `chat.mark_read` carries the highest actually-visible message id observed by
  the debounced UI; a pending watermark is flushed before re-anchoring. (4)
  `session.update` reports an active chat only while the conversation pages are
  visible. Protocol `open_chat` is now the primary route; selection is mirrored
  into the frozen gRPC controller only for composer/pins/search and other D4/D5
  surfaces still awaiting their own port. (5) Steady-state `reset`, reconnect,
  initial-fill reset, and reset-during-extend preserve the appropriate metadata
  and frontier completion. Late replies are guarded by QObject identity and
  stale daemon subscriptions are explicitly removed. (6) QML compilation and
  an offscreen launch cover the rewritten timeline surface; socket/controller
  lifecycle behavior has focused Qt tests. Presence header and receipt-dialog
  scope remain untouched for D3c.

- 2026-07-19 — **D3 split into D3a/D3b/D3c** (implementation judgement; no
  PROTOCOL.md change). The original step combines three substantial mechanics:
  adapting a mature newest-first gRPC message model/UI to daemon-sorted ascending
  whole items; owning selection plus latest/unread/message-id subscription,
  directional-pagination, read-watermark, session and deep-link lifecycle; and
  two independently scoped live views (`presence` per selected chat, `receipts`
  per open dialog). Doing all three together would make ordering and lifecycle
  regressions hard to isolate. **D3a** landed the read-only presentation adapter
  and completion primitive; **D3b** ports timeline ownership/navigation; **D3c**
  ports presence/receipts chrome. D3a readings: (1) `ProtocolMessageModel` mirrors
  every source insert/move/remove/reset and never orders by item fields; ascending
  neighbors determine date/sender grouping. Its `m_textById` state is only lazy
  presentation-derived markup/font metrics (allowed presentation state), never a
  copy of authoritative daemon rows. (2) Unsupported/new message kinds render
  their mandatory wire `fallback`; known image/sticker kinds retain caption/media
  presentation. (3) Direction/status strings map to the existing UI enum numbers
  only to select icons/labels; no state is derived. (4) Repeated wire `ready`
  events needed an unconditional `readyReceived(exhausted)` signal: the existing
  `readyChanged` correctly remains property-change-only and cannot complete two
  extends with equal exhaustion. (5) Mirroring exposed a D1 Qt model-contract bug:
  a sort-key move removed its row before `beginMoveRows`; mutation now occurs
  between begin/end, covered for source+adapter by `QAbstractItemModelTester`.

- 2026-07-19 — D2b2 implementation readings (no PROTOCOL.md change; flag if you
  disagree): (1) **Archived is a sibling subscription, presented as a footer.**
  PROTOCOL returns active/archived as disjoint `chats` subscriptions; rather than
  a proxy that concatenates or a section role, the active list is the `ListView`
  and the archived chats render in its **`footer`** (a collapsible "Archived (N)"
  header + a `Repeater` over `archivedChatsModel`). This scrolls as one list (the
  old single-scroll UX) with zero frontend merging — each model stays a pure
  keyed/sorted collection; the row's own `archived` flag drives per-row collapse.
  Both subs carry the same sidebar `filter`, so the archived section narrows with
  the sidebar exactly like the active list. (2) **Typing is membership, not a
  merged field.** The delegate's `isTyping` asks `chatTyping(chatId)` (is the
  chat_id present in the global `typing` view) — the daemon owns the `typing`
  collection; the frontend composes it into the row at render time, never folding
  typing state into the chat row. A `typingRevision` counter (bumped on any typing
  model change) forces the binding to re-evaluate, since a bare function call is
  not reactive. (3) **The sync strip renders the single current `sync` item.** The
  gRPC `AppController` ran a cross-event *cursor* (suppressing auxiliary sync types
  while a primary is active, picking among concurrent types, keeping max percent).
  The `sync` **object view** already delivers one current state, so the strip
  derives visible/percent/title/detail from it directly, keeping only two bits of
  presentation policy: hide `on_demand` (per-chat history has its own loading UI)
  and never let percent jump backwards within one visible session. The dropped
  type-dedup can, in principle, show a brief auxiliary-type label where gRPC would
  have suppressed it; if that ever matters the spirit-correct fix is **daemon-side**
  (have the `sync` view compute a single displayable state), not a frontend cursor.
  Flag if you want that daemon change now. (4) **Selection/search/drafts stay on
  gRPC** (D3/D5/D4): D2b2 only added read views; the list still routes clicks to
  `AppController.selectChat` and reads drafts from `AppController.chatDraft`.

- 2026-07-19 — **D2b split into D2b1 + D2b2** (implementation judgement; flag if
  you disagree). D2b (chat list *and* `typing` overlay *and* `sync` strip on a
  mature gRPC UI) was materially larger than D2a: the chat list alone needs row-
  shape adaptation, daemon-side filter params, list commands, selection that must
  still cross to the gRPC conversation until D3, and loading/empty — so it split
  into **D2b1** (the chat-list widget) and **D2b2** (`typing` overlay + `sync`
  strip). Two sub-decisions inside D2b1, both to keep it one clean session:
  (1) **Archived section moved to D2b2.** PROTOCOL's `chats` view returns active
  and archived as *disjoint* subscriptions (`archived:false`/`true`); reproducing
  the current single-list collapsible "Archived (n)" section means a second
  subscription presented alongside the first, which pairs naturally with the
  other D2b2 overlays. D2b1 subscribes `archived:false` only; the section QML is
  left inert (never triggers) rather than deleted, so D2b2 just adds the second
  sub. (2) **`chat.ensure_direct` deferred to D5.** Its only caller is the phone-
  number search result (start-a-new-chat), which is part of unified search (D5);
  wiring it in D2b1 would be an unused invokable. The chat-list context menu needs
  only pin/archive/mute.
- 2026-07-19 — D2b1 implementation readings (no PROTOCOL.md change; flag if you
  disagree): (1) **Filtering is a re-subscribe, not a proxy.** The three sidebar
  filters (all/direct/groups) are the `chats` view's `filter` param, so switching
  filter deletes the subscription and subscribes fresh (clearing the model first
  so the old filter never flashes). This *removed* the frontend `ChatListFilterModel`
  QSortFilterProxyModel from the pane — a net deletion of frontend filtering, the
  spirit-correct direction. (2) **The list uses the generic `CollectionViewModel`;
  QML binds `model.item.<field>`.** No per-view C++ roles: the delegate reads the
  daemon `chats` row's own fields (`id`/`name`/`preview`/`unread`/`pinned`/…). The
  daemon emits status/direction as *strings* (`sent`/`delivered`/…, `outgoing`)
  while the delegate wants int enums, so a tiny `statusToInt`/`directionToInt`
  maps them **in QML** — pure presentation (which icon), no state derived; same for
  the `initialsFor` avatar fallback (the row carries no precomputed initials).
  (3) **Selection stays gRPC until D3.** Clicking a row still calls
  `AppController.selectChat` (loads the gRPC conversation) and `current` binds
  `AppController.selectedChatId`; only the *rows* moved to the protocol. This is the
  same "ProtocolController drives, AppController still renders" strangler shape as
  D2a. (4) **Drafts read from the gRPC `AppController.chatDraft(chatId)`.** Drafts
  are frontend presentation state (rule 1 explicitly allows them); the composer that
  authors them is still gRPC until D4, so the delegate reads the draft from there,
  re-evaluating on `selectedChatId` changes (leaving a chat is when its draft is
  committed) — a mid-migration cross-stack read, not new daemon state. (5)
  **`chatsLoading` = `!model.ready`, `chatsEmpty` = `count == 0`** (not gated on
  ready), so the busy spinner shows during the initial fill and the "No chats"
  placeholder only once ready with zero rows — matching the old gRPC semantics.

- 2026-07-18 — **D2 split + D-phase controller shape** (decided by Harsh). D2
  was too big for one session (whole connection/login state machine *and* the
  chat list + typing on a monolithic gRPC controller), so it split into **D2a**
  (`connection`/`login` pages, done) and **D2b** (`chats` + `typing` + `sync`
  strip). The ported pages get a **new C++ `ProtocolController` singleton** that
  owns the `ProtocolClient` and generic view models and derives the pages'
  bindings — running *beside* `AppController` (both connections live), not
  folded into it and not derived in QML. Rationale: matches the codebase's
  thin-QML/fat-C++ style, keeps each stack's logic separable so D7 is a clean
  delete, and avoids putting a real state machine in QML. Later D-steps subscribe
  their views on the same client via `ProtocolController::client()`.

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
- 2026-07-07 — B4 split into B4a (`typing`, done), B4b (`presence`), B4c
  (`receipts`). Reason: three views with different mechanics — `typing` is a
  pure daemon-event collection; `presence` needs the upstream WA presence
  subscription driven by the subscription lifecycle (an actions interface);
  `receipts` re-derives `GetMessageInfo` off a different store table. Same
  pattern as B3a/b/c. No PROTOCOL.md change — all three views are already
  specified there.
- 2026-07-07 — B4a implementation reading (PROTOCOL.md unchanged, flag if you
  disagree): (1) `DaemonEventChatPresence` is overloaded (composing *and*
  availability). The `typing` view treats a **non-empty `SenderID`** as "this
  is a composing event" and ignores SenderID-empty availability events; B4b's
  `presence` will take the complementary half. This is the existing daemon
  event contract, not a new field. (2) The daemon tracks a single composing
  sender per chat (`presenceState.SenderID` is one slot), so a typing item's
  `senders` list holds ≤1 entry today; it is modelled as a list so the wire
  shape needs no change if the daemon later tracks concurrent group typers.
  (3) Sort key for this unwindowed view is the chat id (order is immaterial;
  a stable deterministic key keeps upserts idempotent). New daemon accessor
  `ComposingChats()` supplies the initial fill, TTL-filtering stale entries
  whose expiry timer has not yet fired.
- 2026-07-07 — B4c required a **new daemon event**, `DaemonEventMessageReceipt`
  (implementation finding; PROTOCOL.md unchanged, flag if you disagree). The B4c
  plan note said the `receipts` view "invalidates on `DaemonEventMessageUpdated`/
  `MessageDeleted`." That is insufficient for groups: `applyParticipantReceipt`
  advances a message's aggregate status (and fires `DaemonEventMessageUpdated`)
  only once *every* member reaches a tick state, so a single member's
  delivered/read receipt — precisely the per-member data the receipts view
  exists to show live — records to the store with **no event**. Added a daemon
  event fired on every recorded participant receipt (in `applyParticipantReceipt`
  after the store upsert, gated `!offlineSync` to match the caller's existing
  status-publish gating; `broadcastDaemonEvent` is non-blocking so the extra
  traffic is safe). It carries only message+chat ids; the receipts view keys off
  it to re-derive from the store, and status-only views (messages/chats) ignore
  the new kind. This is a daemon-event-surface addition, **not** a wire-grammar
  or PROTOCOL.md change, and the frozen gRPC daemon-event stream ignores unknown
  kinds — so no sign-off was needed. Other B4c readings: (1) actions seam widened
  from B4b's `PresenceActions` to a combined `DaemonActions`
  (`PresenceActions`+`MessageInfoActions`), as B4b's decision log anticipated.
  (2) Direct-chat receipts: `GetMessageInfo` exposes only the aggregate
  delivered/read for 1:1 (no participant jid/name), so the sole item uses a
  stable sentinel id `"peer"` and appears only once delivery begins — asymmetric
  with groups (which list all members from the start), a data-availability
  limitation, not a spec bend; flag if a real recipient jid/name is wanted here
  (would mean enriching `GetMessageInfo`, which the frozen gRPC path also
  returns). (3) The view holds no receipt state — every invalidate re-derives
  `GetMessageInfo`, keeping the store authoritative (the engine's content-diff
  suppresses no-op upserts).
- 2026-07-07 — B4b implementation readings (PROTOCOL.md unchanged, flag if you
  disagree): (1) The plan named `PublishCachedChatPresence` for the initial
  fill, but that replays cached presence *asynchronously* by re-broadcasting to
  every daemon-event subscriber. Instead the view reads a new **synchronous**
  daemon accessor `ChatAvailability(chatID)` at Open (the availability-half twin
  of B4a's `ComposingChats()`): the `ready` then honestly reflects any cached
  state, and no redundant upserts spray other presence subs. The upstream
  `SubscribeChatPresence` (the actual *point* of subscribing — availability only
  flows on request) is still called; only the replay mechanism differs.
  (2) `presence` is the first view that drives an upstream action, so
  `RegisterDaemonViews` gained an `actions PresenceActions` param; a nil actions
  (fixture/tests, and B4b-irrelevant callers) simply skips the upstream call.
  B4c will widen this actions seam for `GetMessageInfo`. (3) The daemon tracks a
  single availability slot per chat and availability is an individual-only WA
  signal (group presence subscribe is a no-op), so the view carries ≤1 item,
  keyed by the participant jid (== chat_id for a direct chat); modelled as a
  keyed collection so group availability needs no wire change. (4) `last_seen` is
  emitted only while `offline` — an online contact's event carries last_seen 0
  and the frontend renders "online" regardless.
- 2026-07-07 — B5 split into B5a (`self`+`contact`, done) and B5b
  (`group`+`group_members`). Reason: two data-source families — the first two
  are `app.ContactInfo` object views sharing the status/avatar two-phase
  overlay; the second two both derive from `GetGroupInfo`. Same split-by-seam
  pattern as B3a/b/c and B4a/b/c. No PROTOCOL.md change — all four views are
  already specified there.
- 2026-07-07 — B5a implementation readings (PROTOCOL.md unchanged, flag if you
  disagree): (1) **Two-phase is done as re-upserts, not a patch grammar.** The
  network "about" text arrives on the *existing* `DaemonEventContactInfoUpdated`
  (which carries only `{jid, status}`) and is folded onto the held card, which
  re-upserts whole — honoring "split the view, never patch the grammar." (2) The
  view must **not** re-fetch `GetContactInfo`/`SelfProfile` on a
  `ContactInfoUpdated`: that call itself spawns the async status fetch that
  *emits* `ContactInfoUpdated`, so re-fetching on it would loop. Enrichment is
  therefore an in-place overlay; genuine profile changes come through the
  distinct `SelfProfileChanged` (for `self`), which is safe to re-fetch on. (3)
  Avatar overlay matches `Kind==Sender && ID==jid`; the primary (PN-form) avatar
  subject id equals the card's normalized jid, so the refresh the card renders is
  caught, but a LID-form refresh for the same person carries a different id and is
  not matched — acceptable, the primary is what shows. (4) `self` subscribed while
  logged out opens **empty** (no item) rather than erroring, and fills on
  `SelfProfileChanged` or the connection coming up — so a settings page can
  subscribe before login without a resubscribe dance. (5) A `self` re-fetch on
  `SelfProfileChanged` momentarily blanks the overlaid `about` (the fresh
  `SelfProfile` carries no status; it re-streams via `ContactInfoUpdated`) — a
  brief, self-correcting flicker, not a lost field. (6) `contact` with a bad/group
  jid → `invalid_params` (the only failure `GetContactInfo` reports; an
  unknown-but-valid user still returns a card from jid+phone).
- 2026-07-07 — B5b implementation readings (PROTOCOL.md unchanged, flag if you
  disagree): (1) **The flagged `group` spec-vs-data gap was not real.** PROTOCOL's
  `owner`, `my_role`, and announce/locked flags are all present in whatsmeow
  `types.GroupInfo` (`OwnerJID`, `IsAnnounce`, `IsLocked`, per-participant
  `IsAdmin`/`IsSuperAdmin`); they were simply not carried through `app.GroupInfo`.
  Plumbing them is mechanical, not a spec change, so **no sign-off was needed** —
  `app.GroupInfo` gained `OwnerJID`/`MyRole`/`IsAnnounce`/`IsLocked`, populated in
  `wa.refreshGroupInfoLive`. (2) `my_role` is resolved in the same participant
  pass as the member list, keying off `ownParticipantJIDs()` (both PN and LID
  forms) against the canonical id the member rows use, so the "me" row and my_role
  always agree; not-found defaults to "member" (never over-grants the composer).
  Shared `app.GroupRoleString` keeps GroupInfo.MyRole and the group_members `role`
  field on one vocabulary. (3) These fields are **live-only** (the stored-
  participant fallback can't know roles/owner/flags), so they land in phase two —
  phase one lists members as "member" with empty owner/my_role/flags. This is the
  same two-phase shape as B5a, and matches "announce/locked feeds composer
  lockout": until the live card arrives announce is false, so the composer is not
  wrongly locked. (4) `group` omits the member array entirely (PROTOCOL: "No member
  array — the chat header and card chrome need only this"); `member_count` is
  `len(Members)`. The info dialog subscribes `group` + `group_members` together;
  each triggers its own `GetGroupInfo`, so the live fetch runs twice — harmless
  (idempotent events). (5) `group_members` orders rows superadmin<admin<member
  then display-name then jid — a user-facing roster wants a sensible order (unlike
  the transient `receipts`/`presence` lists that key on jid alone), and the daemon
  owns sort so the frontend can only filter, not reorder. A promotion/rename moves
  a row (new sort key) as an ordinary upsert; a departure is a `remove` from the
  roster diff. (6) `group`/`group_members` with a non-group or unknown chat_id →
  `invalid_params` (the only failure `GetGroupInfo` reports; an in-store group with
  no participants yet returns an empty roster, not an error).
- 2026-07-07 — B6 split into B6a (`privacy`/`preferences`/`blocklist`, done) and
  B6b (`starred`/`pinned`). Reason: two data-source families — the first three
  are settings object/collection views over the `SettingsActions` seam (upstream
  WA + daemon-persisted prefs, "fetch-at-subscribe, re-fetch-on-event", like B1's
  object views); the last two are windowed **message-row** views over the store
  reusing the B3 `messages` item shape. Same split-by-seam pattern as B4a/b/c and
  B5a/b; five views was the largest per-step count in Phase B. No PROTOCOL.md
  change — all five views are already specified there.
- 2026-07-07 — B6a required a **new daemon event** `DaemonEventPreferencesChanged`
  (implementation finding; PROTOCOL.md unchanged, flag if you disagree). Same shape
  as the B4c `DaemonEventMessageReceipt` finding: app preferences are
  daemon-persisted and had **no** live-update event, so an open `preferences` view
  could never update when they change. `privacy` and `blocklist` already had their
  change events (`DaemonEventPrivacySettingsChanged`/`DaemonEventBlocklistChanged`);
  preferences did not. Added a payload-free event fired from `SetAppPreferences`
  (the single mutation point; C3's `preferences.set` will call the same path), off
  which the view re-reads `GetAppPreferences`. This is a daemon-event-surface
  addition, **not** a wire-grammar or PROTOCOL.md change, and the frozen gRPC
  daemon-event stream ignores unknown kinds — so no sign-off was needed. Other B6a
  readings (PROTOCOL.md unchanged): (1) `privacy`/`blocklist` are network-backed and
  open **empty** while logged out (like `self`), filling on `ConnectionChanged`
  when still unloaded; `preferences` is always available (defaults) and never opens
  empty. (2) `privacy` applies the `PrivacySettingsChanged` snapshot **directly**
  (no re-fetch — the payload carries the full settings), while `blocklist`
  re-reads `GetBlocklist` on its payload-free change event. (3) `blocklist` rows
  sort by lowercased display name then jid (`blocklistSortKey`) — a user-facing
  settings list wants a sensible order (unlike the transient jid-keyed
  `receipts`/`presence` lists), and the daemon owns sort so the frontend only
  filters; a rename moves a row as an ordinary upsert, an unblock is a `remove`
  from the map diff. (4) A blocked contact's avatar refresh overlays in place
  (Sender-kind, id == held jid); a LID-form refresh carries a different id and is
  not matched — same acceptable caveat as the contact card.
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
- 2026-07-07 — B7 split into B7a (`stickers`/`sticker_packs`/`sticker_pack`,
  done) and B7b (`transfers`). Reason: sticker views are store/list/fetch
  surfaces with the existing sticker library events, while `transfers` is
  daemon active-download state (`DaemonEventMediaDownloadChanged`). No
  PROTOCOL.md change — all four views are already specified there.
- 2026-07-07 — B7a implementation readings (PROTOCOL.md unchanged): (1) Sticker
  item and pack item field names carry over from the frozen sticker RPC field
  sets (`cache_key`, `local_path`, `mime_type`, `is_animated`, `tray_local_path`,
  `contents_fetched`, etc.), with `id == cache_key` for stickers and `id == pack
  id` for packs. (2) `stickers` requires `source` (`recent`|`favorite`|`all`),
  because PROTOCOL lists no default; an unwindowed subscription uses the existing
  picker default cap of 200 rows rather than an unbounded SQL list. (3) The
  `sticker_pack` view opens from the pack shell already in the store; if contents
  are not fetched yet, subscribe returns and the initial window may be empty,
  then a background `GetStickerPack` fetch fills the store and invalidates the
  view so whole sticker rows land as normal upserts. Missing `pack_id` is
  `invalid_params`; unknown pack shell is `not_found`. (4) `sticker_packs` reads
  cached pack rows immediately and starts a best-effort background index refresh;
  installed/tray/content changes invalidate through `DaemonEventStickerLibraryChanged`.
  (5) Sort keys are daemon-assigned from the store/action result order (and pack
  order for `sticker_pack` contents), so frontends still only apply keyed upserts
  ordered by opaque `sort`.
- 2026-07-07 — B7b implementation decision (PROTOCOL.md amended, approved by
  Harsh): `transfers` is **active work only**. A media download start/progress
  appears as a `transfers` upsert keyed by `message_id` with `direction:"download"`,
  bytes, and daemon sort; terminal success or failure removes that row. Durable
  renderable failure state belongs to the message row as `media.download_error`,
  stored in `messages.media_download_error` and delivered by ordinary `messages` /
  `starred` / `pinned` upserts. Retry clears `download_error` via a message upsert;
  success clears it while setting `media.path`. This keeps views as current daemon
  state: frontends never have to catch or cache a terminal failure event, and slow
  clients cannot miss the error. The old `transfers.error` wording is narrowed to
  optional active-transfer error detail, not terminal failure history.
- 2026-07-07 — C1 applied the earlier `chat.mark_read` decision: PROTOCOL.md now
  lists `up_to_message_id` as a required param, and the daemon marks/sends read
  receipts only through that message's timestamp+rowid frontier. Same-timestamp
  messages beyond the visible row are not over-marked; updates remain ordinary
  `chats`/`messages` view upserts.
- 2026-07-07 — C2 command implementation readings (PROTOCOL.md unchanged):
  command responses stay minimal (`message_id`/`message_ids` or `{}`/`path`), and
  all renderable message/media state still reaches frontends through views. The
  protocol's `send.media` `mentions` param is supported by a new
  `wa.SendMediaWithMentions` wrapper so the frozen gRPC `SendMedia` signature and
  generated proto remain untouched. `message.edit`/`message.revoke` map expired
  precondition text to protocol `expired`; other ineligible message operations map
  to `rejected` rather than leaking gRPC status names.
- 2026-07-07 — C3 implementation readings (PROTOCOL.md unchanged): (1) Settings /
  contact / sticker commands still return only `{}`; renderable state is forced
  through existing views by publishing/reusing daemon events after local
  mutations (`PrivacySettingsChanged`, `PreferencesChanged`, `SelfProfileChanged`
  + `ContactInfoUpdated`, `BlocklistChanged`, sticker library/download events).
  (2) `preferences.set` is the protocol's one partial-object command, so the
  handler reads current daemon prefs, overlays only present bool fields, then
  persists the full object. (3) Search queries are transient and return ordered
  arrays under `chats` / `messages` plus `has_more`; rows reuse the daemon-owned
  chat/message wire shapes, so frontends still do not sort or cache them. (4)
  `open_chat` routing targets the most recently updated focused protocol session;
  if none is focused, it falls back to the most recently updated live protocol
  session so notification clicks raise an existing unfocused window instead of
  spawning a duplicate. During migration the notification worker fans the same
  open request to both the legacy gRPC session bus and the protocol server; this
  may be idempotently duplicated for a transitional frontend that is connected to
  both, but it preserves daily-use gRPC behavior until D7.
- 2026-07-07 — **C4 whole-migration audit remediation** (landed as the C4 step;
  single branch, focused commits). A read-only audit across phases A/B/C
  surfaced ~30 findings, folded here. Notable decisions and drift resolutions:
  - **WS2 daemon-event overflow → resync sentinel.** A full per-subscriber
    daemon-event buffer previously dropped events with only a global counter,
    permanently desyncing fold/edge-triggered views (`typing`, `presence`).
    Now the daemon drains that subscriber's channel and enqueues a single
    `DaemonEventResync` sentinel; each view session force-reconciles on it
    (store/network views re-read source, fold views rebuild from their snapshot
    accessors). This extends PROTOCOL.md's existing conn→client slow-consumer
    clause one layer up (daemon→session); no wire change.
  - **F7 correction to the C3 reading above.** `preferences.set`'s read-modify-
    write was moved *into the daemon* (a single atomic merge) so concurrent
    callers can no longer lose fields; the handler only forwards present fields
    (rule 1: the daemon owns state).
  - **F11 error segregation.** `internal/protocol` no longer imports
    `google.golang.org/grpc`; the daemon core (app/wa) raises transport-neutral
    `app.CommandError` values, and each boundary maps them — the protocol layer
    to its error codes, the frozen gRPC server via a single unary interceptor.
    F10: not-logged-in is now its own code and group JIDs are rejected on
    contact-only commands instead of leaking through as `internal`.
  - **WS8 PROTOCOL.md amendments (document current behavior, signed off).**
    Query result envelopes (`{chats:…}`, `{messages:…, has_more}`); `open_chat`
    unfocused fallback; an anchored `messages` window that has reached the live
    edge keeps absorbing contiguous new messages (no gap); `transfers.direction`
    values (`download`; uploads an unmodelled known gap); `is_business` is
    phase-one (not network-fetched) contact data; `starred` is ordered by
    message timestamp, not star time (the store records no star time).
- 2026-08-10 — **DeezNuts DN4/DN5/DN6 taken in one session at Harsh's explicit
  request**, against the one-row-per-session rule in the Session protocol
  above. Recording it so the ledger does not read as drift: the three reports
  arrived together, Harsh asked for all of them in one pass, and each landed
  with its own reasoning and (for DN6) its own test. DN7 came out of DN6 and is
  *not* implemented — it is a PROTOCOL.md addition and waits for sign-off.
- 2026-08-10 — **DN6 measurement note, for whoever tunes this next.** The
  numbers in the DN6 row were taken by hand over the live socket, not
  estimated, and they say something worth keeping: the daemon is not the slow
  part of opening a chat (a `messages` subscribe at `limit:80` is 4-6 ms
  against a 53k-message store). What was slow was the frontend asking for
  things it did not need — 917 chat rows to paint a dozen, twice, on every
  filter switch. Before reaching for `kMessagePageSize` or the daemon's query
  plan, measure: the remaining chat-open cost is QML delegate construction.
