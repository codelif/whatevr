# The Whatevr Protocol

**Status: draft. Protocol version 2. Protocol 1 (NDJSON) is retired and not
served; it lives in git history.**

This document is the contract between `whatevrd` and every frontend. It is the
source of truth: the daemon implements this document and the schema beside
it, not the other way around. If the daemon and this document disagree, one
of them has a bug and it is probably the daemon.

The schema is `proto/whatevr/v2/*.proto`. This document says what things mean
and how they behave; the schema says what they are called and how they are
shaped. Where this document names a method or a field, it uses the schema's
name.

## Design rules

These are the invariants every part of the protocol obeys. A frontend author
who takes in this section can predict the rest of the document.

1. **The daemon owns all state.** A frontend holds no durable state and does
   no merging, sorting, deduplication, or cache invalidation. The only state a
   frontend keeps is presentation state (scroll position, which window is
   open, drafts if it wants them).
2. **Everything you render arrives through a view.** Commands change state;
   their effects are seen *only* through view updates. Command results carry
   ids and errors for correlation, never rows to render. Queries (search) are
   the one exception, and they are named as such.
3. **Every view item carries a daemon-computed sort key.** Frontends never
   implement ordering. The client algorithm for any view is: *keep a map of
   items by id, ordered by sort, apply each update, render.*
4. **Files are paths.** Rows carry paths into the daemon's cache; frontends
   hand the daemon paths to send. Two ways around a path exist for a frontend
   that can't use one: `media_read` copies a file's bytes over the socket, and
   `media_stream` serves a file still downloading over a loopback http url.
5. **Every message is renderable by every frontend.** Messages of kinds a
   frontend does not implement still carry a one-line `fallback`. Partial
   frontends are first-class citizens.
6. **The API is obvious.** A method or field does what its name says, one way
   to do a thing, no hidden modes. The wire is binary and not meant to be
   read by eye; the schema is.
7. **No view or command is tailored to a specific frontend.** Frontends pick
   the subset they need; the daemon never asks who is calling to decide what
   something means.

## Transport

- **Socket:** a local stream socket, one per user:

  | OS | address |
  | --- | --- |
  | Linux | `$XDG_RUNTIME_DIR/whatevr/whatevrd.sock`, unix socket, directory `0700` |
  | macOS | `$TMPDIR/in.codelif.whatevr.sock`, unix socket, `$TMPDIR` is per-user `0700` |
  | Windows | `\\.\pipe\whatevr-<user SID>`, named pipe, DACL for the user only |

  `WHATEVR_SOCKET` overrides the address on every OS. Access control is the
  OS's: whoever can open the socket is the user. Socket activation is
  supported on Linux (systemd) and macOS (launchd).
- **Framing:** every frame is one protobuf `Frame` message, preceded by its
  length as a varint. This is protobuf's own size-delimited format: Go's
  `protodelim`, protobuf-es `sizeDelimitedEncode`/`sizeDelimitedDecodeStream`,
  Java's `writeDelimitedTo`/`parseDelimitedFrom`, and about five lines anywhere
  else. A frame is at most 16 MiB; the daemon closes a connection that sends
  a bigger one.
- **Concurrency:** any number of connections at once. Each is its own session
  with its own subscriptions.

## Schema

- Edition 2024, package `whatevr.v2`, in `proto/`, which is also a Go module
  (`github.com/codelif/whatevr/proto`) holding the generated Go code. Other
  languages generate from the same files with buf or protoc.
- Scalar fields have implicit presence: `0`, `""` and `false` mean absent, and
  absent fields cost nothing on the wire. The few fields where `0` is a real
  value say so with explicit presence in the schema (`LiveLocation.heading_deg`,
  every field of `PreferencesSet`).
- Closed sets are enums. Every enum's `0` is `..._UNSPECIFIED` and never sent
  on purpose. Enums are open: a value a frontend does not know arrives as its
  number, and the frontend treats it as it would `UNSPECIFIED`.
- Times are `int64` unix milliseconds, in fields named `..._ms`. Durations are
  milliseconds too, also `..._ms`, except the few whatsapp gives in whole
  seconds and nothing else (`ephemeral_secs`).
- Within protocol 2 the schema only grows: new fields, new oneof arms, new
  enum values, new messages. Frontends ignore what they do not know, which
  protobuf does by itself. `buf breaking` guards this from the first tag that
  ships protocol 2.

