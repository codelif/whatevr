# Whatevr — Feature Gap Audit

> **Dated 2026-07-03, before the protocol migration finished.** The gRPC stack
> it audits against (`proto/whatevr.proto`, `whatevrd/internal/rpc/`) has since
> been deleted; the daemon now serves [PROTOCOL.md](PROTOCOL.md) on one socket.
> Read every "RPC" below as "daemon method". The *gaps* it lists are still
> accurate: they are missing features, not missing plumbing.

A comprehensive list of everything not yet implemented in whatevr, audited against the
**actual code**: `proto/whatevr.proto` (the full RPC surface), the daemon's
`internal/wa` / `internal/store` / `internal/notify` packages, the **whatkevr**
Qt/Kirigami frontend (QML + C++ controllers), and the public API of the pinned
whatsmeow (`v0.0.0-20260622185415-5f04eac6dbbb`) from the module cache.

Each item is one of: implementable with whatsmeow today (API named where relevant),
a standard app feature needing no whatsmeow, a **wired-but-unexposed** gap (daemon/
controller support exists, no UI entry point), a bug/rough edge, or an upstream
whatsmeow gap.

---

## 0. Baseline — what is ALREADY implemented (do not re-report these)

Verified in code, daemon + whatkevr:

**Account/connection:** QR login, logout, reconnect/backoff with retry status page,
history sync (initial, recent, on-demand older with per-chat exhaustion flag,
offline catchup, stall detection + "open phone" hint), daemon status page
(`StatusPage.qml`), systemd user units.

**Chat list:** pinned/archived sections, filters (Home / Direct messages / Groups),
unified search (chats + full-text messages + phone-number lookup via usync),
pin/archive chat, mute (8h / 1w / always / **custom duration dialog**), typing
indicator in the chat row, **draft indicator** ("Draft: …"), unread badges,
last-message preview with direction/status ticks, keyset pagination, self-profile
footer, starred-messages entry point.

**Conversation:** text/image/sticker bubbles (Lottie animated stickers via rlottie),
replies with quote preview + **jump-to-quoted-message** (with "not available"
toast), @-mentions (render + **mention autocomplete** with cached member list +
clickable mentions opening contact info), reactions (quick bar, full emoji popup,
chips, **ReactionDetailsDialog showing who reacted**), edit (with WhatsApp edit
window enforcement client- and daemon-side), revoke/delete-for-me (single + bulk),
forward to multiple chats (picker dialog), star/unstar, pin with durations
(24h/7d/30d) + pinned-messages banner + expiry handling, per-message info dialog
(per-participant delivered/read/played rows), **multi-select mode** (copy, copy as
Markdown, forward, reply, info, delete, select-all, whole-day toggle), context menu
(Reply/Edit/Forward/**Copy Text**/**Copy as Markdown**/Copy Link(s)/Copy
Image/Save As…/Star/Pin/Select/Info/Delete), date separators, **unread separator +
viewed-based read marking** (scroll-into-view + focus, not open), go-to-bottom FAB
with pending count, "Read more" long-message expansion + full-text dialog with
copy, in-chat search bar with match navigation (n of m, next/prev), per-chat
starred list, "Load older messages from phone" affordance, WhatsApp text formatting
**rendering** (bold/italic/strike/code spans etc. in `messagemarkup.cpp`),
URL linkification with real TLD table, image download-on-demand with streamed
progress circle + "Load image" button + auto-download prefs.

**Composer:** Enter-to-send (configurable to Ctrl+Enter), Shift+Enter newline,
**emoji autocomplete** (`:name:` suggestion mode) and mention suggestion bar,
emoji picker with categories/skin tone/recents, sticker picker (recents,
favorites, packs, search; favorite/unfavorite; install/uninstall packs), attach
image via file dialog, **paste image from clipboard** (`sendClipboardImage`),
image caption, reply banner, edit banner, per-chat **persistent drafts**
(optional), typing presence sent (composing).

