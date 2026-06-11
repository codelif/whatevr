#pragma once

#include <QAbstractItemModel>
#include <QHash>
#include <QList>
#include <QObject>
#include <QSet>
#include <QString>
#include <QStringList>
#include <QUrl>
#include <QVariantMap>
#include <qqmlintegration.h>

#include <cstdint>
#include <memory>

#include <QtGrpc/qtgrpcnamespace.h>

#include "whatevr/v1/whatevr.qpb.h"

QT_BEGIN_NAMESPACE
class QGrpcCallReply;
class QGrpcServerStream;
class QTimer;
class QAbstractGrpcChannel;
class QFileSystemWatcher;
class QLocalSocket;
class QQmlEngine;
class QJSEngine;
QT_END_NAMESPACE

namespace whatevr::v1 {
class GetStatusResponse;
class ConnectionChanged;
class LoginStateChanged;
class LoginEvent;
class ChatUpdated;
class AvatarUpdated;
class ChatPresenceChanged;
class MediaDownloadChanged;
class HistorySyncProgress;
class HistoryBackfilled;
class Message;
namespace DaemonService {
class Client;
}
namespace LoginService {
class Client;
}
namespace FrontendService {
class Client;
}
namespace ChatService {
class Client;
}
namespace SendService {
class Client;
}
namespace DaemonStateGadget {
enum class DaemonState : int32_t;
}
}

class ChatListModel;
class EmojiModel;
class MessageListModel;
class StickerController;

class AppController final : public QObject
{
    Q_OBJECT
    QML_NAMED_ELEMENT(AppController)
    QML_SINGLETON
    Q_PROPERTY(QString applicationId READ applicationId CONSTANT FINAL)
    Q_PROPERTY(QString applicationDisplayName READ applicationDisplayName CONSTANT FINAL)
    Q_PROPERTY(QString executableName READ executableName CONSTANT FINAL)
    Q_PROPERTY(QString daemonSocketPath READ daemonSocketPath CONSTANT FINAL)
    Q_PROPERTY(QString daemonSocketUrl READ daemonSocketUrl CONSTANT FINAL)
    Q_PROPERTY(bool loading READ loading NOTIFY stateChanged FINAL)
    Q_PROPERTY(bool daemonRunning READ daemonRunning NOTIFY stateChanged FINAL)
    Q_PROPERTY(QString daemonInstructions READ daemonInstructions CONSTANT FINAL)
    Q_PROPERTY(QString daemonServiceCommand READ daemonServiceCommand CONSTANT FINAL)
    Q_PROPERTY(QString daemonBinaryCommand READ daemonBinaryCommand CONSTANT FINAL)
    Q_PROPERTY(QString actionError READ actionError NOTIFY stateChanged FINAL)
    Q_PROPERTY(QString connectionPhase READ connectionPhase NOTIFY stateChanged FINAL)
    Q_PROPERTY(bool loginRequired READ loginRequired NOTIFY stateChanged FINAL)
    Q_PROPERTY(bool shellVisible READ shellVisible NOTIFY stateChanged FINAL)
    Q_PROPERTY(bool qrAvailable READ qrAvailable NOTIFY stateChanged FINAL)
    Q_PROPERTY(QString statusTitle READ statusTitle NOTIFY stateChanged FINAL)
    Q_PROPERTY(QString statusText READ statusText NOTIFY stateChanged FINAL)
    Q_PROPERTY(QString detailText READ detailText NOTIFY stateChanged FINAL)
    Q_PROPERTY(QString bannerText READ bannerText NOTIFY stateChanged FINAL)
    Q_PROPERTY(QString qrCode READ qrCode NOTIFY stateChanged FINAL)
    Q_PROPERTY(QString qrExpiryText READ qrExpiryText NOTIFY stateChanged FINAL)
    Q_PROPERTY(QString primaryActionText READ primaryActionText NOTIFY stateChanged FINAL)
    Q_PROPERTY(bool primaryActionEnabled READ primaryActionEnabled NOTIFY stateChanged FINAL)
    Q_PROPERTY(QAbstractItemModel *chatListModel READ chatListModel CONSTANT FINAL)
    Q_PROPERTY(bool chatsLoading READ chatsLoading NOTIFY chatsChanged FINAL)
    Q_PROPERTY(bool chatsEmpty READ chatsEmpty NOTIFY chatsChanged FINAL)
    Q_PROPERTY(QAbstractItemModel *messageListModel READ messageListModel CONSTANT FINAL)
    Q_PROPERTY(QAbstractItemModel *emojiModel READ emojiModel CONSTANT FINAL)
    Q_PROPERTY(QObject *stickers READ stickers CONSTANT FINAL)
    Q_PROPERTY(bool messagesLoading READ messagesLoading NOTIFY messagesChanged FINAL)
    Q_PROPERTY(bool olderMessagesLoading READ olderMessagesLoading NOTIFY messagesChanged FINAL)
    Q_PROPERTY(bool canLoadOlderMessages READ canLoadOlderMessages NOTIFY messagesChanged FINAL)
    Q_PROPERTY(bool messagesEmpty READ messagesEmpty NOTIFY messagesChanged FINAL)
    Q_PROPERTY(QString displayedMessagesChatId READ displayedMessagesChatId NOTIFY messagesChanged FINAL)
    Q_PROPERTY(QString messageErrorText READ messageErrorText NOTIFY messagesChanged FINAL)
    Q_PROPERTY(bool composerEnabled READ composerEnabled NOTIFY composerChanged FINAL)
    Q_PROPERTY(bool sendInFlight READ sendInFlight NOTIFY composerChanged FINAL)
    Q_PROPERTY(QString composerErrorText READ composerErrorText NOTIFY composerChanged FINAL)
    Q_PROPERTY(QString selectedChatId READ selectedChatId NOTIFY selectionChanged FINAL)
    Q_PROPERTY(QString selectedChatName READ selectedChatName NOTIFY selectionChanged FINAL)
    Q_PROPERTY(QString selectedChatAvatarLocalPath READ selectedChatAvatarLocalPath NOTIFY selectionChanged FINAL)
    Q_PROPERTY(QString selectedChatPresenceText READ selectedChatPresenceText NOTIFY selectionChanged FINAL)
    Q_PROPERTY(bool hasSelectedChat READ hasSelectedChat NOTIFY selectionChanged FINAL)
    Q_PROPERTY(bool historySyncVisible READ historySyncVisible NOTIFY historySyncChanged FINAL)
    Q_PROPERTY(int historySyncPercent READ historySyncPercent NOTIFY historySyncChanged FINAL)
    Q_PROPERTY(QString historySyncTitle READ historySyncTitle NOTIFY historySyncChanged FINAL)
    Q_PROPERTY(QString historySyncDetail READ historySyncDetail NOTIFY historySyncChanged FINAL)

public:
    static void setInstance(AppController *instance);
    static AppController *create(QQmlEngine *qmlEngine, QJSEngine *jsEngine);

