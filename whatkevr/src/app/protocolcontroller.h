#pragma once

#include <QAbstractItemModel>
#include <QJsonObject>
#include <QObject>
#include <QHash>
#include <QString>
#include <QStringList>
#include <QUrl>
#include <QVariantMap>
#include <qqmlintegration.h>

#include <cstdint>


QT_BEGIN_NAMESPACE
class QQmlEngine;
class QJSEngine;
class QTimer;
QT_END_NAMESPACE

namespace whatevr::proto
{
class ProtocolClient;
class ObjectViewModel;
class CollectionViewModel;
class Subscription;
} // namespace whatevr::proto

class ProtocolMessageModel;
class ProtocolSearchModel;
class ProtocolStickerController;
class EmojiModel;

// The app's single controller: it owns the ProtocolClient (the one socket to
// the daemon's PROTOCOL.md surface), subscribes every view the UI renders, and
// derives from those views each string, model and command the QML binds. Since
// D7 there is no second stack — the whole frontend is `Whatevr.ProtocolController`
// plus the generic view models it hands out.
//
// It holds no daemon state of its own: rows arrive as keyed upserts ordered by
// the daemon's opaque `sort`, commands return acks only, and the few members
// that do keep state are presentation-only (selection, drafts, search cursors),
// which rule 1 leaves to the frontend.
class ProtocolController final : public QObject
{
    Q_OBJECT
    QML_NAMED_ELEMENT(ProtocolController)
    QML_SINGLETON

    // Shell routing, driven by the `connection`/`login` views.
    Q_PROPERTY(bool starting READ starting NOTIFY stateChanged FINAL)
    Q_PROPERTY(bool loginRequired READ loginRequired NOTIFY stateChanged FINAL)
    Q_PROPERTY(bool shellVisible READ shellVisible NOTIFY stateChanged FINAL)

    // Status page.
    Q_PROPERTY(QString connectionPhase READ connectionPhase NOTIFY stateChanged FINAL)
    Q_PROPERTY(bool daemonRunning READ daemonRunning NOTIFY stateChanged FINAL)
    Q_PROPERTY(bool loading READ loading NOTIFY stateChanged FINAL)
    Q_PROPERTY(QString statusTitle READ statusTitle NOTIFY stateChanged FINAL)
    Q_PROPERTY(QString statusText READ statusText NOTIFY stateChanged FINAL)
    Q_PROPERTY(QString detailText READ detailText NOTIFY stateChanged FINAL)
    Q_PROPERTY(QString bannerText READ bannerText NOTIFY stateChanged FINAL)
    Q_PROPERTY(QString actionError READ actionError NOTIFY stateChanged FINAL)
    Q_PROPERTY(QString primaryActionText READ primaryActionText NOTIFY stateChanged FINAL)
    Q_PROPERTY(bool primaryActionEnabled READ primaryActionEnabled NOTIFY stateChanged FINAL)
    Q_PROPERTY(QString daemonServiceCommand READ daemonServiceCommand CONSTANT FINAL)
    Q_PROPERTY(QString daemonBinaryCommand READ daemonBinaryCommand CONSTANT FINAL)
    Q_PROPERTY(QString daemonInstructions READ daemonInstructions CONSTANT FINAL)

    // Login page.
    Q_PROPERTY(bool qrAvailable READ qrAvailable NOTIFY stateChanged FINAL)
    Q_PROPERTY(QString qrCode READ qrCode NOTIFY stateChanged FINAL)
    Q_PROPERTY(QString qrExpiryText READ qrExpiryText NOTIFY stateChanged FINAL)

    // Chat list (D2b1). The model is the generic keyed/sorted collection over
    // the `chats` view; QML binds `model.item.<field>` off its rows. The filter
    // is a subscribe param (all/direct/groups), so changing it re-subscribes —
    // no frontend-side filtering. loading/empty drive the list placeholders.
    Q_PROPERTY(QAbstractItemModel *chatsModel READ chatsModel CONSTANT FINAL)
    Q_PROPERTY(int chatFilter READ chatFilter WRITE setChatFilter NOTIFY chatFilterChanged FINAL)
    Q_PROPERTY(bool chatsLoading READ chatsLoading NOTIFY chatsChanged FINAL)
    Q_PROPERTY(bool chatsEmpty READ chatsEmpty NOTIFY chatsChanged FINAL)
    // The list is windowed (DN6): the sidebar extends it as it scrolls instead
    // of asking the daemon for every chat at once. Exhausted means the daemon
    // has nothing past the current window.
    Q_PROPERTY(bool chatsExhausted READ chatsExhausted NOTIFY chatsChanged FINAL)

    // Archived chats (D2b2): a second `chats` subscription (`archived: true`) for
    // the collapsible archived section; honours the same filter as the main list.
    Q_PROPERTY(QAbstractItemModel *archivedChatsModel READ archivedChatsModel CONSTANT FINAL)
    Q_PROPERTY(int archivedCount READ archivedCount NOTIFY archivedChanged FINAL)
    // Windowed like the active list, so archivedCount is the loaded window, not
    // the true total; the header renders "N+" while more remain.
    Q_PROPERTY(bool archivedExhausted READ archivedExhausted NOTIFY archivedChanged FINAL)

    // Typing overlay (D2b2): the global `typing` view, keyed by chat_id. The
    // delegate reads chatTyping(chatId); typingRevision bumps on every change so
    // the binding re-evaluates (a function call alone would not).
    Q_PROPERTY(int typingRevision READ typingRevision NOTIFY typingChanged FINAL)

    // History-sync strip (D2b2): derived from the `sync` object view. The names
    // mirror AppController's so the HistorySyncStrip bindings carry over verbatim.
    Q_PROPERTY(bool historySyncVisible READ historySyncVisible NOTIFY historySyncChanged FINAL)
    Q_PROPERTY(int historySyncPercent READ historySyncPercent NOTIFY historySyncChanged FINAL)
    Q_PROPERTY(QString historySyncTitle READ historySyncTitle NOTIFY historySyncChanged FINAL)
    Q_PROPERTY(QString historySyncDetail READ historySyncDetail NOTIFY historySyncChanged FINAL)