**Info cards:** contact card (saved/push/business names, phone, about — fetched
async, business badge, avatar with full-res **ProfilePictureViewer**, "Message"
button), group card (subject, description, created date, member list with
**search**, admin/superadmin badges, member → contact card navigation with Back).

**Settings:** privacy (last seen/online/profile photo/about/read receipts/group
add/call add with correct audience constraints incl. "My contacts except…" as a
*choice*), blocked contacts list + **unblock**, notifications
(enable/preview/sound), auto-download toggles (photos/videos/audio/docs/stickers),
appearance (system/light/dark, KDE color schemes, density, message text size,
exact font size, chat-list avatars toggle), chats (enter behavior, drafts toggle,
**wallpaper: doodle pattern / custom SVG, scale, opacity, auto-tint, color**),
emoji skin tone + reset recents, storage (cache size, location, clear cache),
window layout (remember size/position, chat-list width), profile page (name,
about, phone, connection state, logout), keyboard shortcuts page, about page.

**Notifications (daemon-side D-Bus):** capability sniffing (actions, markup,
image), sender avatar image, message preview toggle, sound, click-to-open-chat
routed to the focused frontend via `HoldSession`/`OpenChat`, suppression for the
actively viewed chat.

Everything below is **missing** (or broken).

---

## 1. Receiving message types

Daemon-side, incoming handling stores only `Conversation`, `ExtendedTextMessage`,
`ImageMessage`, `StickerMessage`. whatsmeow already delivers all of the below in
`events.Message`; each needs storage (`MediaKind`), a bubble in whatkevr, and a
chat-list preview string:

- **Video** — playback, duration, thumbnail-first render (`VideoMessage`).
- **GIFs** — `VideoMessage` + `GifPlayback`; auto-looping muted playback.
- **Voice notes (PTT)** — `AudioMessage` with `PTT=true`; the wire message carries
  `Waveform` bytes and `Seconds`, so waveform bubbles are nearly free. Send the
  **"played" receipt** on listen (`MarkRead` with `types.ReceiptTypePlayed`) — the
  proto and Message Info dialog already model `played_ts_unix`, so the display half
  exists. Playback speed toggle (1×/1.5×/2×) in the bubble.
- **Audio files** — `AudioMessage` with `PTT=false`.
- **Documents** — `DocumentMessage`: filename, size, page count, MIME icon,
  open-with / save-as; plus the `DocumentWithCaptionMessage` wrapper (captioned
  documents currently arrive **invisible**).
- **Video notes (PTV)** — round instant videos (`PtvMessage`).
- **Location** — `LocationMessage`: lat/long, name, address, embedded JPEG thumb;
  click → OSM/system maps.
- **Live location** — `LiveLocationMessage` + its update stream.
- **Contact cards** — `ContactMessage` / `ContactsArrayMessage` (vCard parse,
  "Message" / "Add" actions).
- **Polls** — render `PollCreationMessage(V2/V3)`, tally votes via
  `DecryptPollVote`, show voters, **vote** via `BuildPollVote`/`EncryptPollVote`,
  live result updates.
- ~~**View-once media** — view-once photos/videos/voice notes render as *nothing*.~~
  **Done 2026-07-04:** whatsmeow's `UnwrapRaw` already unwraps the wrappers (sets
  `evt.IsViewOnce`); these now get an honest "View once — view on your phone"
  tombstone bubble. Actual view-once media display remains out of scope.
- ~~**Ephemeral wrapper** — `EphemeralMessage` is not unwrapped.~~ **Stale claim
  (verified 2026-07-04):** the pinned whatsmeow calls `evt.UnwrapRaw()` for both
  live messages and `ParseWebMessage`, so `EphemeralMessage` / `DeviceSentMessage` /
  `ViewOnceMessage*` / `DocumentWithCaptionMessage` arrive already unwrapped and
  ephemeral text/images render fine. The real gap was silent dropping of
  non-text/image/sticker payloads — fixed by the tombstone below.