## Frames

A `Frame` is one of three things:

- `Request`, frontend to daemon: a `uint64` id the frontend chooses, and one
  arm of the `method` oneof, which names the method and carries its
  parameters.
- `Response`, daemon to frontend: the request's id, and one arm of the
  `result` oneof: `error`, `done` for a method with nothing to say, or the
  method's own result. A result arm has the same field number as its request
  arm.
- `Event`, daemon to frontend, unasked: a `ViewUpdate` for a subscription, or
  one of the connection events at the end of this document.

Every request gets exactly one response. Responses may come in any order
relative to other requests. A `subscribe` or `extend` response always comes
before the updates it causes.

A connection runs at most 32 requests at once; past that the daemon stops
reading it until one finishes, so a frontend that floods only waits. Closing
the connection cancels whatever it still has running. A send already
answered stays queued.

### Hello

The first request on a connection must be `hello`, with `protocol: 2`. The
daemon answers with its name, version, protocol and its **features**: the
names of every view and method it serves, spelled as their oneof arm names
(`chats`, `media_stream`, `notifications`, ...). A frontend that wants
something optional checks for its name instead of calling and waiting for
`ERROR_CODE_UNKNOWN_METHOD`. Within protocol 2, new views and methods only
ever add names. A daemon that cannot speak the requested protocol answers
`ERROR_CODE_INVALID_REQUEST` and closes.

A frontend also says which one it is in `frontend_id`, the id its manifest or
app entry gives (see Frontends). Tools that aren't a frontend leave it empty.

### Errors

An `Error` is a `code` from the `ErrorCode` enum and a `message` for humans
and logs, never parsed. The codes are in the schema with a line each. A
method notes the codes it can answer beyond the common ones
(`INVALID_PARAMS`, `NOT_FOUND`, `NOT_LOGGED_IN`, `NOT_CONNECTED`, `INTERNAL`).

## Identity

### People and chats

Every person and every chat has an **id**: a string the daemon gives it the
first time it meets one of its addresses, kept in the daemon's log, so it is
the same after a restart and after a rebuild. A frontend never looks inside
it.

A person can turn out to be two: whatsapp shows somebody by phone number and
later by lid, and the daemon learns that both are one person. Their rows then
fold into one, keeping the older id. The other row's `Remove` carries
`replaced_by`, the id it folded into, so a frontend moves whatever it held on
the old id (an open chat, a draft, a scroll position) to the new one. That is
the only time an id changes.

A direct chat's id is its person's id. A group's id is the group's own.

### Addresses

Commands that name a person take an `Address`: an `id` from a row, a `phone`
number, a `lid` or a `username`, whichever the frontend has. Commands that
name a chat take its id.

### Messages

Every message has an **id** (`MessageRow.id`): an opaque string built from
the chat address the message came on and whatsapp's own id. It does not
change when people fold together, and a send's result gives the final id up
front, before the message has even left. Every message command takes it.

## Views

A **view** is a live collection (or one object) that the daemon keeps
correct on the frontend's side. The daemon sends what is there, then keeps it
right with keyed updates. All ordering, merging, windowing and invalidation
happen in the daemon.

### Verbs

| method | params | result |
| --- | --- | --- |
| `subscribe` | one arm of `view` with its params; `limit` for a collection | `SubscribeResult`: `sub`, plus `anchor_id` for a messages view anchored at unread. `INVALID_PARAMS` for a `limit` over the view's cap, or past 64 subscriptions on the connection |
| `extend` | `sub`, `count`, `direction` | `done`; the window fills through updates, then `ready`. `INVALID_PARAMS` when the window would pass the view's cap |
| `unsubscribe` | `sub` | `done` |

Everything a subscription receives is a `ViewUpdate`: one daemon change to
that subscription, applied whole and then rendered. In order:

1. `reset`: if set, drop the local copy first.
2. `changes`: `Upsert`s (insert or replace the item with this `id`, placed by
   `sort`, carrying the whole item in one arm of `item`) and `Remove`s, in
   order.
3. `ready`: if present, the window is filled for the latest `subscribe` or
   `extend`. `ready.exhausted` says there is nothing further to extend into
   locally, on the frontier just extended.

A `subscribe` is answered first, then one or more updates fill the window, the
last carrying `ready`. Live updates flow from the moment of subscription; there
is no snapshot race by construction. A change that touches many items (a
history sync batch, a chat folding into another) is one update, so a frontend
never draws a half-applied state.

