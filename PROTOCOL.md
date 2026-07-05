# The Whatevr Protocol

**Status: DRAFT — protocol version 1 (not yet implemented). No backwards compatibility with the retired gRPC API.**

This document is the contract between `whatevrd` and every frontend. It is the
source of truth: the daemon implements this document, not the other way
around. If the daemon and this document disagree, one of them has a bug and
it is probably the daemon.

## Design rules

These are the invariants every part of the protocol obeys. A frontend author
who internalizes this section can predict the rest of the document.

1. **The daemon owns all state.** A frontend holds no durable state and does
   no merging, sorting, deduplication, or cache invalidation. The only state a
   frontend keeps is presentation state (scroll position, which window is
   open, drafts if it wants them).
2. **Everything you render arrives through a view.** Commands change state;
   their effects are observable *only* through view updates. Command
   responses carry ids and errors for correlation, never data to render.
3. **Every view item carries a daemon-computed sort key.** Frontends never
   implement ordering. The universal client algorithm for any view is:
   *keep a map of items by `id`, ordered by `sort`, apply upserts and
   removes, render.*
4. **Media never crosses the wire.** The protocol traffics in file paths into
   the daemon's cache. Frontends read files; frontends hand the daemon paths
   to send.
5. **Every message is renderable by every frontend.** Messages of kinds a
   frontend does not implement still carry a human-readable `fallback`
   string. Partial frontends are first-class citizens.
6. **The wire is human-usable.** Newline-delimited JSON over a Unix socket.
   You can develop and debug a frontend with `socat` and your fingers.
7. **No view or command is tailored to a specific frontend.** Frontends pick
   the subset they need; the daemon never asks who is calling to decide what
   something means.

## Transport and framing

- **Socket:** `$XDG_RUNTIME_DIR/whatevr/whatevrd.sock`, Unix `SOCK_STREAM`.
  Access control is filesystem permissions (`0700` directory). systemd socket
  activation is supported and recommended.
- **Framing:** UTF-8, one JSON object per line (`\n` terminated). JSON string
  escaping guarantees no literal newlines inside an object.
- **Concurrency:** any number of simultaneous connections. Each connection is
  an independent session with its own subscriptions.

There are exactly three shapes on the wire:

```jsonc
// Request (frontend → daemon). id is any client-chosen number or string.
{"id": 7, "method": "subscribe", "params": {"view": "chats", "limit": 50}}

// Response (daemon → frontend), exactly one per request, in any order
// relative to other requests but after any events the request caused... see
// per-method notes. Either result or error, never both.
{"id": 7, "result": {"sub": 2}}
{"id": 7, "error": {"code": "not_found", "message": "no chat 12345@g.us"}}

// Event (daemon → frontend). `sub` routes it to a subscription; a few
// connection-directed events (enumerated below) have no `sub`.
{"sub": 2, "event": "upsert", "sort": "00000000000012993421", "item": {"...": "..."}}
```

### Errors

`code` is a stable machine-readable string; `message` is for humans. Core
codes: `invalid_request`, `unknown_method`, `invalid_params`, `not_found`,
`not_logged_in`, `not_connected`, `already_exists`, `expired` (e.g. edit or
revoke window passed), `rejected` (WhatsApp refused), `io` (file/media
problem), `internal`. Methods may document additional codes.

### Hello

The first request on a connection must be `hello`:

```jsonc
{"id": 1, "method": "hello", "params": {"client": "whatkevr", "protocol": 1}}
{"id": 1, "result": {"daemon": "whatevrd", "version": "0.7.0", "protocol": 1,
                     "state": "online", "data_dir": "...", "cache_dir": "..."}}
```

The daemon rejects the connection if it cannot speak the requested
`protocol`. There is exactly one protocol version field and it is an integer.
Within a protocol version, additions (new views, new commands, new fields)
are always allowed; frontends must ignore unknown fields and unknown message
kinds (rule 5 makes the latter safe).

## The view model

A **view** is a live, daemon-maintained collection (or single object) that a
frontend subscribes to. The daemon sends the current contents, then keeps the
frontend's copy correct forever with keyed updates. All ordering, merging,
windowing, and invalidation happen in the daemon.

### Verbs

The entire live surface uses three methods and four events.

**Methods**

| method | params | result |
| --- | --- | --- |
| `subscribe` | `view`, view-specific params, optional `limit` | `{sub, ...view-specific meta}` |
| `extend` | `sub`, `count` | `{}` (completion signaled by `ready`) |
| `unsubscribe` | `sub` | `{}` |