    explicit AppController(QObject *parent = nullptr);
    ~AppController() override;

    [[nodiscard]] QString applicationId() const;
    [[nodiscard]] QString applicationDisplayName() const;
    [[nodiscard]] QString executableName() const;
    [[nodiscard]] QString daemonSocketPath() const;
    [[nodiscard]] QString daemonSocketUrl() const;
    [[nodiscard]] bool loading() const;
    [[nodiscard]] bool daemonRunning() const;
    [[nodiscard]] QString daemonInstructions() const;
    [[nodiscard]] QString daemonServiceCommand() const;
    [[nodiscard]] QString daemonBinaryCommand() const;
    [[nodiscard]] QString actionError() const;
    [[nodiscard]] QString connectionPhase() const;
    [[nodiscard]] bool loginRequired() const;
    [[nodiscard]] bool shellVisible() const;
    [[nodiscard]] bool qrAvailable() const;
    [[nodiscard]] QString statusTitle() const;
    [[nodiscard]] QString statusText() const;
    [[nodiscard]] QString detailText() const;
    [[nodiscard]] QString bannerText() const;
    [[nodiscard]] QString qrCode() const;
    [[nodiscard]] QString qrExpiryText() const;
    [[nodiscard]] QString primaryActionText() const;
    [[nodiscard]] bool primaryActionEnabled() const;
    [[nodiscard]] QAbstractItemModel *chatListModel() const;
    [[nodiscard]] bool chatsLoading() const;
    [[nodiscard]] bool chatsEmpty() const;
    [[nodiscard]] QAbstractItemModel *messageListModel() const;
    [[nodiscard]] QAbstractItemModel *emojiModel() const;
    [[nodiscard]] QObject *stickers() const;
    [[nodiscard]] bool messagesLoading() const;
    [[nodiscard]] bool olderMessagesLoading() const;
    [[nodiscard]] bool canLoadOlderMessages() const;
    [[nodiscard]] bool messagesEmpty() const;
    [[nodiscard]] QString displayedMessagesChatId() const;
    [[nodiscard]] QString messageErrorText() const;
    [[nodiscard]] bool composerEnabled() const;
    [[nodiscard]] bool sendInFlight() const;
    [[nodiscard]] QString composerErrorText() const;
    [[nodiscard]] QString selectedChatId() const;
    [[nodiscard]] QString selectedChatName() const;
    [[nodiscard]] QString selectedChatAvatarLocalPath() const;
    [[nodiscard]] QString selectedChatPresenceText() const;
    [[nodiscard]] bool hasSelectedChat() const;
    [[nodiscard]] bool historySyncVisible() const;
    [[nodiscard]] int historySyncPercent() const;
    [[nodiscard]] QString historySyncTitle() const;
    [[nodiscard]] QString historySyncDetail() const;

