#pragma once

#include <QAbstractItemModel>
#include <QObject>
#include <QString>
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

// The whatevr-protocol counterpart of AppController's connection lifecycle: it
// owns the ProtocolClient (the single socket to the daemon's PROTOCOL.md
// surface) and subscribes to the `connection` and `login` object views,
// deriving from them every string the status/login/splash pages bind to. During
// the D-phase migration it runs *alongside* the still-gRPC AppController — this
// singleton drives the pre-shell screens and the shell-visibility gate, while
// AppController keeps serving the not-yet-ported chat shell over gRPC until D7.
//
// Ported pages bind `Whatevr.ProtocolController.<prop>` exactly as they used to
// bind AppController; each later D-step moves one more page's bindings across
// and subscribes its views through client() on this same connection.
class ProtocolController final : public QObject
{
    Q_OBJECT
    QML_NAMED_ELEMENT(ProtocolController)
    QML_SINGLETON

    // Shell routing (mirrors AppController's gate, now protocol-driven).
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

    // Archived chats (D2b2): a second `chats` subscription (`archived: true`) for
    // the collapsible archived section; honours the same filter as the main list.
    Q_PROPERTY(QAbstractItemModel *archivedChatsModel READ archivedChatsModel CONSTANT FINAL)
    Q_PROPERTY(int archivedCount READ archivedCount NOTIFY archivedChanged FINAL)

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

public:
    static void setInstance(ProtocolController *instance);
    static ProtocolController *create(QQmlEngine *qmlEngine, QJSEngine *jsEngine);

    explicit ProtocolController(QObject *parent = nullptr);
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

    [[nodiscard]] QAbstractItemModel *archivedChatsModel() const;
    [[nodiscard]] int archivedCount() const;

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

    // The daemon's protocol socket, `$XDG_RUNTIME_DIR/whatevr/whatevrd.sock`
    // (distinct from the gRPC socket under whatevrd/). Empty if XDG_RUNTIME_DIR
    // is unset.
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
    void messageJumpReady(const QString &messageId);
    void messageJumpUnavailable(const QString &messageId);
    void openChatRequested(const QString &chatId);

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

    QString m_socketPath;
    whatevr::proto::ProtocolClient *m_client = nullptr;
    whatevr::proto::ObjectViewModel *m_connectionModel = nullptr;
    whatevr::proto::ObjectViewModel *m_loginModel = nullptr;
    whatevr::proto::CollectionViewModel *m_chatsModel = nullptr;
    whatevr::proto::CollectionViewModel *m_archivedModel = nullptr;
    whatevr::proto::CollectionViewModel *m_typingModel = nullptr;
    whatevr::proto::ObjectViewModel *m_syncModel = nullptr;
    whatevr::proto::CollectionViewModel *m_messagesModel = nullptr;
    ProtocolMessageModel *m_messagePresentationModel = nullptr;
    whatevr::proto::Subscription *m_connectionSub = nullptr;
    whatevr::proto::Subscription *m_loginSub = nullptr;
    whatevr::proto::Subscription *m_chatsSub = nullptr;
    whatevr::proto::Subscription *m_archivedSub = nullptr;
    whatevr::proto::Subscription *m_typingSub = nullptr;
    whatevr::proto::Subscription *m_syncSub = nullptr;
    whatevr::proto::Subscription *m_messagesSub = nullptr;
    int m_chatFilter = 0; // 0 = all, 1 = direct, 2 = groups
    int m_typingRevision = 0;

    // Derived history-sync strip state (see recomputeHistorySync).
    bool m_historySyncVisible = false;
    int m_historySyncPercent = 0;
    QString m_historySyncTitle;
    QString m_historySyncDetail;

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

    bool m_clientReady = false;
    bool m_startupGrace = true;
    bool m_reconnectInFlight = false;
    QString m_bannerText;
    QString m_actionError;

    QTimer *m_startupGraceTimer = nullptr;
    QTimer *m_qrTimer = nullptr;
    QTimer *m_readTimer = nullptr;
    QTimer *m_phoneHistoryTimer = nullptr;
    QTimer *m_phoneHistorySettleTimer = nullptr;
};