    // Conversation selection + protocol `messages` timeline (D3b).
    Q_PROPERTY(QAbstractItemModel *messageListModel READ messageListModel CONSTANT FINAL)
    Q_PROPERTY(QString selectedChatId READ selectedChatId NOTIFY selectionChanged FINAL)
    Q_PROPERTY(QString selectedChatName READ selectedChatName NOTIFY selectionChanged FINAL)
    Q_PROPERTY(QString selectedChatAvatarLocalPath READ selectedChatAvatarLocalPath NOTIFY selectionChanged FINAL)
    Q_PROPERTY(bool hasSelectedChat READ hasSelectedChat NOTIFY selectionChanged FINAL)
    Q_PROPERTY(int selectedChatUnreadCount READ selectedChatUnreadCount NOTIFY selectionChanged FINAL)
    Q_PROPERTY(bool selectedChatHistoryExhausted READ selectedChatHistoryExhausted NOTIFY selectionChanged FINAL)
    Q_PROPERTY(bool messagesLoading READ messagesLoading NOTIFY messagesChanged FINAL)
    Q_PROPERTY(bool olderMessagesLoading READ olderMessagesLoading NOTIFY messagesChanged FINAL)
    Q_PROPERTY(bool newerMessagesLoading READ newerMessagesLoading NOTIFY messagesChanged FINAL)
    Q_PROPERTY(bool canLoadOlderMessages READ canLoadOlderMessages NOTIFY messagesChanged FINAL)
    Q_PROPERTY(bool canLoadNewerMessages READ canLoadNewerMessages NOTIFY messagesChanged FINAL)
    Q_PROPERTY(bool olderMessagesFailed READ olderMessagesFailed NOTIFY messagesChanged FINAL)
    Q_PROPERTY(bool newerMessagesFailed READ newerMessagesFailed NOTIFY messagesChanged FINAL)
    Q_PROPERTY(bool messagesAtLiveEdge READ messagesAtLiveEdge NOTIFY messagesChanged FINAL)
    Q_PROPERTY(bool phoneHistoryRequesting READ phoneHistoryRequesting NOTIFY messagesChanged FINAL)
    Q_PROPERTY(bool messagesEmpty READ messagesEmpty NOTIFY messagesChanged FINAL)
    Q_PROPERTY(QString displayedMessagesChatId READ displayedMessagesChatId NOTIFY messagesChanged FINAL)
    Q_PROPERTY(QString messageErrorText READ messageErrorText NOTIFY messagesChanged FINAL)
    Q_PROPERTY(QString unreadAnchorMessageId READ unreadAnchorMessageId NOTIFY unreadAnchorChanged FINAL)
    Q_PROPERTY(int unreadAnchorCount READ unreadAnchorCount NOTIFY unreadAnchorChanged FINAL)
    Q_PROPERTY(bool unreadAnchorResolving READ unreadAnchorResolving NOTIFY unreadAnchorChanged FINAL)

    // Conversation-header presence (D3c): the selected chat's `presence` view
    // (availability/last seen) composed with the global `typing` view. Subscribing
    // is what asks WhatsApp for availability, so the subscription follows exactly
    // what the conversation is showing.
    Q_PROPERTY(QString selectedChatPresenceText READ selectedChatPresenceText NOTIFY presenceChanged FINAL)

    // Message-info dialog (D3c): the per-message `receipts` view, subscribed only
    // while the dialog is open. Rows are the daemon's participant items; the
    // dialog groups them for display and holds no receipt state of its own.
    Q_PROPERTY(bool messageReceiptsLoading READ messageReceiptsLoading NOTIFY messageReceiptsChanged FINAL)
    Q_PROPERTY(QString messageReceiptsError READ messageReceiptsError NOTIFY messageReceiptsChanged FINAL)
    Q_PROPERTY(bool messageReceiptsIsGroup READ messageReceiptsIsGroup NOTIFY messageReceiptsChanged FINAL)
    Q_PROPERTY(qint64 messageReceiptsSentTimestamp READ messageReceiptsSentTimestamp NOTIFY messageReceiptsChanged FINAL)
    Q_PROPERTY(int messageReceiptsRevision READ messageReceiptsRevision NOTIFY messageReceiptsChanged FINAL)

    // Composer + send paths (D4a): `send.text`/`send.media` acks and the
    // in-flight/error state MessageComposer binds to. Sent messages are never
    // applied locally — they arrive back through the `messages` view upsert
    // like any other message.
    Q_PROPERTY(bool composerEnabled READ composerEnabled NOTIFY composerChanged FINAL)
    Q_PROPERTY(bool sendInFlight READ sendInFlight NOTIFY composerChanged FINAL)
    Q_PROPERTY(QString composerErrorText READ composerErrorText NOTIFY composerChanged FINAL)

    // Pinned-message banner (D4b): the displayed chat's `pinned` view. The
    // banner shows one pin at a time, so it reads rows by index rather than
    // instantiating a delegate per row; `ready` mirrors the subscription's
    // initial fill so the conversation can reserve the banner's height.
    Q_PROPERTY(bool pinnedMessagesReady READ pinnedMessagesReady NOTIFY pinnedMessagesChanged FINAL)
    Q_PROPERTY(int pinnedMessagesCount READ pinnedMessagesCount NOTIFY pinnedMessagesChanged FINAL)

    // Forward picker (D4b): a `chats` subscription that lives exactly as long as
    // the picker dialog is open. The dialog's search box filters the rows it
    // already has (presentation-side, like the group-member and receipt lists);
    // the revision tick makes those reads re-evaluate.
    Q_PROPERTY(int forwardTargetsRevision READ forwardTargetsRevision NOTIFY forwardTargetsChanged FINAL)

    // Unified chat-list search (D5): the `search.chats` / `search.messages` /
    // `contacts.check_phone` *queries*. Queries are one-shot and their results
    // are explicitly frontend-transient (PROTOCOL.md "Queries"), so unlike a
    // view they land in a plain presentation model the frontend throws away.
    Q_PROPERTY(QAbstractItemModel *searchResultsModel READ searchResultsModel CONSTANT FINAL)
    Q_PROPERTY(QString searchQuery READ searchQuery NOTIFY searchChanged FINAL)
    Q_PROPERTY(bool searchActive READ searchActive NOTIFY searchChanged FINAL)
    Q_PROPERTY(bool searchBusy READ searchBusy NOTIFY searchChanged FINAL)