- **Group invites** — `GroupInviteMessage`: group preview + Join button
  (`JoinGroupWithInvite`).
- **Event messages** — WhatsApp group events (`EventMessage`): time/place/RSVPs
  (`EncryptComment`/`DecryptComment`).
- **Albums** — `AlbumMessage`: collage-cluster consecutive photos/videos.
- **Buttons / lists / templates / interactive** business messages — at minimum
  render their text content instead of dropping them.
- **Order / product / catalog / payment / sticker-pack-share / call-log
  messages** — placeholder cards.
- **Keep-in-chat** (`KeepInChatMessage`) — honor + badge kept messages.
- **Link previews (receive)** — `ExtendedTextMessage` title/description/thumbnail
  are ignored; render the preview card.
- ~~**"Unsupported message" tombstone** — unrecognized `waE2E.Message` types should
  produce a visible "Unsupported message — view on phone" bubble~~ **Done
  2026-07-04:** documents/video/audio/voice/location/contacts/polls/events/group
  invites/view-once now store a `MediaKindUnsupported` tombstone with a best-effort
  label ("Voice message", "Poll: …", "Document: name.pdf") shown in bubble + chat
  preview. Note: tombstoned rows do **not** self-upgrade when real rendering for a
  kind lands later (dedup by message ID). `BuildUnavailableMessageRequest` fetch
  from phone still TODO.
- **System bubbles in history:** disappearing-timer changes, group
  join/leave/subject/photo/settings/admin changes (`events.GroupInfo` mutates
  state today but leaves no trace in the transcript), **`events.IdentityChange`**
  ("security code changed") — currently entirely unhandled.
- **Undecryptable placeholder retry** — `events.UndecryptableMessage` is stored,
  but there's no "Waiting for this message… request from phone" flow
  (`immediate/delayedRequestMessageFromPhone`).

## 2. Sending message types & composer

Outgoing media is **images only** (`outboundImageExtension`: jpeg/png/gif/webp,
hardcoded `MediaKindImage`; whatkevr's attach dialog filters to images and only
`sendImage` exists). All of the below is `Upload` + `SendMessage` with the right
proto fields:

- **Send video** (caption, thumbnail generation; optional transcode helper).
- **Send GIFs properly** — GIF→MP4 with `GifPlayback=true`. ~~Today a `.gif` file
  is accepted and sent as a static image — active misbehavior.~~ **Partial
  2026-07-04:** `.gif` is now rejected daemon-side with a clear error and filtered
  from the attach dialog/clipboard path; the actual GIF→MP4 send remains TODO.
- **Send documents** — any file type; "send as document" (uncompressed) option
  for images.
- **Send audio files**; **record & send voice notes** (opus, waveform, duration) —
  including the **"recording audio…" presence**: whatsmeow's `SendChatPresence`
  takes `ChatPresenceMediaAudio`, but the RPC only carries a `composing` bool, so
  the wire can't express it end-to-end.
- **Create sticker from image** — image→512×512 webp flow (library stickers send
  fine; ad-hoc creation missing).
- **Send location / contact cards / polls** (`BuildPollCreation`) /
  **view-once media**.
- **Multi-file send + album grouping**; multi-image selection in the file dialog.
- **Drag-and-drop files onto the chat** — paste-from-clipboard exists, a
  `DropArea` does not.
- **Link previews (send)** — fetch OpenGraph locally, fill `ExtendedTextMessage`
  preview fields.
- **Mentions in media captions** (mentions are text-path only:
  `SendMediaRequest` has no `mentioned_jids`).
- **Reply-to context for new media kinds** — `quotedMessageFromStored`
  reconstructs only sticker/image/text; extend per kind as they land.
- **Forwarded flag** — verify forwards set `ContextInfo.IsForwarded` /
  `ForwardingScore`, and render the "Forwarded" / "Forwarded many times" badge on
  receive (nothing in the proto models it).