`sort` is opaque bytes; order items by comparing them bytewise, ascending.
For chats it is the pinned section, then recency; for messages the time, then
the id. A different `sort` on an upsert means the item moved.

`reset` is rare: a history rewrite of a chat, or the daemon recovering a slow
consumer. A reset update carries the fresh contents in the same frame, so it
never shows an empty list.

An update is always one frame. Caps make that hold: every item of a view
fits that view's item size, a window never holds more than fits 15 MiB of
them (see *Caps*), and a change bigger than a frame goes out as a reset of
the window instead.

### Granularity

An upsert always carries the whole item. When that is wasteful, because the
item is big or mixes slow facts with fast ones, the answer is a finer view,
never a patch grammar. `group`/`group_members`, `chats`/`typing` and
`messages`/`live_locations` are the examples.

### Windows

Collection views take `limit`: the window is the first `limit` items in view
order, and the daemon keeps the frontend's copy exactly equal to it (items
pushed out are removed). `extend` grows it in a `direction`.

Every collection view at its default anchor, and a messages view at
`latest`, is a **live-edge** window: it sits at the newest end, new items
arrive there unasked whatever the window size, and `extend` with
`DIRECTION_OLDER` reaches back into the local store. `DIRECTION_NEWER` on a live-edge window is `INVALID_PARAMS`.

A messages view anchored at `unread` or at a `message_id` is a **bounded
window** around that point, with two frontiers that grow on their own:
`DIRECTION_OLDER` reaches up the history, `DIRECTION_NEWER` toward the
present. A new message far past the window is not delivered into it (that
would leave a gap); the frontend learns of it from the `chats` view and
follows the live edge by subscribing at `latest`. Once the newer frontier has
been extended all the way to the present, it stays there: new messages are
next to it and arrive as ordinary upserts.

A `chats` view at `index` or `sort` is a bounded window over list
positions: it keeps its place while chats move through it,
`DIRECTION_NEWER` grows it up the list and `DIRECTION_OLDER` down.

A bounded window that reached the present keeps growing as messages arrive;
past its cap the oldest messages leave it, and the older frontier moves up
with them.

An anchor can also be a `sort`: a row's sort bytes as the frontend got them.
The window sits where that row is or was, whether or not the message still
exists. A window whose anchor message goes away while subscribed stays where
it was the same way.

### Positions

A window can say where it sits in its whole view. An update carries
`position` whenever that place changed: on the first update of a
subscription, and after anything that grows or shrinks the view or moves
the window.

- `count`: every item the view has now, in the window or not. For
  `messages` that is the chat's local store, not the phone's whole history.
- `offset`: how many of them sort before the window's first item.

A frontend draws its scrollbar over all of `count` with the window inside
it, and opens a window anywhere with the `index` anchor: around the item
that many from the start of view order, clamped to the view. An index is a
place as of now; if the view changes before the subscribe lands, the
window opens near it, and its own `position` says where it really is.

`chats` takes the same anchors as `messages` (`latest`, the default, then
`index` and `sort`), so a list longer than its cap pages the same way. A
daemon that serves all of this lists `positions` in its features.

### Caps

Every view has an item size, and its cap is how many such items fit 15 MiB,
rounded down to a power of two. A `limit` of 0 is the view's default, or all
of it up to the cap. Asking past the cap is `INVALID_PARAMS`; a frontend that
needs more pages with a fresh subscription.

| item size | cap | views |
| --- | --- | --- |
| 60 KiB | 256 | `messages`, `starred`, `pinned`, `chat_media` |
| 16 KiB | 512 | `typing` |
| 4 KiB | 2048 | `chats`, `chat`, `problems`, `live_locations`, `stickers`, `sticker_packs`, `sticker_pack`, `transfers`, `notifications` |
| 1 KiB | 8192 | `presence`, `receipts`, `group_members`, `blocklist`, `reactions`, `poll_votes`, `event_responses` |
| 64 KiB | 128 | the object views |

The queries hold `limit` to the cap of the rows they answer with:
`search_messages` to 256, `search_chats` and `search_stickers` to 2048.

An item that would pass its size is cut, never dropped. A message's `text`
goes first and the row says so with `text_truncated`; `message_text` gives
all of it. Past that the daemon cuts the longest words in the item, never an
id or a path. Lists that grow with a group are short in a row and whole in a
finer view, see *Messages*.