    Q_INVOKABLE void refresh();
    Q_INVOKABLE void startDaemon();
    Q_INVOKABLE void copyToClipboard(const QString &text);
    Q_INVOKABLE void triggerPrimaryAction();
    Q_INVOKABLE void selectChat(const QString &chatId);
    Q_INVOKABLE void retryMessages();
    Q_INVOKABLE void loadOlderMessages();
    Q_INVOKABLE void jumpToMessage(const QString &messageId);
    Q_INVOKABLE void sendText(const QString &text, const QString &replyToMessageId = QString());
    Q_INVOKABLE void sendImage(const QString &fileUrl, const QString &caption = QString(), const QString &replyToMessageId = QString());
    Q_INVOKABLE void addRecentEmoji(const QString &emoji);
    Q_INVOKABLE [[nodiscard]] int previousGraphemeBoundary(const QString &text, int cursorPosition) const;
    Q_INVOKABLE bool sendClipboardImage(const QString &caption = QString(), const QString &replyToMessageId = QString());
    Q_INVOKABLE void setChatPinned(const QString &chatId, bool pinned);
    Q_INVOKABLE void setSelectedChatComposing(bool composing);
    Q_INVOKABLE void downloadMessageMedia(const QString &messageId);
    Q_INVOKABLE [[nodiscard]] bool isMessageMediaDownloading(const QString &messageId) const;
    Q_INVOKABLE void copyImageToClipboard(const QString &localPath);
    Q_INVOKABLE bool saveMediaAs(const QString &localPath, const QUrl &destUrl);
    Q_INVOKABLE [[nodiscard]] QString toCommonMark(const QString &text) const;
    Q_INVOKABLE void deleteMessageForMe(const QString &messageId);
    Q_INVOKABLE void revokeMessage(const QString &messageId);
    Q_INVOKABLE void forwardMessage(const QString &messageId, const QStringList &chatIds);
    Q_INVOKABLE void requestMessageInfo(const QString &messageId);
    Q_INVOKABLE void logout();