    // In-chat search (D5): the same `search.messages` query scoped to the
    // selected chat, plus the frontend's own match cursor (which match is
    // focused is presentation state, rule 1).
    Q_PROPERTY(bool chatSearchActive READ chatSearchActive NOTIFY chatSearchChanged FINAL)
    Q_PROPERTY(QString chatSearchQuery READ chatSearchQuery NOTIFY chatSearchChanged FINAL)
    Q_PROPERTY(int chatSearchMatchCount READ chatSearchMatchCount NOTIFY chatSearchChanged FINAL)
    Q_PROPERTY(int chatSearchCurrentIndex READ chatSearchCurrentIndex NOTIFY chatSearchChanged FINAL)
    Q_PROPERTY(QString chatSearchActiveMessageId READ chatSearchActiveMessageId NOTIFY chatSearchChanged FINAL)

    // Starred-messages page (D5): the `starred` view, subscribed for exactly as
    // long as the page is on screen. Windowed like any collection — the page
    // extends `older` as it scrolls instead of loading every star at once.
    Q_PROPERTY(QAbstractItemModel *starredMessagesModel READ starredMessagesModel CONSTANT FINAL)
    Q_PROPERTY(bool starredMessagesLoading READ starredMessagesLoading NOTIFY starredMessagesChanged FINAL)
    Q_PROPERTY(bool starredMessagesExhausted READ starredMessagesExhausted NOTIFY starredMessagesChanged FINAL)

    // Contact/group info card (D5): the `contact` object view, or the `group`
    // object view plus its `group_members` roster, subscribed for the lifetime
    // of the dialog. Two-phase enrichment (a contact's about text, a group's
    // live subject/roles) arrives as ordinary upserts, so the dialog holds no
    // card state and needs no merge step. `infoCardBlocked` composes the
    // `blocklist` view the same way the chat rows compose `typing`.
    Q_PROPERTY(QString infoCardKind READ infoCardKind NOTIFY infoCardChanged FINAL)
    Q_PROPERTY(QString infoCardSubject READ infoCardSubject NOTIFY infoCardChanged FINAL)
    Q_PROPERTY(QVariantMap infoCard READ infoCard NOTIFY infoCardChanged FINAL)
    Q_PROPERTY(bool infoCardLoading READ infoCardLoading NOTIFY infoCardChanged FINAL)
    Q_PROPERTY(QString infoCardError READ infoCardError NOTIFY infoCardChanged FINAL)
    Q_PROPERTY(bool infoCardBlocked READ infoCardBlocked NOTIFY infoCardChanged FINAL)
    Q_PROPERTY(int groupMemberCount READ groupMemberCount NOTIFY groupMembersChanged FINAL)
    Q_PROPERTY(int groupMembersRevision READ groupMembersRevision NOTIFY groupMembersChanged FINAL)

    // The displayed group conversation's own `group_members` roster, for the
    // composer's `@`-mention picker (the D4a leftover this step's view unblocks).
    // Subscribed alongside the messages window, like `presence` and `pinned`.
    Q_PROPERTY(int chatMembersRevision READ chatMembersRevision NOTIFY chatMembersChanged FINAL)

    // Settings/profile (D6). The object properties expose daemon rows verbatim;
    // commands are ack-only and their effects return through these views.
    Q_PROPERTY(QVariantMap privacySettings READ privacySettings NOTIFY privacySettingsChanged FINAL)
    Q_PROPERTY(QVariantMap appPreferences READ appPreferences NOTIFY appPreferencesChanged FINAL)
    Q_PROPERTY(QAbstractItemModel *blockedContactsModel READ blockedContactsModel CONSTANT FINAL)
    Q_PROPERTY(QVariantMap selfProfile READ selfProfile NOTIFY selfProfileChanged FINAL)
    Q_PROPERTY(QString currentUserName READ currentUserName NOTIFY selfProfileChanged FINAL)
    Q_PROPERTY(QString currentUserAvatarPath READ currentUserAvatarPath NOTIFY selfProfileChanged FINAL)
    Q_PROPERTY(QString currentUserStatusText READ currentUserStatusText NOTIFY selfProfileChanged FINAL)
    Q_PROPERTY(QString currentUserJid READ currentUserJid NOTIFY selfProfileChanged FINAL)

    // Emoji is frontend presentation state (recents/search/skin-tone helpers),
    // not daemon state. Stickers are the protocol-backed D6 picker surface.
    Q_PROPERTY(QAbstractItemModel *emojiModel READ emojiModel CONSTANT FINAL)
    Q_PROPERTY(QObject *stickers READ stickers CONSTANT FINAL)

    // WHATKEVR_PERF=1 — frame/navigation timing diagnostics in Main.qml.
    Q_PROPERTY(bool perfLogging READ perfLogging CONSTANT FINAL)

public:
    static void setInstance(ProtocolController *instance);
    static ProtocolController *create(QQmlEngine *qmlEngine, QJSEngine *jsEngine);

    // `parent` is deliberately not defaulted: QQmlPrivate picks the singleton's
    // construction mode at compile time and prefers a default constructor over
    // create(), so a default argument here would make the QML engine build its
    // own second controller — one main() never start()s, leaving the shell stuck
    // on the splash forever.
    explicit ProtocolController(QObject *parent);
    // Test seam: connect to an explicit socket path instead of the XDG default.
    ProtocolController(QString socketPath, QObject *parent);
    ~ProtocolController() override;

    // Begin connecting to the daemon and subscribe the connection/login views.
    // Idempotent; called once from main() after the event loop is up.
    void start();

    // The shared socket to the daemon, so later D-steps subscribe more views on
    // the same connection. Never null after construction.
    [[nodiscard]] whatevr::proto::ProtocolClient *client() const { return m_client; }