Asking the phone for older history is a separate command,
`chat_request_older`, because it costs network; its results land as upserts
like anything else. While a request is out, the chat row says
`loading_older`. It clears when the phone answers, when the request fails, or
after 90 seconds without an answer.

Several subscriptions to one view with different params are normal: the chat
list and the archived list are two `chats` subscriptions.

### Slow consumers

Every update is keyed, so for a lagging connection the daemon may merge
pending updates (only the latest version of an item matters) and, at worst,
drop them all and send a `reset` with the current contents. Correctness never
depends on seeing every state in between.

### The daemon knows what is shown

Subscriptions tell the daemon what every frontend shows, and demand-driven
work keys off them: avatar fetching, asking whatsapp for presence,
suppressing notifications for the open chat.

## View inventory

Object views send one item with an empty id.

| view | params | items | notes |
| --- | --- | --- | --- |
| `connection` | none | object | state, since, a finished `detail` sentence, the last attempt's `cause`, retry attempt and time, `can_reconnect`, sends waiting |
| `login` | none | object | subscribing starts the qr pairing when logged out, or joins it; `state`, the `qr` code and when it expires |
| `sync` | none | object | history sync type, phase (`STALLED` included), percent and counts |
| `problems` | none | one per live problem | `kind`, `since_ms`, `next_retry_ms` (0 for never), a finished `text`; most severe first. A healthy daemon has none |
| `chats` | `filter`, `archived`, `anchor` | chat rows | name, type, avatar, preview, unread and marked unread, pin, archive, mute, `history_exhausted`, `loading_older`, timer, `read_only` |
| `chat` | `chat_id` | object | the same row `chats` sends, outside any filter or window |
| `messages` | `chat_id`, `anchor` | message rows | see *Messages*; delete for me removes; a revoke is an upsert with `revoked` |
| `typing` | none | one per chat with anyone composing | id is the chat id; who, and whether they are recording; removed when the last one stops |
| `presence` | `chat_id` | one per person in the chat | availability and last seen. Subscribing is what asks whatsapp for it |
| `receipts` | `message_id` | one per recipient | delivered, read and played times, live while open |
| `self` | none | object | our own phone, push name, about, avatar |
| `contact` | `person` | object | a contact card; local facts first, `about` when fetched |
| `group` | `chat_id` | object | subject, description, avatar, created, owner, member count, my role, announce/locked/approval, community, `error` when whatsapp won't describe it |
| `group_members` | `chat_id` | one per member | person and role |
| `reactions` | `message_id` | one per person who reacted | id is the person; emoji and time, newest first |
| `poll_votes` | `message_id` | one per vote | id is the option index and the person; by option, newest first |
| `event_responses` | `message_id` | one per person who answered | id is the person; the answer, extra guests and time, newest first |
| `privacy` | none | object | every privacy setting |
| `preferences` | none | object | the daemon's own preferences |
| `blocklist` | none | one per blocked person | |
| `starred` | `chat_id` (empty for all) | message rows | `chat_name` set; ordered by the message's time |
| `pinned` | `chat_id` | message rows | pinned and unexpired; removed when a pin runs out |
| `live_locations` | `chat_id` | one per running share | id is the message that opened it; sender, started, expires, updated, the latest position. Removed when a share ends |
| `chat_media` | `chat_id` | message rows, media kinds | newest first |
| `stickers` | `source` | sticker rows | recent, favorite or all |
| `sticker_packs` | none | pack rows | |
| `sticker_pack` | `pack_id` | sticker rows | the fetch is async; items land as they resolve |
| `transfers` | none | one per running transfer | message, direction, done and total bytes, an error it is retrying through; removed when it ends. Only the counts live here: whether a fetch runs at all is `downloading` on the message's media, so a renderer never joins two views |
| `notifications` | none | one per notification | see *Notifications* |

## Commands

Commands are requests whose effect shows up in views. Results are `done` or
ids for correlation.

**Session and account**

| method | params | result |
| --- | --- | --- |
| `session_update` | `focused`, `active_chat_id`, `shows_notifications` | `done`: feeds notification suppression and `OpenChat` routing |
| `daemon_reconnect` | none | `done` |
| `account_logout` | none | `done`: logs out and wipes this account's data |
| `frontend_set_default` | `id` | `done`: the frontend a click or link starts; `NOT_FOUND` for one the daemon can't start |
| `link_open` | `url` | `done`: see Links; `INVALID_PARAMS` for a link it can't read, `NOT_FOUND` for a chat or person it doesn't know |