- **Broadcast lists** — true broadcast send (whatsmeow `broadcast.go`).
- **Scheduled local sends** — daemon send-queue already persists pending
  messages; add a `send_at` column and it falls out.
- **Composer polish (no whatsmeow):** spellcheck (none today — Qt/Sonnet),
  formatting toolbar or Ctrl+B/I shortcuts that wrap selection in `*` `_` `~`,
  multi-line paste warning, attach-anything button, image paste preview dialog
  with caption before send (does paste send immediately? verify UX), emoji
  variation for recently-used sync with phone.

## 3. Message actions & in-chat UX

- ~~**Full-screen image lightbox for message photos** — `ProfilePictureViewer`
  exists for avatars, but clicking a message image opens nothing.~~ **Done
  2026-07-04:** clicking a downloaded message photo opens the full-screen viewer
  (reuses `ProfilePictureViewer`). Zoom/pan/gallery arrows still TODO.
- **In-chat media / links / docs gallery** — "Media, links and docs" per chat
  (pure local SQLite; links are already extractable by the markup engine).
- **Reaction with any emoji from the full picker** exists? The
  `ReactionEmojiPopup` exists — verify it exposes the complete emoji set, not
  just quick reactions; if so this is done, else finish it.
- **Copy selection of a bubble's text** (partial-text selection in bubbles) and
  "reply to selection".
- **Message translation hook** (local/offline or service-configurable).
- **Jump to date** — calendar scrubber in chat scroll.
- **Unread mentions badge** ("@" pill on the chat row when you were mentioned in
  unread messages — needs a small store flag; all local).
- **Search filters** — by sender, by media kind, date range (FTS store exists;
  extend query + UI chips).
- **Starred/pinned rows → jump into conversation context** — StarredMessagesPage
  and the pinned banner list items; verify each navigates via
  `showMessageInChat` (banner does; starred page should too).

## 4. Chat list & chat management

- **Mark chat as read (manual)** and **mark as unread** — the viewed-based
  auto-read is implemented, but there is no context-menu override; unread needs
  `appstate.BuildMarkChatAsRead(read=false)` (unused in whatsmeow) + a store
  flag.
- **Delete chat** — `appstate.BuildDeleteChat` (syncs to phone) + local purge +
  confirmation. No RPC exists.
- **Clear chat** (keep the row, wipe messages) — local wipe + appstate clear.
- **Exit group from the chat row** (see §5) and **Block from the chat row/card**
  (see §10).
- **Unread-only filter chip** and **archived-list unread badge**; "keep chats
  archived" toggle (WA setting: archived chats stay archived on new messages —
  verify current daemon behavior and expose the choice).
- **Favorites filter** — WA favorites sync via appstate `favorites` patches;
  worst case implement locally.
- **Chat-list previews for every new media kind** (🎤 0:12, 📄 name.pdf, 📍
  Location, 📊 poll question…) — falls out of §1 but the preview strings live in
  the daemon's chat store, list it so it isn't forgotten.