    [[nodiscard]] bool starting() const;
    [[nodiscard]] bool loginRequired() const;
    [[nodiscard]] bool shellVisible() const;
    [[nodiscard]] QString connectionPhase() const;
    [[nodiscard]] bool daemonRunning() const;
    [[nodiscard]] bool loading() const;
    [[nodiscard]] QString statusTitle() const;
    [[nodiscard]] QString statusText() const;
    [[nodiscard]] QString detailText() const;
    [[nodiscard]] QString bannerText() const;
    [[nodiscard]] QString actionError() const;
    [[nodiscard]] QString primaryActionText() const;
    [[nodiscard]] bool primaryActionEnabled() const;
    [[nodiscard]] QString daemonServiceCommand() const;
    [[nodiscard]] QString daemonBinaryCommand() const;
    [[nodiscard]] QString daemonInstructions() const;
    [[nodiscard]] bool qrAvailable() const;
    [[nodiscard]] QString qrCode() const;
    [[nodiscard]] QString qrExpiryText() const;

    [[nodiscard]] QAbstractItemModel *chatsModel() const;
    [[nodiscard]] int chatFilter() const { return m_chatFilter; }
    void setChatFilter(int filter);
    [[nodiscard]] bool chatsLoading() const;
    [[nodiscard]] bool chatsEmpty() const;
    [[nodiscard]] bool chatsExhausted() const;
    Q_INVOKABLE void loadMoreChats();
    void ensureSelectedChatLoaded();

    [[nodiscard]] QAbstractItemModel *archivedChatsModel() const;
    [[nodiscard]] int archivedCount() const;
    [[nodiscard]] bool archivedExhausted() const;
    Q_INVOKABLE void loadMoreArchivedChats();

    [[nodiscard]] int typingRevision() const { return m_typingRevision; }
    [[nodiscard]] Q_INVOKABLE bool chatTyping(const QString &chatId) const;

    [[nodiscard]] bool historySyncVisible() const { return m_historySyncVisible; }
    [[nodiscard]] int historySyncPercent() const { return m_historySyncPercent; }
    [[nodiscard]] QString historySyncTitle() const { return m_historySyncTitle; }
    [[nodiscard]] QString historySyncDetail() const { return m_historySyncDetail; }

    [[nodiscard]] QAbstractItemModel *messageListModel() const;
    [[nodiscard]] QString selectedChatId() const { return m_selectedChatId; }
    [[nodiscard]] QString selectedChatName() const;
    [[nodiscard]] QString selectedChatAvatarLocalPath() const;
    [[nodiscard]] bool hasSelectedChat() const { return !m_selectedChatId.isEmpty(); }
    [[nodiscard]] int selectedChatUnreadCount() const;
    [[nodiscard]] bool selectedChatHistoryExhausted() const;
    [[nodiscard]] bool messagesLoading() const;
    [[nodiscard]] bool olderMessagesLoading() const { return m_olderMessagesLoading; }
    [[nodiscard]] bool newerMessagesLoading() const { return m_newerMessagesLoading; }
    [[nodiscard]] bool canLoadOlderMessages() const { return m_canLoadOlderMessages && !m_olderMessagesFailed; }
    [[nodiscard]] bool canLoadNewerMessages() const { return m_canLoadNewerMessages && !m_newerMessagesFailed; }
    [[nodiscard]] bool olderMessagesFailed() const { return m_olderMessagesFailed; }
    [[nodiscard]] bool newerMessagesFailed() const { return m_newerMessagesFailed; }
    [[nodiscard]] bool messagesAtLiveEdge() const { return m_messagesAtLiveEdge; }
    [[nodiscard]] bool phoneHistoryRequesting() const { return m_phoneHistoryRequesting; }
    [[nodiscard]] bool messagesEmpty() const;
    [[nodiscard]] QString displayedMessagesChatId() const { return m_displayedMessagesChatId; }
    [[nodiscard]] QString messageErrorText() const { return m_messageErrorText; }
    [[nodiscard]] QString unreadAnchorMessageId() const { return m_unreadAnchorMessageId; }
    [[nodiscard]] int unreadAnchorCount() const { return m_unreadAnchorCount; }
    [[nodiscard]] bool unreadAnchorResolving() const { return m_unreadAnchorResolving; }

    [[nodiscard]] QString selectedChatPresenceText() const;

    [[nodiscard]] bool messageReceiptsLoading() const;
    [[nodiscard]] QString messageReceiptsError() const { return m_receiptsError; }
    [[nodiscard]] bool messageReceiptsIsGroup() const;
    [[nodiscard]] qint64 messageReceiptsSentTimestamp() const;
    [[nodiscard]] int messageReceiptsRevision() const { return m_receiptsRevision; }
    // Every participant row of the open dialog's message, in daemon `sort` order.
    // The dialog splits them into its read/delivered sections; it never reorders.
    [[nodiscard]] Q_INVOKABLE QVariantList messageReceipts() const;
    // A direct chat's single aggregate row (the daemon's sentinel-keyed item), or
    // an empty map before delivery begins.
    [[nodiscard]] Q_INVOKABLE QVariantMap directMessageReceipt() const;

    [[nodiscard]] bool composerEnabled() const;
    [[nodiscard]] bool sendInFlight() const { return m_sendInFlight; }
    [[nodiscard]] QString composerErrorText() const { return m_composerErrorText; }

    [[nodiscard]] bool pinnedMessagesReady() const;
    [[nodiscard]] int pinnedMessagesCount() const;
    // Display fields of one pinned row (`messageId`, `senderName`, `preview`),
    // or an empty map when the index is out of range.
    [[nodiscard]] Q_INVOKABLE QVariantMap pinnedMessageAt(int index) const;

    [[nodiscard]] int forwardTargetsRevision() const { return m_forwardTargetsRevision; }
    // Every candidate forward target whose name matches `query` (empty matches
    // all), in the daemon's `chats` order. Rows are the daemon items verbatim.
    [[nodiscard]] Q_INVOKABLE QVariantList forwardChatTargets(const QString &query) const;
    // Subscribe/drop the picker's own `chats` view; the dialog's lifetime is the
    // subscription's lifetime (same shape as openMessageReceipts).
    Q_INVOKABLE void openForwardTargets();
    Q_INVOKABLE void closeForwardTargets();

    // --- unified search (D5) ---
    [[nodiscard]] QAbstractItemModel *searchResultsModel() const;
    [[nodiscard]] QString searchQuery() const { return m_searchQuery; }
    [[nodiscard]] bool searchActive() const { return !m_searchQuery.trimmed().isEmpty(); }
    [[nodiscard]] bool searchBusy() const { return m_searchBusy; }
    Q_INVOKABLE void setSearchQuery(const QString &query);
    Q_INVOKABLE void clearSearch();