**Events** (all carry `sub`)

| event | payload | meaning |
| --- | --- | --- |
| `upsert` | `sort`, `item` | insert or replace the item with this `item.id`, positioned by `sort` |
| `remove` | `id` | delete the item with this id |
| `ready` | optional `exhausted` | the window is fully populated for the latest subscribe/extend; `exhausted: true` means there is nothing further to extend into locally |
| `reset` | — | discard the local copy; fresh upserts follow, then `ready` |

Order after `subscribe`: response first (delivering `sub`), then upserts, then
`ready`. Same for `extend`. Live updates flow from the moment of subscription;
there is no snapshot/subscribe race by construction.

`sort` is an opaque string; order items by bytewise comparison, ascending.
The daemon computes it (for chats: pinned section then recency; for messages:
timestamp with arrival-order tiebreaker). A changed `sort` in an upsert means
the item moved. Frontends never look inside it.

`reset` is rare (e.g. a history-sync rewrite of a chat, or the daemon
recovering a slow consumer — see below). Frontends implement it once, in the
same generic view code, and never think about it again.

### Granularity

An upsert always carries the whole item. When that starts to feel wasteful —
the item is big, or it mixes slow-changing facts with fast-changing ones
(persistent chat data vs. who-is-typing-right-now) — the answer is never a
richer grammar (no patch events, no field masks): it is a finer-grained
view. Splitting keeps clients dumb, and it composes with pick-and-choose:
frontends subscribe to exactly the granularity they render.
`group`/`group_members` and `chats`/`typing` below are the canonical
examples.

### Windows

Collection views accept `limit`: the window is the first `limit` items in
view order, and the daemon keeps the client copy exactly equal to that
window (items pushed out of the window are `remove`d). `extend` grows the
window. Message views are anchored at the live edge: new messages always
arrive regardless of window size; `extend` reaches older into the local
store. Fetching history *from the phone* is a separate explicit command
(`chat.request_older`), because it has network cost and its results land like
any other message upserts.

Multiple subscriptions to the same view with different params are normal
(e.g. the chat list page and the archived page are two `chats`
subscriptions).

### Slow consumers

Because every update is a keyed upsert, the daemon may coalesce updates for a
lagging connection (only the latest version of an item matters) and, in the
extreme, drop the buffer entirely and send `reset`. Correctness never depends
on a frontend seeing every intermediate state.

### A useful consequence: the daemon knows what is visible

Subscriptions tell the daemon exactly what every frontend is currently
displaying. Demand-driven work — avatar fetching, presence subscription
upstream to WhatsApp, notification suppression for the visible chat — keys
off subscriptions automatically. (The old API needed `RequestAvatars`,
`SubscribeChatPresence`, and `UpdateSessionState` for this; they are gone.)

## View inventory

Object views deliver a single item (id `"self"` or similar) via the same
upsert grammar. Field sets carry over from the old proto messages unless
noted; this inventory fixes the shape of the protocol, not every field name.