**Chats**

| method | params | result |
| --- | --- | --- |
| `chat_mark_read` | `chat_id`, `up_to_message_id` | `done`: the newest message the user saw; the daemon sends read receipts and recounts |
| `chat_pin` | `chat_id`, `pinned` | `done` |
| `chat_archive` | `chat_id`, `archived` | `done` |
| `chat_mute` | `chat_id`, `muted`, `duration_ms` (0 is forever) | `done` |
| `chat_typing` | `chat_id`, `composing`, `recording` | `done` |
| `chat_request_older` | `chat_id` | `requested`: false when the phone has nothing older or a request is out |
| `chat_ensure_direct` | `person` | `chat_id`: open it with `chat` and `messages`; its row appears in `chats` once something is in it, the first send included |

**Sends.** Each answers with the new message's id at once; the message
arrives through views, `PENDING` until it leaves.

| method | params |
| --- | --- |
| `send_text` | `chat_id`, `text`, `reply_to`, `mentions` |
| `send_media` | `chat_id`, `path`, `caption`, `reply_to`, `mentions`, `as_document`, `view_once`. The daemon copies the file before answering; the caller may delete its copy |
| `send_sticker` | `chat_id`, `sticker_id`, `reply_to` |

Every send takes a `key`: a string the frontend picks, unique per send (128
random bits is plenty). A send whose key the daemon has seen sends nothing and
answers the first one's result, so a frontend that lost the answer repeats the
request safely, across a reconnect or a daemon restart. The same key with
different params is `INVALID_PARAMS`. `message_forward` takes one key for the
whole forward. An empty key turns this off.

Sends can fail `GUARDED` on a daemon started with a send guard (a debug build
pointed at a real account), for an address outside it.

**Messages**

| method | params | result |
| --- | --- | --- |
| `message_react` | `message_id`, `emoji` ("" removes) | `done` |
| `message_edit` | `message_id`, `text` | `done`; `EXPIRED` past `edit_until_ms` |
| `message_revoke` | `message_id` | `done`; `EXPIRED` past whatsapp's window |
| `message_delete` | `message_id` | `done`: delete for me |
| `message_star` | `message_id`, `starred` | `done` |
| `message_pin` | `message_id`, `pinned`, `duration_ms` | `done` |
| `message_forward` | `message_id`, `chat_ids` | `message_ids`, in `chat_ids` order |
| `message_mark_played` | `message_id` | `done`: a played receipt for a voice note; again is a no-op |
| `message_request_from_phone` | `message_id` | `done`: asks our own phone to resend a message that would not decrypt. The daemon already asks once by itself; this is the manual retry. `REJECTED` for a message not waiting |
| `message_text` | `message_id` | `text` and `mentions`: the whole text of a row that came with `text_truncated` |
| `poll_vote` | `message_id`, `option_indexes` | `done`: the whole selection, not a change; empty withdraws every vote |
| `event_rsvp` | `message_id`, `response`, `extra_guests` | `done` |
| `group_join_invite` | `message_id` | `chat_id`: joins the group an invite row offers, or just says where it is if we're in it. `EXPIRED` for a dead invite |

**Media**

| method | params | result |
| --- | --- | --- |
| `media_download` | `message_id` | `done`: progress in `transfers`, the path lands on the row |
| `media_stream` | `message_id` | `stream_id`, `url`, `mime`, `size_bytes`, `duration_ms`. See *Files* |
| `media_cancel_download` | `message_id` | `done`; what landed is kept, so a later download resumes. `REJECTED` when nothing is running |
| `media_read` | `path`, `offset`, `max_bytes` | `data`, `size_bytes`, `eof`. See *Files* |
| `media_fetch_profile_picture` | `person` | `path`: full resolution, for an avatar viewer |

**Settings, people, stickers, notifications**