    // --- in-chat search (D5) ---
    [[nodiscard]] bool chatSearchActive() const { return m_chatSearchActive; }
    [[nodiscard]] QString chatSearchQuery() const { return m_chatSearchQuery; }
    [[nodiscard]] int chatSearchMatchCount() const { return static_cast<int>(m_chatSearchMatchIds.size()); }
    // 1-based for display ("3 of 12"); 0 while there is no focused match.
    [[nodiscard]] int chatSearchCurrentIndex() const { return m_chatSearchIndex < 0 ? 0 : m_chatSearchIndex + 1; }
    [[nodiscard]] QString chatSearchActiveMessageId() const;
    Q_INVOKABLE void openChatSearch();
    Q_INVOKABLE void closeChatSearch();
    Q_INVOKABLE void setChatSearchQuery(const QString &query);
    Q_INVOKABLE void chatSearchNext();
    Q_INVOKABLE void chatSearchPrevious();

    // --- starred page (D5) ---
    [[nodiscard]] QAbstractItemModel *starredMessagesModel() const;
    [[nodiscard]] bool starredMessagesLoading() const;
    [[nodiscard]] bool starredMessagesExhausted() const;
    // Subscribe/drop the `starred` view; chatId empty spans every chat. The
    // page's lifetime is the subscription's lifetime.
    Q_INVOKABLE void openStarredMessages(const QString &chatId);
    Q_INVOKABLE void closeStarredMessages();
    Q_INVOKABLE void loadMoreStarredMessages();
    // Display fields (`messageId`, `chatId`, `chatName`, `senderName`,
    // `preview`, `timeText`, `isOutgoing`) derived from one daemon message-row
    // item. A pure function of its argument, so a delegate can call it on the
    // row it already holds and it re-evaluates when that row upserts.
    [[nodiscard]] Q_INVOKABLE QVariantMap messageRowDisplay(const QVariantMap &item) const;

    // --- contact / group info card (D5) ---
    [[nodiscard]] QString infoCardKind() const { return m_infoCardKind; }
    // The jid (contact) or chat_id (group) the open card is showing.
    [[nodiscard]] QString infoCardSubject() const { return m_infoCardSubject; }
    [[nodiscard]] QVariantMap infoCard() const;
    [[nodiscard]] bool infoCardLoading() const;
    [[nodiscard]] QString infoCardError() const { return m_infoCardError; }
    [[nodiscard]] bool infoCardBlocked() const;
    [[nodiscard]] int groupMemberCount() const;
    [[nodiscard]] int groupMembersRevision() const { return m_groupMembersRevision; }
    // Members whose name or phone matches `query` (empty matches all), in the
    // daemon's roster order — PROTOCOL.md names member search as
    // presentation-side filtering over rows the frontend already has.
    [[nodiscard]] Q_INVOKABLE QVariantList groupMembers(const QString &query) const;
    [[nodiscard]] int chatMembersRevision() const { return m_chatMembersRevision; }
    // The same filtering over the conversation's roster, for the mention picker.
    [[nodiscard]] Q_INVOKABLE QVariantList chatMembers(const QString &query) const;
    Q_INVOKABLE void openContactCard(const QString &jid);
    Q_INVOKABLE void openGroupCard(const QString &chatId);
    Q_INVOKABLE void closeInfoCard();
    Q_INVOKABLE void setContactBlocked(const QString &jid, bool blocked);
    // `media.fetch_profile_picture`; the full-resolution path comes back through
    // profilePictureReady for the avatar viewer.
    Q_INVOKABLE void viewProfilePicture(const QString &jid);
    // `chat.ensure_direct`, then select the (possibly brand-new) chat and ask
    // the shell to surface it — the row itself appears in the `chats` view.
    Q_INVOKABLE void startDirectChat(const QString &jid);

    // --- settings / profile / emoji / stickers (D6) ---
    [[nodiscard]] QVariantMap privacySettings() const;
    [[nodiscard]] QVariantMap appPreferences() const;
    [[nodiscard]] QAbstractItemModel *blockedContactsModel() const;
    [[nodiscard]] QVariantMap selfProfile() const;
    [[nodiscard]] QString currentUserName() const;
    [[nodiscard]] QString currentUserAvatarPath() const;
    [[nodiscard]] QString currentUserStatusText() const;
    [[nodiscard]] QString currentUserJid() const;
    [[nodiscard]] QAbstractItemModel *emojiModel() const;
    [[nodiscard]] QObject *stickers() const;
    Q_INVOKABLE void openPrivacySettings();
    Q_INVOKABLE void closePrivacySettings();
    Q_INVOKABLE void openBlockedContacts();
    Q_INVOKABLE void closeBlockedContacts();
    Q_INVOKABLE void setPrivacyAudience(const QString &category, const QString &value);
    Q_INVOKABLE void setReadReceipts(bool enabled);
    Q_INVOKABLE void setAppPreference(const QString &key, bool value);
    Q_INVOKABLE void setProfileStatus(const QString &text);
    Q_INVOKABLE void logout();

    // Composer send paths (D4a): map straight to `send.text`/`send.media`; the
    // daemon acks with an id only, the rendered message arrives via the
    // `messages` view. mentionedJids/replyToMessageId/caption may be empty.
    Q_INVOKABLE void sendText(const QString &text, const QString &replyToMessageId, const QStringList &mentionedJids);
    Q_INVOKABLE void sendMedia(const QString &fileUrl, const QString &caption, const QString &replyToMessageId);
    // Sends whatever image the clipboard currently holds (pasted bitmap or a
    // local image file URL), same as sendMedia. Returns false when the
    // clipboard had nothing sendable, so the caller can fall back to a normal
    // paste-as-text.
    Q_INVOKABLE bool sendClipboardImage(const QString &caption, const QString &replyToMessageId);
    // Maps to `chat.typing`; the composer calls this on every start/stop and
    // periodically while composing (WhatsApp's composing indicator has a TTL).
    Q_INVOKABLE void setSelectedChatComposing(bool composing);