    // Entry points for single-instance activation / deep links. handleCommandLine
    // is fed the argv of this or a forwarded secondary instance (KDBusService);
    // it raises the window and, if a whatevr:// URL is present, opens that chat.
    void handleCommandLine(const QStringList &arguments);
    void openChatFromUri(const QString &uri);

Q_SIGNALS:
    void stateChanged();
    void activateWindowRequested();
    void openChatRequested(const QString &chatId);
    void chatsChanged();
    void messagesChanged();
    void composerChanged();
    void selectionChanged();
    void historySyncChanged();
    void outgoingMessageAddedToSelectedChat();
    void messageJumpReady(const QString &messageId);
    void messageJumpUnavailable(const QString &messageId);
    void mediaDownloadChanged(const QString &messageId);
    void mediaDownloadFailed(const QString &messageId, const QString &errorText);
    // info: status, sentTsUnix, deliveredTsUnix, readTsUnix, isGroup, and
    // receipts (list of maps with jid/displayName/avatarLocalPath/timestamps).
    void messageInfoReceived(const QString &messageId, const QVariantMap &info);
    // Emitted when a message-info request could not be fulfilled, so the dialog
    // can stop waiting and surface the reason instead of spinning forever.
    void messageInfoFailed(const QString &messageId, const QString &errorText);
    void messageActionFailed(const QString &errorText);
    void messageForwarded(int chatCount);

private:
    void bootstrap();
    void launchDaemonBinary();
    bool ensureChannel();
    void resetChannel();
    void probeAndConnect();
    void startSession();
    void enterNotRunning();
    void setupSocketWatcher();
    void refreshSocketWatch();
    void attachClients();
    void requestStatus();
    void requestReconnect();
    void requestChats();
    void requestMessages(const QString &chatId);
    void requestOlderMessages();
    void requestSelectedChatReadIfActive();
    void requestSelectedChatPresence();
    void setChatComposing(const QString &chatId, bool composing);
    void ensureFrontendSession();
    void updateFrontendSessionState();
    void sendFrontendSessionState();
    void sendSelectedChatReadIfActive();
    void ensureDaemonStream();
    void ensureLoginStream();
    void scheduleRetry(int delayMs = 2000);
    void handleTransportFailure(const QString &context, const QString &message, QtGrpc::StatusCode code = QtGrpc::StatusCode::Unknown);
    [[nodiscard]] bool daemonSocketExists() const;
    void applyStatusResponse(const whatevr::v1::GetStatusResponse &status);
    void applyConnectionChanged(const whatevr::v1::ConnectionChanged &change);
    void applyLoginStateChanged(const whatevr::v1::LoginStateChanged &change);
    void applyLoginEvent(const whatevr::v1::LoginEvent &event);
    void applyChatUpdated(const whatevr::v1::ChatUpdated &update);
    void applyAvatarUpdated(const whatevr::v1::AvatarUpdated &update);
    void applyChatPresenceChanged(const whatevr::v1::ChatPresenceChanged &presence);
    void applyMediaDownloadChanged(const whatevr::v1::MediaDownloadChanged &download);
    void applyMessageEvent(const whatevr::v1::Message &message);
    void applyMessageDeleted(const QString &chatId, const QString &messageId);
    void applyHistorySyncProgress(const whatevr::v1::HistorySyncProgress &progress);
    void applyHistoryBackfilled(const whatevr::v1::HistoryBackfilled &backfilled);
    [[nodiscard]] bool shouldDisplayHistorySyncProgress(const whatevr::v1::HistorySyncProgress &progress) const;
    [[nodiscard]] bool shouldCompleteHistorySyncDisplay(const whatevr::v1::HistorySyncProgress &progress) const;
    bool resetHistorySyncDisplay(int percent = 0);
    void updateQrExpiryText();
    void clearLoginState();
    void clearBanner();
    void emitStateChanged();
    void updateSelectedChatData();
    void cacheMessages(const QString &chatId, const QList<whatevr::v1::Message> &messages, bool canLoadOlderMessages);
    bool restoreCachedMessages(const QString &chatId);
    void scheduleSelectedChatMessageReload(const QString &chatId);
    void tryApplyPendingDeepLink();

    // High-level connection lifecycle. Drives every status string the GUI
    // shows and replaces the old (m_loading && !m_hasStatus) ad-hoc logic.
    enum class Phase {
        Connecting, // a socket is present (or assumed) and we are talking to it
        Connected,  // the daemon answered; daemon state drives the rest
        NotRunning, // no socket / gRPC Unavailable — daemon isn't up
        Error,      // reachable but a call failed for another reason
    };
    Phase m_phase = Phase::Connecting;
    bool m_loginRequired = false;
    bool m_canReconnect = false;
    bool m_hasStatus = false;
    bool m_reconnectInFlight = false;
    QString m_daemonStateLabel;
    QString m_statusDetail;
    QString m_bannerText;
    // A sticky, action-triggered error (e.g. "Start whatevrd" couldn't launch
    // anything). Separate from the transient m_bannerText so the NotRunning
    // retry tick (clearBanner) can't wipe it.
    QString m_actionError;
    QString m_qrCode;
    QString m_qrExpiryText;
    qint64 m_qrExpiresAtUnix = 0;
    bool m_chatsLoading = false;
    bool m_messagesLoading = false;
    bool m_olderMessagesLoading = false;
    bool m_canLoadOlderMessages = false;
    QString m_messagesLoadingChatId;
    QString m_olderMessagesLoadingChatId;
    QString m_jumpToMessageChatId;
    QString m_jumpToMessageId;
    QString m_displayedMessagesChatId;
    QString m_messageErrorText;
    bool m_sendInFlight = false;
    QString m_composerErrorText;
    QString m_selectedChatId;
    QString m_selectedChatName;
    QString m_selectedChatAvatarLocalPath;
    bool m_selectedChatIsGroup = false;
    QString m_frontendSessionId;
    bool m_selectedChatComposing = false;
    int m_selectedChatAvailability = 0;
    qint64 m_selectedChatLastSeenUnix = 0;
    bool m_historySyncVisible = false;
    int m_historySyncPercent = 0;
    QString m_historySyncTitle;
    QString m_historySyncDetail;
    bool m_historySyncCursorActive = false;
    whatevr::v1::HistorySyncTypeGadget::HistorySyncType m_historySyncCursorSyncType =
        whatevr::v1::HistorySyncTypeGadget::HistorySyncType::HISTORY_SYNC_TYPE_UNSPECIFIED;
    std::uint32_t m_historySyncCursorChunkOrder = 0;
    whatevr::v1::HistorySyncPhaseGadget::HistorySyncPhase m_historySyncCursorPhase =
        whatevr::v1::HistorySyncPhaseGadget::HistorySyncPhase::HISTORY_SYNC_PHASE_UNSPECIFIED;