| view | params | items | notes |
| --- | --- | --- | --- |
| `connection` | — | object | daemon/WhatsApp state (`starting`, `need_login`, `connecting`, `online`, `reconnecting`, `offline`), retry info, pending outgoing count |
| `login` | — | object | subscribing starts/attaches to the QR pairing flow when logged out; item carries `state` and current `qr` (`code`, `expires_at`). Phone-number pairing later adds a field, not a new mechanism |
| `sync` | — | object | history sync progress: type, phase, percent, counts; `stalled` phase included |
| `chats` | `filter` (`all`\|`direct`\|`groups`), `archived` (bool), `limit` | chat rows | full row per old `Chat` incl. preview, unread, mute/pin/archive, `history_exhausted`; typing indicators live in the `typing` view |
| `messages` | `chat_id`, `limit`, `anchor` (`latest` \| `unread` \| `{message_id}`) | message rows | subscribe meta returns `anchor_id` when anchored at unread; `remove` on delete-for-me; revocation is an upsert with `revoked: true` |
| `typing` | — | one item per chat with anyone composing | id is the `chat_id`; `senders` (jid + display name — frontends compose the localized label); `remove` when the last sender stops. Global, unwindowed, and tiny: chat lists and conversation headers both read it, everyone else skips it |
| `presence` | `chat_id` | one item per participant | `availability`, `last_seen`. Subscribing is what triggers the upstream WhatsApp presence subscription for that chat — availability is only delivered on request, whereas typing arrives unsolicited, which is why `typing` and `presence` are separate views |
| `receipts` | `message_id` | one item per participant | per-member delivered/read/played times, updating live while the info dialog is open |
| `self` | — | object | own profile: jid, phone, push name, about, avatar path |
| `contact` | `jid` | object | contact card; local data upserted immediately, network-fetched fields (about, business) upserted when they land — the old two-phase hack is just how views work |
| `group` | `chat_id` | object | subject, description, avatar, created, owner, `member_count`, `my_role`, announce/locked flags (feeds composer lockout in admins-only groups); same two-phase behavior. No member array — the chat header and card chrome need only this |
| `group_members` | `chat_id` | one item per member | jid, display name, phone, avatar path, role; joins/leaves/promotions are single upserts/removes. The info dialog subscribes to `group` + `group_members`; member search is presentation-side filtering over rows it already has |
| `privacy` | — | object | all privacy category values |
| `preferences` | — | object | daemon-persisted app preferences (notification gates, auto-download) |
| `blocklist` | — | blocked contacts | |
| `starred` | optional `chat_id`, `limit` | message rows + `chat_name` | windowed; syncs with stars made on other devices |
| `pinned` | `chat_id` | message rows | currently-pinned, unexpired; expiry produces `remove` |
| `stickers` | `source` (`recent`\|`favorite`\|`all`), `limit` | stickers | |
| `sticker_packs` | — | packs | |
| `sticker_pack` | `pack_id` | stickers | contents fetch is async; items land as they resolve |
| `transfers` | — | active media transfers | `message_id`, direction, `received_bytes`, `total_bytes`, `error`; `remove` on completion — completion itself is visible as the message row upserting with its new `media_path` |
| `notifications` | — | notification records | what the daemon would notify about, for applets, relays, and headless setups; daemon's own D-Bus notifier is unaffected |

Avatar paths are embedded in chat/message/contact/member rows and refresh via
ordinary upserts; visibility-driven fetching is automatic (see above).

## Command inventory

Commands are plain requests. Acks are minimal; ids in results exist for
correlation (e.g. to scroll to your own just-sent message when it upserts).

**Session & account**

| method | params | result |
| --- | --- | --- |
| `session.update` | `focused` (bool), `active_chat_id` | `{}` — feeds notification suppression and `open_chat` routing |
| `daemon.reconnect` | — | `{}` |
| `account.logout` | — | `{}` |

**Chats**

| method | params | result |
| --- | --- | --- |
| `chat.mark_read` | `chat_id` | `{}` |
| `chat.pin` | `chat_id`, `pinned` | `{}` |
| `chat.archive` | `chat_id`, `archived` | `{}` |
| `chat.mute` | `chat_id`, `muted`, `duration_secs` (0 = forever) | `{}` |
| `chat.typing` | `chat_id`, `composing` | `{}` |
| `chat.request_older` | `chat_id` | `{requested}` — asks the phone; results land as message upserts, exhaustion flips the chat row flag |
| `chat.ensure_direct` | `jid` | `{chat_id}` — row appears in `chats` views |

**Messages**

| method | params | result |
| --- | --- | --- |
| `send.text` | `chat_id`, `text`, `reply_to`, `mentions` (jids) | `{message_id}` |
| `send.media` | `chat_id`, `path`, `caption`, `reply_to`, `mentions` | `{message_id}` — daemon copies the file into its cache immediately; the caller may delete its copy on return |
| `send.sticker` | `chat_id`, `cache_key`, `reply_to` | `{message_id}` |
| `message.react` | `message_id`, `emoji` ("" removes) | `{}` |
| `message.edit` | `message_id`, `text` | `{}` — may fail `expired` |
| `message.revoke` | `message_id` | `{}` — may fail `expired` |
| `message.delete` | `message_id` | `{}` — local delete-for-me |
| `message.star` | `message_id`, `starred` | `{}` |
| `message.pin` | `message_id`, `pinned`, `duration_secs` | `{}` |
| `message.forward` | `message_id`, `chat_ids` | `{message_ids}` |
| `media.download` | `message_id` | `{}` — progress in `transfers`, path lands via message upsert |
| `media.fetch_profile_picture` | `jid` | `{path}` — full resolution, for the avatar viewer |

**Settings, contacts, stickers**