| method | params | result |
| --- | --- | --- |
| `privacy_set` | `category`, `value` | `done` |
| `preferences_set` | the fields to change; absent fields stay | `done` |
| `self_set_about` | `text` | `done` |
| `contact_block` | `person`, `blocked` | `done` |
| `sticker_favorite` | `sticker_id` or `message_id`, `favorite` | `done` |
| `sticker_download` | `sticker_id` | `done`: lands through sticker upserts |
| `sticker_pack_install` | `pack_id`, `installed` | `done` |
| `sticker_packs_refresh` | none | `done`: fetches the store's packs again |
| `notification_dismiss` | `id` | `done`: removed from `notifications` everywhere |

## Queries

One answer, nothing live, for data a frontend shows and throws away. The
results are whole rows inside the response.

| method | params | result |
| --- | --- | --- |
| `search_chats` | `query`, `limit` | `chats`, in list order |
| `search_messages` | `query`, `chat_id` (empty for all), `limit`, `before` | `messages` with `chat_name`, newest first; `more` says another page exists, asked for with `before` set to the last id |
| `search_stickers` | `query`, `limit` | `stickers`, daemon-ordered |
| `contact_check_phone` | `phone` | `registered`, the normalized `phone`, `person_id`, `name`, `business` |
| `frontend_list` | none | `frontends`: each one's `id`, `name`, `terminal`, `source`, `connected`, `is_default`, by id |

## Messages

A `MessageRow` has the facts every kind shares (id, chat, sender, time,
status, text, mentions, quote, reactions, edited, revoked, starred,
forwarded, pin, edit window, kept, view once, a send's error) and one arm of
`body` for what the kind adds. And always:

- `fallback`: one line any frontend can draw ("🎤 Voice message (0:12)",
  "📊 Poll: dinner?"). A frontend draws it for a body arm it does not know,
  which then shows as an unset oneof. New kinds never break a frontend.
- `text`: the words, or a media kind's caption, for every kind. Mentions in
  it already read `@Name`; each `Mention` names the person and the code point
  span.
- `edit_until_ms`: when whatsapp stops taking an edit, 0 for a row that can't
  be edited at all. A frontend greys out its edit action on it instead of
  holding a number whatsapp owns.

Media kinds (`image`, `video`, `gif`, `voice`, `audio`, `document`,
`video_note`, `sticker`) each carry a `Media`: mime, size, dimensions,
duration, `thumbnail_path`, `path` (empty until downloaded), `downloading`,
`download_error`. A voice note adds its `waveform`, 64 bytes of 0-100, the one
piece of media data on the socket, because the bubble needs it before any
download. A location's map is a `Media` too: the daemon stitches it from map
tiles and it downloads like anything else.

The download lifecycle: `media_download`, then the row upserts with
`downloading`, bytes move in `transfers`, then the row upserts with `path`
set and `downloading` cleared, or `download_error` set. A retry clears the
error with another upsert.

Reactions, a poll option's voters and an event's responders grow with the
group, so a row names at most 16 of each, the newest, and counts all of
them: `reaction_counts` per emoji (most first, `mine` on ours), `votes` per
option, `going`, `maybe` and `not_going` on an event. The `reactions`,
`poll_votes` and `event_responses` views list everyone.

The rest of the bodies are in the schema with a line each: polls and events
carry their tally, joined when read so a vote is one upsert; an album carries
its children as whole rows with their own ids, and they are not in the
transcript on their own while their album exists; a group invite carries what
the daemon learned by looking the code up; business cards are flattened from
whatsapp's four wire shapes into one `Interactive`; money is thousandths of a
unit plus an ISO 4217 code, never formatted.

**System rows** are something the chat did to itself: a `type`, the whole
sentence in `text`, and the parts it was built from (`actor`, the first few
`names` and an `overflow` count, `value`, `on`, `ephemeral_secs`) for a
frontend that speaks another language. They don't move the chat list, replace
its preview or count as unread, except one that named you (`about_self`).

**Waiting rows** are messages that would not decrypt. When the resend comes
it has the same id, so it upserts the waiting row in place, keeping its spot
and its unread count. `never` is set when whatsapp said it never will
decrypt here.

## Files

Rows carry absolute paths into the daemon's cache. A frontend on the same
machine opens them.

A frontend that can't reach the path (a sandbox, another machine later) reads
the bytes with `media_read`: one chunk per request, from `offset`, at most
`max_bytes` (capped by the daemon), until `eof`. Only paths the daemon put on
a row can be read.

`media_stream` is for playing a file that is still downloading. It answers
with a loopback http url (`127.0.0.1`, a per-process token in the url) that
serves range requests, so any player can seek. The download keeps going and
the row gets its `path` when done, which is what to use from then on. It
fails `REJECTED` for media that can't be streamed (no length or hash, a CDN
that ignores ranges); fall back to `media_download`. If a stream fails late,
the daemon finishes a whole download instead and tells the connection that
asked with one `MediaStreamUpdate`: `LOCAL` with the `path` to switch to, or
`FAILED`.