    struct CachedMessages {
        QList<whatevr::v1::Message> messages;
        bool canLoadOlderMessages = false;
    };
    QHash<QString, CachedMessages> m_messageCache;
    QStringList m_messageCacheOrder;

    ChatListModel *m_chatListModel = nullptr;
    EmojiModel *m_emojiModel = nullptr;
    MessageListModel *m_messageListModel = nullptr;
    StickerController *m_stickerController = nullptr;

    std::shared_ptr<QAbstractGrpcChannel> m_channel;
    std::unique_ptr<whatevr::v1::DaemonService::Client> m_daemonClient;
    std::unique_ptr<whatevr::v1::LoginService::Client> m_loginClient;
    std::unique_ptr<whatevr::v1::FrontendService::Client> m_frontendClient;
    std::unique_ptr<whatevr::v1::ChatService::Client> m_chatClient;
    std::unique_ptr<whatevr::v1::SendService::Client> m_sendClient;
    std::unique_ptr<QGrpcCallReply> m_statusReply;
    std::unique_ptr<QGrpcCallReply> m_reconnectReply;
    std::unique_ptr<QGrpcCallReply> m_chatsReply;
    std::unique_ptr<QGrpcCallReply> m_messagesReply;
    std::unique_ptr<QGrpcCallReply> m_olderMessagesReply;
    std::unique_ptr<QGrpcCallReply> m_jumpToMessageReply;
    std::unique_ptr<QGrpcCallReply> m_markChatReadReply;
    std::unique_ptr<QGrpcCallReply> m_subscribeChatPresenceReply;
    QHash<QString, std::shared_ptr<QGrpcCallReply>> m_setChatPinnedReplies;
    QHash<QString, std::shared_ptr<QGrpcCallReply>> m_setChatPresenceReplies;
    std::unique_ptr<QGrpcCallReply> m_updateSessionStateReply;
    std::unique_ptr<QGrpcCallReply> m_sendTextReply;
    std::unique_ptr<QGrpcCallReply> m_sendMediaReply;
    QHash<QString, std::shared_ptr<QGrpcCallReply>> m_mediaDownloadReplies;
    QSet<QString> m_mediaDownloadingMessageIds;
    std::unique_ptr<QGrpcCallReply> m_messageInfoReply;
    std::unique_ptr<QGrpcCallReply> m_revokeMessageReply;
    std::unique_ptr<QGrpcCallReply> m_forwardMessageReply;
    QHash<QString, std::shared_ptr<QGrpcCallReply>> m_deleteMessageReplies;
    std::unique_ptr<QGrpcCallReply> m_logoutReply;
    std::unique_ptr<QGrpcServerStream> m_frontendSessionStream;
    std::unique_ptr<QGrpcServerStream> m_daemonStream;
    std::unique_ptr<QGrpcServerStream> m_loginStream;
    QFileSystemWatcher *m_socketWatcher = nullptr;
    QLocalSocket *m_probeSocket = nullptr;
    QTimer *m_retryTimer = nullptr;
    QTimer *m_qrTimer = nullptr;
    QTimer *m_selectedChatReloadTimer = nullptr;
    QTimer *m_updateSessionStateTimer = nullptr;
    QTimer *m_markChatReadTimer = nullptr;
    QString m_pendingSelectedChatReloadId;
    QString m_localComposingChatId;
    QString m_markChatReadChatId;
    QString m_pendingMarkChatReadId;
    QString m_pendingDeepLinkChatId;
};