    // Message actions (D4b): the `message.*` commands. Every one of them acks
    // with ids/errors only — the reaction pill, star, pin, edited body and
    // revoke tombstone all arrive back as ordinary `messages`/`pinned` view
    // upserts (rule 2), so nothing is applied locally and there is no
    // optimistic update to roll back on failure.
    // Media download (D4c): `media.download` acks immediately and the daemon
    // runs the transfer in the background. Live progress arrives through the
    // global `transfers` view (composed into the timeline's download roles),
    // the finished file as a `messages` upsert carrying `media.path`, and a
    // failure as the same row's durable `media.download_error` — so this call
    // keeps no per-message state and there is nothing to roll back.
    Q_INVOKABLE void downloadMessageMedia(const QString &messageId);

    Q_INVOKABLE void sendReaction(const QString &messageId, const QString &emoji);
    Q_INVOKABLE void editMessage(const QString &messageId, const QString &newText);
    Q_INVOKABLE void revokeMessage(const QString &messageId);
    Q_INVOKABLE void deleteMessageForMe(const QString &messageId);
    Q_INVOKABLE void setMessageStarred(const QString &messageId, bool starred);
    Q_INVOKABLE void pinMessage(const QString &messageId, int durationSecs);
    Q_INVOKABLE void unpinMessage(const QString &messageId);
    // One call per source message; a multi-select forward loops over them and
    // the "forwarded" report fires once for the whole batch.
    Q_INVOKABLE void forwardMessage(const QString &messageId, const QStringList &chatIds);
    // Whether a message sent at this time is still inside WhatsApp's edit
    // window, so the context menu can hide the entry. The daemon is
    // authoritative and answers `expired` regardless.
    [[nodiscard]] Q_INVOKABLE bool canEditAt(qint64 timestampUnix) const;

    Q_INVOKABLE void startDaemon();
    Q_INVOKABLE void triggerPrimaryAction();
    Q_INVOKABLE void copyToClipboard(const QString &text);

    // Chat-list mutations, mapped to the daemon's `chat.*` commands (acks only;
    // the row change lands back through the `chats` view — no local state).
    Q_INVOKABLE void setChatPinned(const QString &chatId, bool pinned);
    Q_INVOKABLE void setChatArchived(const QString &chatId, bool archived);
    Q_INVOKABLE void setChatMuted(const QString &chatId, bool muted, int durationSecs);

    Q_INVOKABLE void selectChat(const QString &chatId);
    Q_INVOKABLE void retryMessages();
    Q_INVOKABLE void loadOlderMessages();
    Q_INVOKABLE void loadNewerMessages();
    Q_INVOKABLE void requestOlderMessagesFromPhone();
    Q_INVOKABLE void jumpToMessage(const QString &messageId);
    Q_INVOKABLE void jumpToBottom();
    Q_INVOKABLE void showMessageInChat(const QString &chatId, const QString &messageId);
    Q_INVOKABLE void markSelectedChatViewed(const QString &upToMessageId);
    Q_INVOKABLE void setConversationVisible(bool visible);

    // Subscribe/drop the `receipts` view for one message: the dialog's lifetime is
    // the subscription's lifetime, which is also what tells the daemon the info
    // dialog is on screen.
    Q_INVOKABLE void openMessageReceipts(const QString &messageId);
    Q_INVOKABLE void closeMessageReceipts();

    // --- frontend-only helpers (D7) ---
    //
    // These touch no daemon surface at all: composer drafts, local file and
    // clipboard operations, and text shaping. Rule 1 leaves presentation state
    // to the frontend, and none of it is renderable daemon data — the daemon
    // has no command behind any of them.

    // Per-chat composer draft, persisted under the "Save unsent drafts"
    // preference (Settings owns the `settings/persistDrafts` key; this reads it
    // the same way EmojiModel reads its own). Empty ids and blank text clear.
    // A draft never reorders a chat row — the daemon owns the `chats` sort.
    Q_INVOKABLE void setChatDraft(const QString &chatId, const QString &text);
    [[nodiscard]] Q_INVOKABLE QString chatDraft(const QString &chatId) const;

    // Local media utilities behind the message context menu. Failures surface
    // through messageActionFailed, like the `message.*` command errors.
    Q_INVOKABLE void copyImageToClipboard(const QString &localPath);
    Q_INVOKABLE bool saveMediaAs(const QString &localPath, const QUrl &destUrl);
    // WhatsApp markup -> CommonMark, for "Copy as Markdown".
    [[nodiscard]] Q_INVOKABLE QString toCommonMark(const QString &text) const;
    // Start of the grapheme cluster before the cursor, so Backspace deletes a
    // whole emoji (ZWJ sequences, skin tones) rather than one code unit.
    [[nodiscard]] Q_INVOKABLE int previousGraphemeBoundary(const QString &text, int cursorPosition) const;

    [[nodiscard]] static bool perfLogging();

    // Single-instance entry point: the launch arguments of this process, or
    // those forwarded by a second launch through KDBusService. A
    // `whatevr://chat/<id>` URL selects that chat once the shell is up;
    // anything else just raises the window.
    void handleCommandLine(const QStringList &arguments);

    // The daemon's protocol socket, `$XDG_RUNTIME_DIR/whatevr/whatevrd.sock`.
    // Empty if XDG_RUNTIME_DIR is unset.
    [[nodiscard]] static QString daemonSocketPath();

Q_SIGNALS:
    void stateChanged();
    void chatFilterChanged();
    void chatsChanged();
    void archivedChanged();
    void typingChanged();
    void historySyncChanged();
    void selectionChanged();
    void messagesChanged();
    void unreadAnchorChanged();
    void presenceChanged();
    void messageReceiptsChanged();
    void composerChanged();
    void pinnedMessagesChanged();
    void forwardTargetsChanged();
    void searchChanged();
    void chatSearchChanged();
    void starredMessagesChanged();
    void infoCardChanged();
    void groupMembersChanged();
    void chatMembersChanged();
    void privacySettingsChanged();
    void appPreferencesChanged();
    void blocklistChanged();
    void selfProfileChanged();
    void settingsActionFailed(const QString &errorText);
    // `media.fetch_profile_picture` outcomes for the avatar viewer.
    void profilePictureReady(const QString &jid, const QString &localPath);
    void profilePictureFailed(const QString &jid, const QString &errorText);
    // A `message.*` command was rejected; the timeline shows the text as a
    // transient notification (the daemon's message, or a generic fallback).
    void messageActionFailed(const QString &errorText);
    // A whole forward batch landed successfully, for the "Forwarded to N chats"
    // notification.
    void messageForwarded(int chatCount);
    void messageJumpReady(const QString &messageId);
    void messageJumpUnavailable(const QString &messageId);
    void openChatRequested(const QString &chatId);
    // Raise and focus the window: a second launch, or a deep link arriving
    // before the chat shell exists.
    void activateWindowRequested();

private:
    // Transport reachability, independent of the daemon-reported WhatsApp state.
    enum class Phase : std::uint8_t {
        Connecting, // socket connecting, or within the cold-start grace
        Connected,  // hello acknowledged; the connection view is authoritative
        NotRunning, // no socket and grace elapsed — the daemon isn't up
    };
    [[nodiscard]] Phase phase() const;
    [[nodiscard]] bool daemonSocketExists() const;

