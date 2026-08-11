# The Whatevr Protocol

**Status: stable. Protocol version 1, implemented and served by `whatevrd`.
No backwards compatibility with the retired gRPC API.**

Version 1 is frozen: within it, additions (new views, new commands, new fields,
new message kinds) are always allowed and never break a conforming frontend,
but nothing already specified here changes shape. A change that would break a
frontend is protocol 2, negotiated through `hello`.

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
{"id": 1, "result": {"daemon": "whatevrd", "version": "0.6.0", "protocol": 1,
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
| `extend` | `sub`, `count`, `direction` (`older`\|`newer`) | `{}` (completion signaled by `ready`) |
| `unsubscribe` | `sub` | `{}` |

**Events** (all carry `sub`)

| event | payload | meaning |
| --- | --- | --- |
| `upsert` | `sort`, `item` | insert or replace the item with this `item.id`, positioned by `sort` |
| `remove` | `id` | delete the item with this id |
| `ready` | optional `exhausted` | the window is fully populated for the latest subscribe/extend; `exhausted: true` means there is nothing further to extend into locally (for the frontier just extended, see *Windows*) |
| `reset` | none | discard the local copy; fresh upserts follow, then `ready` |

Order after `subscribe`: response first (delivering `sub`), then upserts, then
`ready`. Same for `extend`. Live updates flow from the moment of subscription;
there is no snapshot/subscribe race by construction.

`sort` is an opaque string; order items by bytewise comparison, ascending.
The daemon computes it (for chats: pinned section then recency; for messages:
timestamp with arrival-order tiebreaker). A changed `sort` in an upsert means
the item moved. Frontends never look inside it.

`reset` is rare (e.g. a history-sync rewrite of a chat, or the daemon
recovering a slow consumer, see below). Frontends implement it once, in the
same generic view code, and never think about it again.

### Granularity

An upsert always carries the whole item. When that starts to feel wasteful,
because the item is big or it mixes slow-changing facts with fast-changing
ones (persistent chat data vs. who-is-typing-right-now), the answer is never a
richer grammar (no patch events, no field masks): it is a finer-grained
view. Splitting keeps clients dumb, and it composes with pick-and-choose:
frontends subscribe to exactly the granularity they render.
`group`/`group_members` and `chats`/`typing` below are the canonical
examples.

### Windows

Collection views accept `limit`: the window is the first `limit` items in
view order, and the daemon keeps the client copy exactly equal to that
window (items pushed out of the window are `remove`d). `extend` grows the
window, and every `extend` carries a `direction`.

Every collection view, and a `messages` view with the `latest` anchor, is a
**live-edge** window: it sits at the newest / highest-priority end, new items
arrive there unsolicited regardless of window size, and `extend` with
`direction: "older"` reaches back away from that edge into the local store.
`direction: "newer"` is meaningless for a live-edge window, whose newer edge
*is* the live edge, so the daemon rejects it (`invalid_params`).

The `unread` and `{message_id}` message anchors instead pin a **bounded window
around a mid-history anchor**, and its two frontiers grow independently:
`extend` with `direction: "older"` reaches up-history, `"newer"` toward the
present. Messages outside the window do not intrude: a new message far past
the anchor is *not* delivered into this window (it would leave a render gap);
the frontend learns of it through the `chats` view (its row's updated
preview/unread) and *follows* the live edge by subscribing `latest`. `ready`'s
`exhausted` reports the frontier just extended.

Once the newer frontier has been extended all the way to the live edge, though,
it stays adjacent to it: subsequent messages arriving at the present *are*
contiguous with the window, so they are delivered as ordinary upserts (there is
no gap to leave) even though a prior `ready` reported the newer side exhausted.
A window that has reached the live edge this way therefore keeps growing at the
present; to freeze it instead, re-subscribe at a fixed `{message_id}` anchor.

Fetching history *from the phone* is a separate explicit command
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
displaying. Demand-driven work (avatar fetching, presence subscription
upstream to WhatsApp, notification suppression for the visible chat) keys
off subscriptions automatically. (The old API needed `RequestAvatars`,
`SubscribeChatPresence`, and `UpdateSessionState` for this; they are gone.)

## View inventory

Object views deliver a single item (id `"self"` or similar) via the same
upsert grammar. Field sets carry over from the old proto messages unless
noted; this inventory fixes the shape of the protocol, not every field name.

| view | params | items | notes |
| --- | --- | --- | --- |
| `connection` | none | object | daemon/WhatsApp state (`starting`, `need_login`, `connecting`, `online`, `reconnecting`, `offline`), retry info, pending outgoing count |
| `login` | none | object | subscribing starts/attaches to the QR pairing flow when logged out; item carries `state` and current `qr` (`code`, `expires_at`). Phone-number pairing later adds a field, not a new mechanism |
| `sync` | none | object | history sync progress: type, phase, percent, counts; `stalled` phase included |
| `chats` | `filter` (`all`\|`direct`\|`groups`), `archived` (bool), `limit` | chat rows | full row per old `Chat` incl. preview, unread, mute/pin/archive, `history_exhausted`; typing indicators live in the `typing` view |
| `chat` | `chat_id` | object | the same live chat row emitted by `chats`, independent of chat-list filters and windows |
| `messages` | `chat_id`, `limit`, `anchor` (`latest` \| `unread` \| `{message_id}`) | message rows | subscribe meta returns `anchor_id` when anchored at unread; `remove` on delete-for-me; revocation is an upsert with `revoked: true` |
| `typing` | none | one item per chat with anyone composing | id is the `chat_id`; `senders` (jid + display name; frontends compose the localized label); `remove` when the last sender stops. Global, unwindowed, and tiny: chat lists and conversation headers both read it, everyone else skips it |
| `presence` | `chat_id` | one item per participant | `availability`, `last_seen`. Subscribing is what triggers the upstream WhatsApp presence subscription for that chat: availability is only delivered on request, whereas typing arrives unsolicited, which is why `typing` and `presence` are separate views |
| `receipts` | `message_id` | one item per participant | per-member delivered/read/played times, updating live while the info dialog is open |
| `self` | none | object | own profile: jid, phone, push name, about, avatar path |
| `contact` | `jid` | object | contact card; local data (including the `is_business` flag) upserted immediately, the network-fetched `about` upserted when it lands; the old two-phase hack is just how views work |
| `group` | `chat_id` | object | subject, description, avatar, created, owner, `member_count`, `my_role`, announce/locked flags (feeds composer lockout in admins-only groups); same two-phase behavior. No member array; the chat header and card chrome need only this |
| `group_members` | `chat_id` | one item per member | jid, display name, phone, avatar path, role; joins/leaves/promotions are single upserts/removes. The info dialog subscribes to `group` + `group_members`; member search is presentation-side filtering over rows it already has |
| `privacy` | none | object | all privacy category values |
| `preferences` | none | object | daemon-persisted app preferences (notification gates, auto-download) |
| `blocklist` | none | blocked contacts | |
| `starred` | optional `chat_id`, `limit` | message rows + `chat_name` | windowed; syncs with stars made on other devices. Ordered by the message's own timestamp (newest first), not by when it was starred (the store records no star time), so starring an old message places it deep in the window rather than at the top |
| `pinned` | `chat_id` | message rows | currently-pinned, unexpired; expiry produces `remove` |
| `stickers` | `source` (`recent`\|`favorite`\|`all`), `limit` | stickers | |
| `sticker_packs` | none | packs | |
| `sticker_pack` | `pack_id` | stickers | contents fetch is async; items land as they resolve |
| `transfers` | none | active media transfers | `message_id`, `direction`, `received_bytes`, `total_bytes`, optional active `error`; `remove` on terminal success or failure: success itself is visible as the message row upserting with its new `media.path`, failure as the message row upserting with `media.download_error`. `direction` is `"download"` today; outbound uploads are not yet modelled here (a known gap) |
| `notifications` | none | notification records | **Reserved, not served in protocol 1**: subscribing errors `not_found`. What the daemon would notify about, for applets, relays, and headless setups; the daemon's own D-Bus notifier is unaffected. Its shape waits on a real consumer (see *Open questions*) |

Avatar paths are embedded in chat/message/contact/member rows and refresh via
ordinary upserts; visibility-driven fetching is automatic (see above).

## Command inventory

Commands are plain requests. Acks are minimal; ids in results exist for
correlation (e.g. to scroll to your own just-sent message when it upserts).

**Session & account**

| method | params | result |
| --- | --- | --- |
| `session.update` | `focused` (bool), `active_chat_id` | `{}`: feeds notification suppression and `open_chat` routing |
| `daemon.reconnect` | none | `{}` |
| `account.logout` | none | `{}` |

**Chats**

| method | params | result |
| --- | --- | --- |
| `chat.mark_read` | `chat_id`, `up_to_message_id` | `{}` |
| `chat.pin` | `chat_id`, `pinned` | `{}` |
| `chat.archive` | `chat_id`, `archived` | `{}` |
| `chat.mute` | `chat_id`, `muted`, `duration_secs` (0 = forever) | `{}` |
| `chat.typing` | `chat_id`, `composing` | `{}` |
| `chat.request_older` | `chat_id` | `{requested}`: asks the phone; results land as message upserts, exhaustion flips the chat row flag |
| `chat.ensure_direct` | `jid` | `{chat_id}`: row appears in `chats` views |

`chat.mark_read` marks messages read through the frontend's visible horizon;
`up_to_message_id` is the newest message the user has actually seen. The daemon
recomputes unread counts and sends WhatsApp read receipts; updates land through
views.

**Messages**

| method | params | result |
| --- | --- | --- |
| `send.text` | `chat_id`, `text`, `reply_to`, `mentions` (jids) | `{message_id}` |
| `send.media` | `chat_id`, `path`, `caption`, `reply_to`, `mentions` | `{message_id}`: daemon copies the file into its cache immediately; the caller may delete its copy on return |
| `send.sticker` | `chat_id`, `cache_key`, `reply_to` | `{message_id}` |
| `message.react` | `message_id`, `emoji` ("" removes) | `{}` |
| `message.edit` | `message_id`, `text` | `{}`: may fail `expired` |
| `message.revoke` | `message_id` | `{}`: may fail `expired` |
| `message.delete` | `message_id` | `{}`: local delete-for-me |
| `message.star` | `message_id`, `starred` | `{}` |
| `message.pin` | `message_id`, `pinned`, `duration_secs` | `{}` |
| `message.forward` | `message_id`, `chat_ids` | `{message_ids}` |
| `media.download` | `message_id` | `{}`: progress in `transfers`, path lands via message upsert |
| `media.fetch_profile_picture` | `jid` | `{path}`: full resolution, for the avatar viewer |

**Settings, contacts, stickers**

| method | params | result |
| --- | --- | --- |
| `privacy.set` | `category`, `value` | `{}` |
| `preferences.set` | partial preferences object | `{}` |
| `self.set_about` | `text` | `{}` (later: `self.set_name`, `self.set_avatar`) |
| `contact.block` | `jid`, `blocked` | `{}` |
| `sticker.favorite` | `cache_key` or `message_id`, `favorite` | `{}` |
| `sticker.download` | `cache_key` | `{}`: lands via `stickers`/`sticker_pack` upsert |
| `sticker_pack.install` | `pack_id`, `installed` | `{}` |
| `sticker_packs.refresh` | none | `{}`: forces a store refresh; results land via `sticker_packs` upserts/removes |

## Queries

One-shot request/response for data that is legitimately transient (search
results a frontend renders and throws away; frontend-only state is allowed
for these).

Unlike view events (which stream item-at-a-time upserts), a query returns its
whole result inside the one response `result` object, under a named key:

| method | params | result |
| --- | --- | --- |
| `search.chats` | `query`, `limit` | `{chats: [chat row, …]}` |
| `search.messages` | `query`, optional `chat_id`, `limit`, `before_message_id` cursor | `{messages: […], has_more}`: each message row carries `chat_name`; `has_more` drives the `before_message_id` keyset cursor |
| `search.stickers` | `query`, `limit` | `{stickers: […]}`: daemon-ordered sticker rows |
| `contacts.check_phone` | `phone` | `{registered, jid, display_name, is_business, phone}` (normalized `phone`) |

## Messages on the wire

A message item has a `kind` (`text`, `image`, `sticker`, and over time
`video`, `voice`, `document`, `location`, `poll`, …), kind-specific fields,
and always:

- `fallback`: a human-readable one-line rendering ("🎤 Voice message (0:12)",
  "📊 Poll: dinner?"). A frontend renders `fallback` for any `kind` it does
  not implement. New kinds are therefore never a breaking change.
- `sort` (on the envelope), `id`, `chat_id`, `sender` (id, name, avatar path),
  `timestamp`, `direction`, `status` (`pending`→`sent`→`delivered`→`read`, or
  `failed`), and the interaction state that applies to any kind: `reply_to`
  quote, `reactions`, `mentions`, `edited`, `revoked`, `starred`, `pinned_until`.

Media-bearing kinds carry `media` (`mime`, dimensions, `thumbnail_path`,
`path` which is empty until downloaded, optional `download_error`) and the
download lifecycle is: `media.download` → active progress in `transfers` →
`transfers` remove when the attempt ends → message upsert with `path` set on
success or `download_error` set on failure. A retry clears `download_error` via
another message upsert.

## Connection-directed events

Events without `sub`, sent to specific connections. Currently exactly one:

| event | data | meaning |
| --- | --- | --- |
| `open_chat` | `chat_id` | the user asked the system to surface a chat (notification click, `whatevr://chat/…` URL); sent to the most recently focused frontend (per `session.update`), which should raise its window and open the chat. If no frontend is currently focused, it falls back to the most recently active session, so a notification click raises an existing but unfocused window instead of cold-starting a duplicate |

## Example session

A complete frontend, by hand. Rows are abridged (a real chat row carries more
fields) and ids shortened; everything else is what the daemon sends:

```console
$ socat - UNIX-CONNECT:"$XDG_RUNTIME_DIR/whatevr/whatevrd.sock"
{"id":1,"method":"hello","params":{"client":"human","protocol":1}}
{"id":1,"result":{"daemon":"whatevrd","version":"0.6.0","protocol":1,"state":"online","data_dir":"…","cache_dir":"…"}}
{"id":2,"method":"subscribe","params":{"view":"chats","limit":2}}
{"id":2,"result":{"sub":1}}
{"sub":1,"event":"upsert","sort":"0-00000000004294967295-04611686016657387904-12036…@g.us","item":{"id":"12036…@g.us","name":"family","is_group":true,"unread":3,"preview":"📷 Photo","pinned":true}}
{"sub":1,"event":"upsert","sort":"1-04611686016657387774-91887…@s.whatsapp.net","item":{"id":"91887…@s.whatsapp.net","name":"Aditi","is_group":false,"unread":0,"preview":"see you then","last_message_direction":"outgoing","last_message_status":"read"}}
{"sub":1,"event":"ready"}
{"id":3,"method":"send.text","params":{"chat_id":"91887…@s.whatsapp.net","text":"oi"}}
{"id":3,"result":{"message_id":"3EB0…"}}
{"sub":1,"event":"upsert","sort":"1-04611686016657387360-91887…@s.whatsapp.net","item":{"id":"91887…@s.whatsapp.net","name":"Aditi","is_group":false,"unread":0,"preview":"oi","last_message_direction":"outgoing","last_message_status":"pending"}}
```

Note what the last line is *not*: the send's response carried only an id. The
row you render arrived through the view you were already subscribed to, and the
`sort` key moved the chat up the list for you.

The acceptance bar for this protocol, kept under `examples/`: **a working
frontend in ~30 lines of shell with `socat` and `jq`**: subscribe to the
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

- **`notifications` view shape** (actions? dismissal sync?): sketch when the
  first non-D-Bus consumer (relay/applet) is real.
- **Multi-account:** out of scope for protocol 1; the likely shape is an
  `account` param on `hello` or per-account sockets. Nothing in this design
  precludes it.