- **Per-chat notification override** (mute is synced; a local "notify without
  sound / hide preview for this chat" tier is UI-only).
- **Per-chat wallpaper** — global wallpaper/pattern engine already exists
  (`ChatWallpaper.qml`); add a per-chat key.
- **Disappearing messages** — per-chat timer (`SetDisappearingTimer`), default
  for new chats (`SetDefaultDisappearingTimer`), local expiry sweep, timer state
  in the header, system bubbles (§1).
- **Chat lock / hidden chats** (local PIN/keyring).
- **Export chat** to txt/zip with media (local).
- **Labels** — WhatsApp labels: `BuildLabelChat` / `BuildLabelMessage` /
  `BuildLabelEdit` all exist in whatsmeow appstate and are unused; incoming
  `events.LabelEdit` / `events.LabelAssociation` are not handled.

## 5. Groups — the single biggest gap (whatsmeow has 100% of this, zero RPCs exist)

The group card is view-only. All directly available in whatsmeow:

- **Create group** — `CreateGroup` (name, members, photo); "New group" flow in
  the sidebar.
- **Add/remove members, promote/demote admins** — `UpdateGroupParticipants`;
  actions on the member list (UI skeleton — member rows, search, admin badges —
  already exists in `ContactInfoDialog`).
- **Leave group** — `LeaveGroup` (+ render "you left", disable composer).
- **Edit subject/description/photo** — `SetGroupName`, `SetGroupTopic`/
  `SetGroupDescription`, `SetGroupPhoto` (nil clears).
- **Group permission settings** — `SetGroupAnnounce` (admins-only send: also
  needs composer lockout when announce is on and you're not admin — currently
  nothing checks this), `SetGroupLocked`, `SetGroupMemberAddMode`,
  `SetGroupJoinApprovalMode`.
- **Invite links** — `GetGroupInviteLink(reset)` show/copy/revoke; **join via
  link** `JoinGroupWithLink` with preview (`GetGroupInfoFromLink` /
  `GetGroupInfoFromInvite`); detect chat.whatsapp.com links in message text and
  offer join.
- **Join requests** — `GetGroupRequestParticipants` +
  `UpdateGroupRequestParticipants` (approve/reject) with a pending-requests
  badge for admins.
- **Groups directory** — `GetJoinedGroups` browser.
- **Member actions** — message privately (EnsureDirectChat exists — wire it),
  make/dismiss admin, remove.
- **Owner + "created by" in the card** (`GroupInfo.OwnerJID` is available;
  created time is already shown).
- **Mention-all helper** for admins (compose `@` for every member).

## 6. Communities

Nothing exists. whatsmeow: `GetSubGroups`, `GetLinkedGroupsParticipants`,
`LinkGroup`/`UnlinkGroup`. Community list, linked-group browser, announcement
group rendering, community info card.

## 7. Channels (newsletters)

Nothing exists; whatsmeow has a full API: `GetSubscribedNewsletters`,
`GetNewsletterInfo(WithInvite)`, `GetNewsletterMessages` +
`NewsletterSubscribeLiveUpdates` + `GetNewsletterMessageUpdates` (view counts),
`FollowNewsletter`/`UnfollowNewsletter`, `NewsletterSendReaction`,
`NewsletterToggleMute`, `NewsletterMarkViewed`, `CreateNewsletter`,
`UploadNewsletter(Reader)` (note: channel media is plaintext uploads), channel
comments via `DecryptComment`/`EncryptComment`. Needs its own sidebar section, a
feed view, and reaction UI.

## 8. Status / Stories

Nothing exists. Statuses arrive as ordinary `events.Message` from
`status@broadcast` (currently dropped or misfiled — **verify they don't pollute
the chat list**):

- Status tab: contact rings, auto-advance viewer for text (bg color/font),
  photo, video, audio statuses; mark-viewed receipts.
- **Post status** — send to `status@broadcast`, audience from
  `GetStatusPrivacy`; text-status composer with backgrounds.
- Status replies (DM with quoted status context), mute someone's status.

## 9. Calls

Zero handling — `events.CallOffer` / `CallOfferNotice` / `CallTerminate` /
`CallReject` are not subscribed, so **an incoming call shows nothing at all**:

- Incoming-call notification ("answer on your phone"), missed-call chat-list
  entry / call log (local persistence).
- **Reject from desktop** — `RejectCall` exists in whatsmeow.
- Actually answering/placing calls is upstream-blocked (§17).

## 10. Contacts, profile & account

- ~~**Edit own About text** — wired but unexposed.~~ **Done 2026-07-04:** About is
  editable on the profile page via the existing `SetProfileStatus` plumbing.
- **Set own profile photo** — the `w:profile:picture` IQ with self target
  (whatsmeow's `SetGroupPhoto` path); add change/remove on the profile page.
- **Set own push name** — `appstate.BuildSettingPushName` exists in the pinned
  whatsmeow. ~~The proto comment on `SetProfileStatus` claiming the display name
  is not settable via whatsmeow is outdated — fix comment~~ (comment fixed
  2026-07-04) — the RPC + UI implementation is still TODO.
- **Block/report from the contact card and chat** — ~~wired but unexposed: add
  Block on the contact card (with confirm)~~ **Done 2026-07-04:** Block/Unblock
  (with confirmation) on the contact card. Block+report-spam still TODO
  (whatsmeow has reporting-token plumbing).
- **Contact list / "New chat" picker** — browse the synced address book
  (`Store.Contacts.GetAllContacts`) instead of only searching existing chats and
  typing full phone numbers.
- **Shared groups in common** on the contact card (local join across group
  participant store).
- **Last seen / online in the contact card and chat header** — presence events
  are already consumed for the conversation; surface availability + last-seen
  line in the header and card.
- **Contact QR** — show mine (`GetContactQRLink`), resolve scanned/pasted ones
  (`ResolveContactQRLink`); handle `wa.me/...` links in search and message text.
- **Security/identity screen** — `GetUserDevices`, identity key comparison,
  40-digit code + QR verify; pairs with the missing `events.IdentityChange`
  handling (§1).
- **Business profile detail** — `GetBusinessProfile`: category, address, hours,
  website, catalog link (card shows only a "Business account" badge today).
- **Meta AI bots** — `GetBotListV2` / `GetBotProfiles` (label those chats
  properly at minimum).

## 11. Privacy, security & settings

- **"My contacts except…" exception picker** — the audience is selectable in
  `PrivacyAudienceCombo` but there is **no UI or RPC to edit the exception
  list**. The one-IQ `contact_blacklist` protocol is documented in project
  memory (retired-fork notes) and works against upstream now.
- **Status privacy** — `GetStatusPrivacy` (pairs with §8).
- **Default disappearing timer** — `SetDefaultDisappearingTimer` (pairs with §4).
- **App lock** — PIN/keyring lock for the desktop app (local).
- **Encrypted DB at rest** — SQLCipher option for the daemon store (local).
- **Proxy support** — `SetProxy`/`SetSOCKSProxy`/`SetProxyAddress` + daemon
  config + settings UI (censored-network users).
- **Multi-account** — N whatsmeow clients over one sqlstore container; account
  switcher in the sidebar.
- **Local backup/restore** of the daemon DB + media (export/import bundle).
- **Storage management, deeper** — per-chat usage breakdown, auto-download size
  caps, auto-clear policy (page exists with only total + clear-all).
- **Temporary-ban / TOS screens** — `events.TemporaryBan` is handled
  daemon-side; verify whatkevr renders a real blocking screen; `AcceptTOSNotice`
  for TOS blocks.

## 12. Login & connection

- **Pair by phone number** — `PairPhone` (8-char code) as an alternative to QR;
  needed for headless daemon installs and accessibility.
- **Re-login without daemon restart** after `events.LoggedOut` — verify the
  LoginPage flow covers a mid-session logout-from-phone gracefully.
- **"Appear offline" mode** — `SendPresence(unavailable)` strategy toggle
  (notifications keep flowing to the phone); related: `SetPassive`.
- **Connection diagnostics** — keepalive latency history
  (`KeepAliveTimeout/Restored` already handled), socket state timeline on the
  status page.

## 13. Media pipeline & storage

- **Media retry protocol** — when media expired from WA servers, send
  `SendMediaRetryReceipt` and consume `events.MediaRetry` so the phone re-uploads
  (today an expired download is a dead error).
- **Thumbnail-first everywhere** — `DownloadThumbnail` for video/docs before the
  full blob.
- **Streaming playback** — play video/audio while `DownloadToFile` streams.
- **Content-hash dedup for all media** (stickers already dedup by SHA256; extend
  to images/video/docs across chats).
- **`DeleteMedia`** server-side cleanup after revoke.
- **EXIF strip toggle on image send** (privacy; local).
- The auto-download prefs for video/audio/documents are **dead switches** until
  §1 lands those kinds — either wire them or hide them.

## 14. Notifications & Linux desktop integration

Current daemon notifier is solid (avatars, markup, sound, click-to-open). Missing:

- **Inline reply action** — `content.Actions` is only `{"default", "Open Chat"}`;
  org.freedesktop.Notifications inline-reply capability (`inline-reply` cap,
  `x-kde-reply` hints) lets you reply straight from the popup; add "Mark as
  read" action too.
- **Notification grouping/stacking per chat** and replace-on-update
  (`replaces_id`) instead of one popup per message.
- **DND / notification schedule / snooze-per-chat until…** (local).
- **System tray / StatusNotifierItem** — none exists; unread count badge,
  close-to-tray, middle-click quick actions.
- **Launcher badge** — Unity LauncherEntry D-Bus unread count on the taskbar
  icon.
- **Autostart toggle** in settings (systemd user unit exists; expose
  enable/disable).
- **Start minimized** option (WindowLayout page has "On startup" group —
  extend).
- **Multi-window** — pop a conversation into its own window.
- **Global shortcuts audit** — only `StandardKey.Preferences` is a window-level
  `Shortcut`; add Ctrl+K chat switcher, Ctrl+F in-chat search, Alt+Up/Down
  next/prev chat, Ctrl+W close chat (a "Close Chat" action exists — verify its
  binding), Esc-to-clear stack; the KeyboardShortcutsPage lists only 5 entries.
- **XDG portals** — verify file dialogs go through portals for Flatpak
  readiness; "Show in folder" / "Open with…" on saved media.
- **MPRIS** for voice-note/audio playback once audio lands.
- **KRunner / GNOME Shell search provider** — fuzzy-find chats from the shell
  (search RPCs already exist; D-Bus shim).
- **Share portal target** — "Send via Whatevr" system-wide.
- **CLI companion** (`whatevrctl send/list/watch`) over the protocol socket,
  near-free and huge for scripting; `examples/shell-frontend.sh` is most of it
  already.
- **Packaging spread** — Flatpak/flathub manifest, RPM/deb CI artifacts (AUR
  exists).

## 15. whatkevr parity notes (frontend-only debt)

Where the daemon already provides everything and only QML/controller work
remains:

- ~~Edit **About** (§10), **Block** entry points (§10) — both one-dialog jobs.~~
  **Done 2026-07-04.**
- ~~Image lightbox (§3).~~ **Done 2026-07-04** (basic viewer; zoom/pan TODO).
- Caption editing UI when pasting an image (verify paste → caption path).
- Read-receipts-off awareness: when the account disables read receipts, grey
  ticks should still show delivered correctly — verify MessageInfo/status
  rendering against privacy state.
- Accessibility pass: `Accessible.name` exists in places (sidebar); audit
  bubbles/menus; sticker `accessibility_text` is modeled — verify it reaches
  `Accessible.name` on sticker bubbles.
- RTL layout audit; translation catalog completeness (I18n wrapper exists).
- whatgevr (the Rust/GTK frontend) trails whatkevr on nearly all of §0 — decide
  whether it's a showcase or a maintained peer, and say so in the README.

## 16. Bugs & rough edges (from this audit)

- ~~**Ephemeral wrapper not unwrapped**~~ **Stale (verified 2026-07-04):** whatsmeow
  `UnwrapRaw` already unwraps it on both live and history-sync paths (§1).
- ~~**View-once and captioned-document messages silently dropped**~~ **Done
  2026-07-04:** both arrive unwrapped and now render as labeled tombstones (§1).
- ~~**`.gif` sent as a static image** via the image path (§2)~~ **Done 2026-07-04:**
  rejected with a clear typed error (transcode-to-video path still TODO, §2).
- ~~**Outdated proto comment**: `SetProfileStatus` doc says push name isn't
  settable via whatsmeow~~ **Fixed 2026-07-04** (implementation tracked in §10).
- ~~**`events.DeleteForMe` / `DeleteChat` / `ClearChat` unhandled**~~ **Done
  2026-07-04:** phone-side delete-for-me hard-deletes the local row; delete chat /
  clear chat sync to the local store and open frontends.
- **`events.MarkChatAsRead` not in the handled-event list** — phone-side
  read/unread toggles may only partially sync (self-receipts vs appstate);
  verify unread counts after marking read on the phone while whatevr is closed.
- **Call events invisible** (§9).
- **`events.IdentityChange` unhandled** — no security-code-change notices, and
  no re-verify prompt (§1, §10).
- **Status broadcast messages** — confirm `status@broadcast` traffic doesn't
  create a bogus chat row (§8).
- **Deprecated `offset` in `ListChatsRequest`** — documented as skip/repeat-prone;
  remove once no frontend uses it.
- **SendMedia error surface** — non-image file paths should fail with a typed,
  user-explainable gRPC error; verify it's not a generic string.
- **Announce-mode groups** — composer isn't disabled when only admins may send
  (§5); sends will just fail server-side.
- **History-sync stall** has detection + hint; add a user-triggered retry
  (re-issue `BuildHistorySyncRequest`) instead of waiting on the phone.
- **Edit-window UX** — `canEditAt` hides Edit client-side and daemon enforces;
  also hide **Delete for Everyone** past WhatsApp's revoke window (server
  rejects late revokes; check the daemon surfaces that failure).

## 17. Upstream whatsmeow gaps (WhatsApp Web has it; whatsmeow does not)

Track separately; not buildable without upstream/reverse-engineering work:

- **Voice/video call media** — signaling events + `RejectCall` only; no
  SRTP/media stack, so answering/placing calls (and screen share) is impossible.
- **Payments** — render-only; no initiation API.
- **Catalog/cart/business commerce ops** — only `GetBusinessProfile` +
  message rendering.
- **Curated sticker-store browsing** — `FetchStickerPack` covers synced/shared
  packs only.
- **Newsletter admin/moderation ops** — partially covered MEX calls upstream.
- **Meta AI conversational features** beyond bot listing.
- **Usernames (@handles)** — rolling out in WA; watch upstream `usync`/JID work.
- **Companion media-quality (HD default) account setting sync** — no API.

## 18. Beyond-parity ideas (differentiators)

- **Local voice-note transcription** (whisper.cpp) — desktop-only superpower;
  pairs with voice-note playback (§1).
- **Local semantic search** (embeddings) layered over existing FTS.
- **Rule engine / hooks** — "on message matching X from Y, run Z" + the
  `whatevrctl` CLI = the scriptable WhatsApp for Linux.
- **Headless relay mode** — daemon-only install forwarding to ntfy/email/Matrix
  when no frontend holds a session (SessionBus already knows).
- **Date/time detection → "add to calendar"** action on messages.
- **Message statistics dashboard** — volume, response times, top emoji (local
  SQLite).
- **Archive exporter** — per-chat Markdown/HTML with inlined media.
- **Read-position sync across frontends** — HoldSession/focus plumbing exists;
  share viewed-state between whatkevr windows/instances.
- **Smart auto-download** — learn per-chat which media the user always opens.

---

*Generated 2026-07-03. Sources: `proto/whatevr.proto`; `whatevrd/internal/{wa,store,notify,rpc}`;*
*`whatkevr/src` (all QML pages/components, `AppController`, `messagemarkup`); whatsmeow*
*`v0.0.0-20260622185415-5f04eac6dbbb` public API (`Client` methods + `appstate` builders + `types/events`).*