    // The daemon connection state token from the `connection` view
    // (need_login/connecting/online/reconnecting/offline/starting), or empty
    // when the view has not filled.
    [[nodiscard]] QString daemonState() const;
    [[nodiscard]] bool canReconnect() const;

    void onClientReady();
    void onClientDisconnected();
    void onConnectionValueChanged();
    void onLoginValueChanged();
    void refreshQrExpiry();
    void requestReconnect();
    void launchDaemonBinary();

    // Deep links: parse `whatevr://chat/<percent-encoded-id>` and hold the id
    // until the chat shell can show it (a notification click may cold-start the
    // app, so the request can arrive long before login completes).
    void openChatFromUri(const QString &uri);
    void tryApplyPendingDeepLink();

    // Composer drafts, loaded once at construction and rewritten on every
    // change (the map is a handful of short strings).
    void loadPersistedDrafts();
    void savePersistedDrafts() const;

    // (Re)subscribe the `chats` view (active + archived) for the current filter.
    // Clears the models first so a filter switch never briefly shows the old
    // filter's rows.
    void subscribeChats();
    // "all" / "direct" / "groups" for the current m_chatFilter (0/1/2).
    [[nodiscard]] QString chatFilterName() const;

    // Recompute the derived history-sync strip state from the `sync` view item.
    void recomputeHistorySync();

    [[nodiscard]] QVariantMap selectedChatItem() const;
    void setSelectedChat(const QString &chatId, const QString &anchor, const QString &jumpMessageId);
    void subscribeMessages(const QString &anchor, const QString &jumpMessageId = {});
    void onMessagesSubscribed(const QVariantMap &meta);
    void onMessagesReady(bool exhausted);
    void onMessagesFailed(const QString &code, const QString &message);
    void onMessagesReset();
    void extendMessages(const QString &direction, bool force = false);
    void sendSessionUpdate();

    // Clears the unread-anchor divider (a send implies the user has seen
    // everything up to it) — mirrors AppController::dismissUnreadAnchor.
    void dismissUnreadAnchor();

    // (Re)point the `presence` subscription at whatever chat the conversation is
    // currently showing, or drop it when nothing is.
    void updatePresenceSubscription();

    // Same, for the `pinned` banner view: it follows what the conversation is
    // showing, so a hidden conversation holds no pinned subscription.
    void updatePinnedSubscription();

    // Same again, for the composer's mention roster: a `group_members`
    // subscription on the displayed conversation, and only when it is a group.
    void updateChatMembersSubscription();

    // Name/phone filtering over a `group_members` model, keeping the daemon's
    // roster order. Shared by the info dialog and the mention picker.
    [[nodiscard]] static QVariantList filterMemberRows(const whatevr::proto::CollectionViewModel *model,
                                                       const QString &query);

    // Issues a `message.*` command whose only interesting outcome is failure.
    void sendMessageCommand(const QString &method, const QJsonObject &params, const QString &failureText);

    // Fire the unified-search queries for the current query string. A
    // generation counter drops replies to superseded queries — the client
    // always answers, so the guard is on this side.
    void runSearch();
    void runChatSearch();
    void resetChatSearch();
    void updateBlocklistSubscription();
    void sendSettingsCommand(const QString &method, const QJsonObject &params, const QString &failureText);

    QString m_socketPath;
    whatevr::proto::ProtocolClient *m_client = nullptr;
    whatevr::proto::ObjectViewModel *m_connectionModel = nullptr;
    whatevr::proto::ObjectViewModel *m_loginModel = nullptr;
    whatevr::proto::CollectionViewModel *m_chatsModel = nullptr;
    whatevr::proto::CollectionViewModel *m_archivedModel = nullptr;
    whatevr::proto::CollectionViewModel *m_typingModel = nullptr;
    whatevr::proto::ObjectViewModel *m_syncModel = nullptr;
    whatevr::proto::CollectionViewModel *m_messagesModel = nullptr;
    whatevr::proto::CollectionViewModel *m_presenceModel = nullptr;
    whatevr::proto::CollectionViewModel *m_receiptsModel = nullptr;
    whatevr::proto::CollectionViewModel *m_pinnedModel = nullptr;
    whatevr::proto::CollectionViewModel *m_forwardTargetsModel = nullptr;
    whatevr::proto::CollectionViewModel *m_transfersModel = nullptr;
    whatevr::proto::CollectionViewModel *m_starredModel = nullptr;
    whatevr::proto::CollectionViewModel *m_groupMembersModel = nullptr;
    whatevr::proto::CollectionViewModel *m_chatMembersModel = nullptr;
    whatevr::proto::CollectionViewModel *m_blocklistModel = nullptr;
    whatevr::proto::ObjectViewModel *m_infoCardModel = nullptr;
    whatevr::proto::ObjectViewModel *m_privacyModel = nullptr;
    whatevr::proto::ObjectViewModel *m_preferencesModel = nullptr;
    whatevr::proto::ObjectViewModel *m_selfModel = nullptr;
    ProtocolMessageModel *m_messagePresentationModel = nullptr;
    ProtocolSearchModel *m_searchResultsModel = nullptr;
    ProtocolStickerController *m_stickerController = nullptr;
    mutable EmojiModel *m_emojiModel = nullptr;
    whatevr::proto::Subscription *m_connectionSub = nullptr;
    whatevr::proto::Subscription *m_loginSub = nullptr;
    whatevr::proto::Subscription *m_chatsSub = nullptr;
    whatevr::proto::Subscription *m_archivedSub = nullptr;
    whatevr::proto::Subscription *m_typingSub = nullptr;
    whatevr::proto::Subscription *m_syncSub = nullptr;
    whatevr::proto::Subscription *m_messagesSub = nullptr;
    whatevr::proto::Subscription *m_presenceSub = nullptr;
    whatevr::proto::Subscription *m_receiptsSub = nullptr;
    whatevr::proto::Subscription *m_pinnedSub = nullptr;
    whatevr::proto::Subscription *m_forwardTargetsSub = nullptr;
    whatevr::proto::Subscription *m_transfersSub = nullptr;
    whatevr::proto::Subscription *m_starredSub = nullptr;
    whatevr::proto::Subscription *m_infoCardSub = nullptr;
    whatevr::proto::Subscription *m_groupMembersSub = nullptr;
    whatevr::proto::Subscription *m_chatMembersSub = nullptr;
    whatevr::proto::Subscription *m_blocklistSub = nullptr;
    whatevr::proto::Subscription *m_privacySub = nullptr;
    whatevr::proto::Subscription *m_preferencesSub = nullptr;
    whatevr::proto::Subscription *m_selfSub = nullptr;
    int m_chatFilter = 0; // 0 = all, 1 = direct, 2 = groups
    int m_typingRevision = 0;