| method | params | result |
| --- | --- | --- |
| `privacy.set` | `category`, `value` | `{}` |
| `preferences.set` | partial preferences object | `{}` |
| `self.set_about` | `text` | `{}` (later: `self.set_name`, `self.set_avatar`) |
| `contact.block` | `jid`, `blocked` | `{}` |
| `sticker.favorite` | `cache_key` or `message_id`, `favorite` | `{}` |
| `sticker.download` | `cache_key` | `{}` — lands via `stickers`/`sticker_pack` upsert |
| `sticker_pack.install` | `pack_id`, `installed` | `{}` |

## Queries

One-shot request/response for data that is legitimately transient (search
results a frontend renders and throws away — frontend-only state is allowed
for these).

| method | params | result |
| --- | --- | --- |
| `search.chats` | `query`, `limit` | chat rows |
| `search.messages` | `query`, optional `chat_id`, `limit`, `before_message_id` cursor | message rows + `chat_name`, `has_more` |
| `contacts.check_phone` | `phone` | `registered`, `jid`, `display_name`, `is_business`, normalized `phone` |

## Messages on the wire

A message item has a `kind` (`text`, `image`, `sticker`, and over time
`video`, `voice`, `document`, `location`, `poll`, …), kind-specific fields,
and always:

- `fallback` — a human-readable one-line rendering ("🎤 Voice message (0:12)",
  "📊 Poll: dinner?"). A frontend renders `fallback` for any `kind` it does
  not implement. New kinds are therefore never a breaking change.
- `sort` (on the envelope), `id`, `chat_id`, `sender` (id, name, avatar path),
  `timestamp`, `direction`, `status` (`pending`→`sent`→`delivered`→`read`, or
  `failed`), and the interaction state that applies to any kind: `reply_to`
  quote, `reactions`, `mentions`, `edited`, `revoked`, `starred`, `pinned_until`.

Media-bearing kinds carry `media` (`mime`, dimensions, `thumbnail_path`,
`path` — empty until downloaded, byte size) and the download lifecycle is:
`media.download` → progress in `transfers` → message upserts with `path` set.

## Connection-directed events

Events without `sub`, sent to specific connections. Currently exactly one:

| event | data | meaning |
| --- | --- | --- |
| `open_chat` | `chat_id` | the user asked the system to surface a chat (notification click, `whatevr://chat/…` URL); sent to the focused frontend, which should raise its window and open the chat |

## Example session

A complete frontend, by hand:

```console
$ socat - UNIX-CONNECT:"$XDG_RUNTIME_DIR/whatevr/whatevrd.sock"
{"id":1,"method":"hello","params":{"client":"human","protocol":1}}
{"id":1,"result":{"daemon":"whatevrd","version":"0.7.0","protocol":1,"state":"online"}}
{"id":2,"method":"subscribe","params":{"view":"chats","limit":2}}
{"id":2,"result":{"sub":1}}
{"sub":1,"event":"upsert","sort":"0-000000000018832100","item":{"id":"12036...@g.us","name":"family","unread":3,"preview":"Mom: photo","kind_hint":"image"}}
{"sub":1,"event":"upsert","sort":"1-000000000018831970","item":{"id":"91887...@s.whatsapp.net","name":"Aditi","unread":0,"preview":"you: see you then"}}
{"sub":1,"event":"ready"}
{"id":3,"method":"send.text","params":{"chat_id":"91887...@s.whatsapp.net","text":"oi"}}
{"id":3,"result":{"message_id":"3EB0..."}}
{"sub":1,"event":"upsert","sort":"1-000000000018832544","item":{"id":"91887...@s.whatsapp.net","name":"Aditi","unread":0,"preview":"you: oi","status":"sent"}}
```

The acceptance bar for this protocol, kept under `examples/`: **a working
frontend in ~30 lines of shell with `socat` and `jq`** — subscribe to the
chat list, print incoming messages, send replies. If a change to the
protocol breaks the spirit of that script, the change is wrong.

## Resolved decisions

- **Envelope:** bespoke minimal, decided. No `"jsonrpc":"2.0"` field, string
  error codes. A conforming client is ~50 lines in any language; library
  compatibility is not worth the ceremony.
- **Item granularity:** whole-item upserts stay; oversized or mixed-lifetime
  items are split into finer views (see *Granularity*). Applied to
  `group`/`group_members` and `chats`/`typing`.

## Open questions

- **`notifications` view shape** (actions? dismissal sync?) — sketch when the
  first non-D-Bus consumer (relay/applet) is real.
- **Multi-account:** out of scope for protocol 1; the likely shape is an
  `account` param on `hello` or per-account sockets. Nothing in this design
  precludes it.
