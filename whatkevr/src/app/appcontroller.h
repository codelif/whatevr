#pragma once

#include <QAbstractItemModel>
#include <QHash>
#include <QList>
#include <QObject>
#include <QSet>
#include <QString>

#include <cstdint>
#include <memory>

#include "whatevr/v1/whatevr.qpb.h"

QT_BEGIN_NAMESPACE
class QGrpcCallReply;
class QGrpcServerStream;
class QTimer;
class QAbstractGrpcChannel;
QT_END_NAMESPACE

namespace whatevr::v1 {
class GetStatusResponse;
class ConnectionChanged;
class LoginStateChanged;
class LoginEvent;
class ChatUpdated;
class ChatPresenceChanged;
class MediaDownloadChanged;
class HistorySyncProgress;
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
class MessageListModel;

class AppController final : public QObject
{
    Q_OBJECT
    Q_PROPERTY(QString applicationId READ applicationId CONSTANT FINAL)
    Q_PROPERTY(QString applicationDisplayName READ applicationDisplayName CONSTANT FINAL)
    Q_PROPERTY(QString executableName READ executableName CONSTANT FINAL)
    Q_PROPERTY(QString daemonSocketPath READ daemonSocketPath CONSTANT FINAL)
    Q_PROPERTY(QString daemonSocketUrl READ daemonSocketUrl CONSTANT FINAL)
    Q_PROPERTY(bool loading READ loading NOTIFY stateChanged FINAL)
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
    explicit AppController(QObject *parent = nullptr);
    ~AppController() override;

    [[nodiscard]] QString applicationId() const;
    [[nodiscard]] QString applicationDisplayName() const;
    [[nodiscard]] QString executableName() const;
    [[nodiscard]] QString daemonSocketPath() const;
    [[nodiscard]] QString daemonSocketUrl() const;
    [[nodiscard]] bool loading() const;
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
    Q_INVOKABLE void triggerPrimaryAction();
    Q_INVOKABLE void selectChat(const QString &chatId);
    Q_INVOKABLE void retryMessages();
    Q_INVOKABLE void loadOlderMessages();
    Q_INVOKABLE void sendText(const QString &text);
    Q_INVOKABLE void sendImage(const QString &fileUrl, const QString &caption = QString());
    Q_INVOKABLE void setSelectedChatComposing(bool composing);
    Q_INVOKABLE void downloadMessageMedia(const QString &messageId);
    Q_INVOKABLE bool isMessageMediaDownloading(const QString &messageId) const;
    Q_INVOKABLE void logout();

Q_SIGNALS:
    void stateChanged();
    void chatsChanged();
    void messagesChanged();
    void composerChanged();
    void selectionChanged();
    void historySyncChanged();
    void mediaDownloadChanged(const QString &messageId);
    void mediaDownloadFailed(const QString &messageId, const QString &errorText);

private:
    void bootstrap();
    bool ensureChannel();
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
    void ensureDaemonStream();
    void ensureLoginStream();
    void scheduleRetry(int delayMs = 2000);
    void handleTransportFailure(const QString &context, const QString &message);
    void applyStatusResponse(const whatevr::v1::GetStatusResponse &status);
    void applyConnectionChanged(const whatevr::v1::ConnectionChanged &change);
    void applyLoginStateChanged(const whatevr::v1::LoginStateChanged &change);
    void applyLoginEvent(const whatevr::v1::LoginEvent &event);
    void applyChatUpdated(const whatevr::v1::ChatUpdated &update);
    void applyChatPresenceChanged(const whatevr::v1::ChatPresenceChanged &presence);
    void applyMediaDownloadChanged(const whatevr::v1::MediaDownloadChanged &download);
    void applyMessageEvent(const whatevr::v1::Message &message);
    void applyHistorySyncProgress(const whatevr::v1::HistorySyncProgress &progress);
    void updateQrExpiryText();
    void clearBanner();
    void emitStateChanged();
    void updateSelectedChatData();
    void cacheMessages(const QString &chatId, const QList<whatevr::v1::Message> &messages, bool canLoadOlderMessages);
    bool restoreCachedMessages(const QString &chatId);

    bool m_loading = true;
    bool m_loginRequired = false;
    bool m_canReconnect = false;
    bool m_hasStatus = false;
    bool m_reconnectInFlight = false;
    QString m_daemonStateLabel;
    QString m_statusDetail;
    QString m_bannerText;
    QString m_qrCode;
    QString m_qrExpiryText;
    qint64 m_qrExpiresAtUnix = 0;
    bool m_chatsLoading = false;
    bool m_messagesLoading = false;
    bool m_olderMessagesLoading = false;
    bool m_canLoadOlderMessages = false;
    QString m_messagesLoadingChatId;
    QString m_olderMessagesLoadingChatId;
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

    struct CachedMessages {
        QList<whatevr::v1::Message> messages;
        bool canLoadOlderMessages = false;
    };
    QHash<QString, CachedMessages> m_messageCache;

    ChatListModel *m_chatListModel = nullptr;
    MessageListModel *m_messageListModel = nullptr;

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
    std::unique_ptr<QGrpcCallReply> m_markChatReadReply;
    std::unique_ptr<QGrpcCallReply> m_subscribeChatPresenceReply;
    QHash<QString, std::shared_ptr<QGrpcCallReply>> m_setChatPresenceReplies;
    std::unique_ptr<QGrpcCallReply> m_updateSessionStateReply;
    std::unique_ptr<QGrpcCallReply> m_sendTextReply;
    std::unique_ptr<QGrpcCallReply> m_sendMediaReply;
    QHash<QString, std::shared_ptr<QGrpcCallReply>> m_mediaDownloadReplies;
    QSet<QString> m_mediaDownloadingMessageIds;
    std::unique_ptr<QGrpcCallReply> m_logoutReply;
    std::unique_ptr<QGrpcServerStream> m_frontendSessionStream;
    std::unique_ptr<QGrpcServerStream> m_daemonStream;
    std::unique_ptr<QGrpcServerStream> m_loginStream;
    QTimer *m_retryTimer = nullptr;
    QTimer *m_qrTimer = nullptr;
    QString m_localComposingChatId;
};