    // Derived history-sync strip state (see recomputeHistorySync).
    bool m_historySyncVisible = false;
    int m_historySyncPercent = 0;
    QString m_historySyncTitle;
    QString m_historySyncDetail;

    // Chat id a deep link asked for, held until shellVisible() (cleared as soon
    // as it is applied).
    QString m_pendingDeepLinkChatId;
    // chat id -> composer draft text.
    QHash<QString, QString> m_drafts;

    QString m_selectedChatId;
    QString m_displayedMessagesChatId;
    QString m_requestedAnchor;
    QString m_effectiveAnchor;
    QString m_pendingJumpMessageId;
    QString m_jumpFallbackAnchor;
    QString m_pendingExtendDirection;
    QString m_messageErrorText;
    QString m_unreadAnchorMessageId;
    QString m_pendingReadWatermark;
    QString m_phoneHistoryOldestId;
    QString m_lastReadWatermark;
    int m_unreadAnchorCount = 0;
    bool m_unreadAnchorResolving = false;
    bool m_waitingInitialMessages = false;
    bool m_refillingAfterReset = false;
    // One chat-list `extend` in flight at a time; cleared by the view's `ready`
    // (or by a rejected extend) so scrolling cannot pile requests up.
    bool m_chatsExtendPending = false;
    bool m_archivedExtendPending = false;
    bool m_olderMessagesLoading = false;
    bool m_newerMessagesLoading = false;
    bool m_canLoadOlderMessages = false;
    bool m_canLoadNewerMessages = false;
    bool m_olderMessagesFailed = false;
    bool m_newerMessagesFailed = false;
    bool m_messagesAtLiveEdge = false;
    bool m_phoneHistoryRequesting = false;
    bool m_conversationVisible = false;
    int m_messagesGeneration = 0;
    int m_phoneHistoryGeneration = 0;

    // The chat the `presence` subscription currently covers (empty when none).
    QString m_presenceChatId;
    // The chat the `pinned` subscription currently covers (empty when none).
    QString m_pinnedChatId;
    // The message the open info dialog is watching (empty when it is closed).
    QString m_receiptsMessageId;
    QString m_receiptsError;
    int m_receiptsRevision = 0;

    bool m_clientReady = false;
    bool m_startupGrace = true;
    bool m_reconnectInFlight = false;
    QString m_bannerText;
    QString m_actionError;

    // Composer send state (D4a).
    bool m_sendInFlight = false;
    QString m_composerErrorText;
    // The chat a local "composing" was last sent true for, so a stop is only
    // sent to the chat that actually owns the composing state (mirrors
    // AppController::m_localComposingChatId).
    QString m_localComposingChatId;

    // Forward-batch bookkeeping (D4b). A multi-select forward dispatches one
    // `message.forward` per source message in a synchronous loop, so the batch
    // starts when the in-flight count goes 0 -> 1 and reports once when it
    // drains — one "Forwarded to N chats" for the whole selection.
    int m_forwardTargetsRevision = 0;
    int m_forwardInFlight = 0;
    int m_forwardBatchChatCount = 0;
    bool m_forwardBatchFailed = false;

    // Unified search (D5). The generation counter is bumped per query so a
    // late reply to a superseded query is dropped instead of overwriting the
    // model; `m_searchPending` counts the two half-queries still in flight.
    QString m_searchQuery;
    bool m_searchBusy = false;
    int m_searchGeneration = 0;
    int m_searchPending = 0;

    // In-chat search (D5). The match list is a query result (transient); the
    // cursor into it is presentation state.
    bool m_chatSearchActive = false;
    QString m_chatSearchQuery;
    QStringList m_chatSearchMatchIds;
    int m_chatSearchIndex = -1;
    int m_chatSearchGeneration = 0;

    // The chat the open starred page is scoped to, "" for every chat; only
    // meaningful while m_starredSub is live.
    QString m_starredChatId;

    // Info card (D5): "contact" or "group" while a card is open, else empty.
    QString m_infoCardKind;
    QString m_infoCardSubject;
    QString m_infoCardError;
    int m_groupMembersRevision = 0;
    bool m_privacyPageOpen = false;
    bool m_blocklistPageOpen = false;

    // The chat the conversation-scoped `group_members` roster covers (empty
    // when the conversation is hidden or is not a group).
    QString m_chatMembersChatId;
    int m_chatMembersRevision = 0;

    QTimer *m_startupGraceTimer = nullptr;
    QTimer *m_qrTimer = nullptr;
    QTimer *m_readTimer = nullptr;
    QTimer *m_phoneHistoryTimer = nullptr;
    QTimer *m_phoneHistorySettleTimer = nullptr;
    QTimer *m_searchDebounceTimer = nullptr;
    QTimer *m_chatSearchDebounceTimer = nullptr;
};