## Notifications

The daemon decides what deserves a notification (the chat isn't muted, isn't
open on a focused frontend, preferences allow it) and keeps one item per
notification in the `notifications` view, removed when its chat is read or
it is dismissed anywhere. A frontend may show them itself; it then says
`shows_notifications` in `session_update`, and the daemon's own notifier (D-Bus
on Linux) stays quiet while any connected session says so. With no such
session the daemon shows them itself.

## Connection events

Events without a subscription, sent to one connection:

| event | data | meaning |
| --- | --- | --- |
| `open_chat` | `chat_id` | a notification was clicked or a link opened; sent to the most recently focused frontend, or the most recently active one if none is focused |
| `activate` | none | whatevr itself was opened with no chat in mind; same target. a frontend that can raise its window does |
| `media_stream_update` | `stream_id`, `message_id`, `state`, `path` or `error` | see *Files*; at most once per stream, after its response |

## Frontends

The daemon knows which frontends it can start, so a notification click or a
link works with none open. It finds them in three places, and when two share an
id the later one wins:

1. **system**: json manifests a package installed, in `share/whatevr/frontends`
   next to the binary, in each `$XDG_DATA_DIRS/whatevr/frontends` on Linux, and
   in `Whatevr.app/Contents/Resources/frontends` on macOS.
2. **native**: a `.desktop` file with `X-Whatevr-Frontend=<id>` on Linux; on
   macOS an app that declares the `whatevr-frontend` URL scheme and names
   itself with a `WhatevrFrontendID` Info.plist key.
3. **user**: manifests in the config dir's `frontends`, which
   `whatevrd frontend add` writes.

A manifest is `{"id", "name", "exec", "terminal"}`: `exec` is the argv, run
without a shell, and `terminal` runs it in a terminal. That terminal is the
`terminal` preference with the argv appended, or the platform's when it's
empty (`xdg-terminal-exec`, then `$TERMINAL -e`; Terminal.app on macOS). The
`default_frontend` preference picks which one starts; empty is `whattui`.

`open_chat` and `activate` only go to connections that gave a `frontend_id`; a
tool on the socket can't show a chat. When a click or link finds no frontend,
the daemon starts the default one and holds the chat for 60 seconds: the first
frontend to say hello in that time gets it as `open_chat` right after its hello
answer. A newer chat replaces a held one.

### Links

`whatevr://` links only navigate; nothing in one sends. A dev build's app uses
`whatevr-dev://`, the same format. `link_open` takes one, and `whatevrd open`
is the command line for it, what the desktop's link handler runs.

| link | opens |
| --- | --- |
| `whatevr://` | no chat: `activate`, or the default frontend started |
| `whatevr://chat/<chat_id>` | that chat, by the id rows use |
| `whatevr://chat?phone=<number>` | the direct chat with that number, made if there is none; also `lid=` or `username=`, exactly one |

## Example

`examples/frontend.py` is a whole frontend in about 60 lines of Python on the
generated types: connect, hello, subscribe to the chat list, print what
arrives, send a message. If a change to the protocol makes that file much
longer or harder to read, the change is wrong.

## Open questions

- **Multi-account:** out of scope for protocol 2. Nothing here blocks an
  account on `hello` or a socket per account later.

### Platform socket defaults

Protocol version 2 is unchanged on macOS. When `WHATEVR_SOCKET` is absent, macOS
uses `in.codelif.whatevr.sock` directly in the per-user directory returned by
Darwin's `confstr(_CS_DARWIN_USER_TEMP_DIR)`, with no subdirectory: launchd creates
a missing socket directory owned by root, and boot clears that directory's
subdirectories. An explicit `XDG_RUNTIME_DIR` replaces it with
`$XDG_RUNTIME_DIR/whatevr/whatevrd.sock` on either platform. Linux continues to use
its XDG runtime directory.
`whatevrd paths --json` reports the resolved path. The notification app's separate
private IPC socket is an implementation detail, not part of this protocol.
