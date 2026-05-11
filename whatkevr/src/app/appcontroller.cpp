#include "appcontroller.h"

#include <QDir>
#include <QDateTime>
#include <QFileInfo>
#include <QStandardPaths>
#include <QTimer>
#include <QUrl>

#include <KLocalizedString>

#include <QtGrpc/qgrpccallreply.h>
#include <QtGrpc/qgrpchttp2channel.h>
#include <QtGrpc/qtgrpcnamespace.h>
#include <QtGrpc/qgrpcstream.h>

#include "../models/chatlistmodel.h"
#include "../models/messagelistmodel.h"
#include "whatevr/v1/whatevr.qpb.h"
#include "whatevr/v1/whatevr_client.grpc.qpb.h"

using whatevr::v1::ChatUpdated;
using whatevr::v1::ConnectionChanged;
using whatevr::v1::DaemonStateGadget::DaemonState;
using whatevr::v1::GetStatusRequest;
using whatevr::v1::GetStatusResponse;
using whatevr::v1::GetMessagesRequest;
using whatevr::v1::GetMessagesResponse;
using whatevr::v1::HistorySyncProgress;
using whatevr::v1::ListChatsRequest;
using whatevr::v1::ListChatsResponse;
using whatevr::v1::LoginEvent;
using whatevr::v1::LoginStateChanged;
using whatevr::v1::DownloadMessageMediaRequest;
using whatevr::v1::DownloadMessageMediaResponse;
using whatevr::v1::SendMediaRequest;
using whatevr::v1::SendMediaResponse;
using whatevr::v1::SendTextRequest;
using whatevr::v1::SendTextResponse;
using whatevr::v1::SubscribeEventsRequest;
using whatevr::v1::SubscribeLoginEventsRequest;

namespace {

QString fallbackStateLabel(DaemonState state)
{
    switch (state) {
    case DaemonState::DAEMON_STATE_STARTING:
        return i18nc("@label daemon state", "Starting");
    case DaemonState::DAEMON_STATE_NEED_LOGIN:
        return i18nc("@label daemon state", "Needs login");
    case DaemonState::DAEMON_STATE_CONNECTING:
        return i18nc("@label daemon state", "Connecting");
    case DaemonState::DAEMON_STATE_ONLINE:
        return i18nc("@label daemon state", "Online");
    case DaemonState::DAEMON_STATE_RECONNECTING:
        return i18nc("@label daemon state", "Reconnecting");
    case DaemonState::DAEMON_STATE_OFFLINE:
        return i18nc("@label daemon state", "Offline");
    case DaemonState::DAEMON_STATE_UNSPECIFIED:
    default:
        return i18nc("@label daemon state", "Unknown");
    }
}

QString formatRetryDetail(qint64 nextRetryUnix)
{
    if (nextRetryUnix <= 0) {
        return QString();
    }

    const qint64 now = QDateTime::currentSecsSinceEpoch();
    const qint64 seconds = qMax<qint64>(0, nextRetryUnix - now);
    if (seconds <= 1) {
        return i18nc("@info", "Retrying now");
    }

    return i18ncp("@info countdown", "Retrying in %1 second", "Retrying in %1 seconds", seconds);
}

QString formatQrExpiry(qint64 expiresAtUnix)
{
    if (expiresAtUnix <= 0) {
        return QString();
    }

    const qint64 now = QDateTime::currentSecsSinceEpoch();
    const qint64 secondsLeft = expiresAtUnix - now;

    if (secondsLeft <= 0) {
        return i18nc("@info", "QR code expired. Refresh to request a new one.");
    }

    if (secondsLeft < 60) {
        return i18ncp("@info countdown", "Expires in %1 second", "Expires in %1 seconds", secondsLeft);
    }

    const qint64 minutes = (secondsLeft + 59) / 60;
    return i18ncp("@info countdown", "Expires in %1 minute", "Expires in %1 minutes", minutes);
}

QString syncTypeLabel(whatevr::v1::HistorySyncTypeGadget::HistorySyncType type)
{
    using whatevr::v1::HistorySyncTypeGadget::HistorySyncType;
    switch (type) {
    case HistorySyncType::HISTORY_SYNC_TYPE_INITIAL_BOOTSTRAP:
        return i18nc("@label", "Initial history sync");
    case HistorySyncType::HISTORY_SYNC_TYPE_INITIAL_STATUS_V3:
        return i18nc("@label", "Status history sync");
    case HistorySyncType::HISTORY_SYNC_TYPE_FULL:
        return i18nc("@label", "Full history sync");
    case HistorySyncType::HISTORY_SYNC_TYPE_RECENT:
        return i18nc("@label", "Recent history sync");
    case HistorySyncType::HISTORY_SYNC_TYPE_PUSH_NAME:
        return i18nc("@label", "Updating names");
    case HistorySyncType::HISTORY_SYNC_TYPE_NON_BLOCKING_DATA:
        return i18nc("@label", "Syncing background data");
    case HistorySyncType::HISTORY_SYNC_TYPE_ON_DEMAND:
        return i18nc("@label", "Loading requested history");
    case HistorySyncType::HISTORY_SYNC_TYPE_UNSPECIFIED:
    default:
        return i18nc("@label", "Syncing history");
    }
}

}

AppController::AppController(QObject *parent)
    : QObject(parent)
{
    m_chatListModel = new ChatListModel(this);
    m_messageListModel = new MessageListModel(this);

    m_retryTimer = new QTimer(this);
    m_retryTimer->setSingleShot(true);
    connect(m_retryTimer, &QTimer::timeout, this, &AppController::refresh);

    m_qrTimer = new QTimer(this);
    m_qrTimer->setInterval(1000);
    connect(m_qrTimer, &QTimer::timeout, this, &AppController::updateQrExpiryText);

    QTimer::singleShot(0, this, &AppController::bootstrap);
}

AppController::~AppController() = default;

QString AppController::applicationId() const
{
    return QStringLiteral("in.codelif.Whatevr");
}

QString AppController::applicationDisplayName() const
{
    return QStringLiteral("Whatevr");
}

QString AppController::executableName() const
{
    return QStringLiteral("whatkevr");
}

QString AppController::daemonSocketPath() const
{
    const QString runtimePath = QStandardPaths::writableLocation(QStandardPaths::RuntimeLocation);
    if (runtimePath.isEmpty()) {
        return QString();
    }

    return QDir(runtimePath).filePath(QStringLiteral("whatevrd/whatevrd.sock"));
}

QString AppController::daemonSocketUrl() const
{
    const QString socketPath = daemonSocketPath();
    if (socketPath.isEmpty()) {
        return QString();
    }

    return QStringLiteral("unix://%1").arg(socketPath);
}

bool AppController::loading() const
{
    return m_loading;
}

bool AppController::loginRequired() const
{
    return m_loginRequired;
}

bool AppController::shellVisible() const
{
    return !m_loading && !m_loginRequired && m_hasStatus;
}

bool AppController::qrAvailable() const
{
    return !m_qrCode.isEmpty();
}

QString AppController::statusTitle() const
{
    if (m_loginRequired) {
        return i18nc("@title", "Scan to sign in");
    }

    if (m_loading && !m_hasStatus) {
        return i18nc("@title", "Connecting to whatevrd");
    }

    if (shellVisible()) {
        return i18nc("@title", "Daemon session ready");
    }

    return i18nc("@title", "Waiting for whatevrd");
}

QString AppController::statusText() const
{
    if (m_loginRequired) {
        return i18nc("@info", "Use WhatsApp on your phone to scan the QR code below.");
    }

    if (m_loading && !m_hasStatus) {
        return i18nc("@info", "Preparing the local daemon connection and reading the current session state.");
    }

    if (shellVisible()) {
        return i18nc("@info", "The daemon is reachable. Chat list and timeline work land next on top of this shell.");
    }

    return i18nc("@info", "Whatevr could not reach the daemon yet.");
}

QString AppController::detailText() const
{
    QStringList lines;

    if (!m_daemonStateLabel.isEmpty()) {
        lines << i18nc("@info", "State: %1", m_daemonStateLabel);
    }

    if (!m_statusDetail.isEmpty()) {
        lines << m_statusDetail;
    }

    if (!daemonSocketPath().isEmpty()) {
        lines << i18nc("@info", "Socket: %1", daemonSocketPath());
    }

    return lines.join(QLatin1Char('\n'));
}

QString AppController::bannerText() const
{
    return m_bannerText;
}

QString AppController::qrCode() const
{
    return m_qrCode;
}

QString AppController::qrExpiryText() const
{
    return m_qrExpiryText;
}

QString AppController::primaryActionText() const
{
    return m_canReconnect && !m_loginRequired
        ? i18nc("@action:button", "Reconnect")
        : i18nc("@action:button", "Refresh");
}

bool AppController::primaryActionEnabled() const
{
    return !m_reconnectInFlight;
}

QAbstractItemModel *AppController::chatListModel() const
{
    return m_chatListModel;
}

bool AppController::chatsLoading() const
{
    return m_chatsLoading;
}

bool AppController::chatsEmpty() const
{
    return m_chatListModel->isEmpty();
}

QAbstractItemModel *AppController::messageListModel() const
{
    return m_messageListModel;
}

bool AppController::messagesLoading() const
{
    return m_messagesLoading;
}


bool AppController::messagesEmpty() const
{
    return m_messageListModel->isEmpty();
}

QString AppController::messageErrorText() const
{
    return m_messageErrorText;
}

bool AppController::composerEnabled() const
{
    return hasSelectedChat() && m_sendClient != nullptr;
}

bool AppController::sendInFlight() const
{
    return m_sendInFlight;
}

QString AppController::composerErrorText() const
{
    return m_composerErrorText;
}

QString AppController::selectedChatId() const
{
    return m_selectedChatId;
}

QString AppController::selectedChatName() const
{
    return m_selectedChatName;
}

QString AppController::selectedChatAvatarLocalPath() const
{
    return m_selectedChatAvatarLocalPath;
}

bool AppController::hasSelectedChat() const
{
    return !m_selectedChatId.isEmpty();
}

bool AppController::historySyncVisible() const
{
    return m_historySyncVisible;
}

int AppController::historySyncPercent() const
{
    return m_historySyncPercent;
}

QString AppController::historySyncTitle() const
{
    return m_historySyncTitle;
}

QString AppController::historySyncDetail() const
{
    return m_historySyncDetail;
}

void AppController::refresh()
{
    m_retryTimer->stop();
    clearBanner();
    m_loading = true;
    emitStateChanged();

    if (!ensureChannel()) {
        return;
    }

    requestStatus();
    ensureDaemonStream();
    ensureLoginStream();
}

void AppController::triggerPrimaryAction()
{
    if (m_canReconnect && !m_loginRequired) {
        requestReconnect();
        return;
    }

    refresh();
}

void AppController::selectChat(const QString &chatId)
{
    if (m_selectedChatId == chatId) {
        return;
    }

    m_selectedChatId = chatId;
    m_messageErrorText.clear();
    m_composerErrorText.clear();
    m_messagesLoading = !chatId.isEmpty();
    m_messageListModel->clear();
    updateSelectedChatData();
    Q_EMIT selectionChanged();
    Q_EMIT messagesChanged();
    Q_EMIT composerChanged();

    if (!m_selectedChatId.isEmpty()) {
        requestMessages(m_selectedChatId);
    }
}

void AppController::retryMessages()
{
    if (!m_selectedChatId.isEmpty()) {
        requestMessages(m_selectedChatId);
    }
}


void AppController::sendText(const QString &text)
{
    const QString trimmed = text.trimmed();
    if (!m_sendClient || m_sendTextReply || m_selectedChatId.isEmpty() || trimmed.isEmpty()) {
        return;
    }

    SendTextRequest request;
    request.setChatId(m_selectedChatId);
    request.setText(trimmed);

    m_sendInFlight = true;
    m_composerErrorText.clear();
    Q_EMIT composerChanged();

    m_sendTextReply = m_sendClient->SendText(request);
    auto *reply = m_sendTextReply.get();
    const QString chatId = m_selectedChatId;

    connect(reply, &QGrpcCallReply::finished, this, [this, reply, chatId](const QGrpcStatus &status) {
        if (m_sendTextReply.get() != reply) {
            return;
        }

        m_sendInFlight = false;

        if (!status.isOk()) {
            m_sendTextReply.reset();
            m_composerErrorText = status.message().isEmpty()
                ? i18nc("@info", "Unable to send message")
                : status.message();
            Q_EMIT composerChanged();
            return;
        }

        const auto response = reply->read<SendTextResponse>();
        m_sendTextReply.reset();
        if (response && response->hasMessage() && response->message().chatId() == chatId) {
            applyMessageEvent(response->message());
        }

        Q_EMIT composerChanged();
    });
}

void AppController::sendImage(const QString &fileUrl, const QString &caption)
{
    if (!m_sendClient || m_sendMediaReply || m_selectedChatId.isEmpty() || fileUrl.isEmpty()) {
        return;
    }

    const QUrl url(fileUrl);
    const QString filePath = url.isLocalFile() ? url.toLocalFile() : fileUrl;
    if (filePath.isEmpty()) {
        return;
    }

    SendMediaRequest request;
    request.setChatId(m_selectedChatId);
    request.setFilePath(filePath);
    request.setCaption(caption.trimmed());

    m_sendInFlight = true;
    m_composerErrorText.clear();
    Q_EMIT composerChanged();

    m_sendMediaReply = m_sendClient->SendMedia(request);
    auto *reply = m_sendMediaReply.get();
    const QString chatId = m_selectedChatId;

    connect(reply, &QGrpcCallReply::finished, this, [this, reply, chatId](const QGrpcStatus &status) {
        if (m_sendMediaReply.get() != reply) {
            return;
        }

        m_sendInFlight = false;

        if (!status.isOk()) {
            m_sendMediaReply.reset();
            m_composerErrorText = status.message().isEmpty()
                ? i18nc("@info", "Unable to send image")
                : status.message();
            Q_EMIT composerChanged();
            return;
        }

        const auto response = reply->read<SendMediaResponse>();
        m_sendMediaReply.reset();
        if (response && response->hasMessage() && response->message().chatId() == chatId) {
            applyMessageEvent(response->message());
        }

        Q_EMIT composerChanged();
    });
}

void AppController::downloadMessageMedia(const QString &messageId)
{
    const QString id = messageId.trimmed();
    if (id.isEmpty() || !m_chatClient || m_mediaDownloadReplies.contains(id)) {
        return;
    }

    DownloadMessageMediaRequest request;
    request.setMessageId(id);

    std::shared_ptr<QGrpcCallReply> reply = m_chatClient->DownloadMessageMedia(request);
    if (!reply) {
        return;
    }
    m_mediaDownloadReplies.insert(id, reply);
    Q_EMIT mediaDownloadChanged(id);

    auto *replyPtr = reply.get();
    connect(replyPtr, &QGrpcCallReply::finished, this, [this, replyPtr, id](const QGrpcStatus &status) {
        auto it = m_mediaDownloadReplies.constFind(id);
        if (it == m_mediaDownloadReplies.cend() || it.value().get() != replyPtr) {
            return;
        }

        if (!status.isOk()) {
            m_mediaDownloadReplies.remove(id);
            Q_EMIT mediaDownloadChanged(id);
            const QString errorText = status.message().isEmpty()
                ? i18nc("@info", "Unable to download image")
                : status.message();
            Q_EMIT mediaDownloadFailed(id, errorText);
            return;
        }

        const auto response = replyPtr->read<DownloadMessageMediaResponse>();
        m_mediaDownloadReplies.remove(id);
        Q_EMIT mediaDownloadChanged(id);
        if (response && response->hasMessage()) {
            applyMessageEvent(response->message());
        }
    });
}

bool AppController::isMessageMediaDownloading(const QString &messageId) const
{
    return m_mediaDownloadReplies.contains(messageId.trimmed());
}

void AppController::logout()
{
    if (!m_loginClient || m_logoutReply) {
        return;
    }

    m_logoutReply = m_loginClient->Logout(whatevr::v1::LogoutRequest {});
    auto *reply = m_logoutReply.get();

    connect(reply, &QGrpcCallReply::finished, this, [this, reply](const QGrpcStatus &status) {
        if (m_logoutReply.get() != reply) {
            return;
        }

        m_logoutReply.reset();
        if (!status.isOk()) {
            handleTransportFailure(i18nc("@info", "Logout failed"), status.message());
            return;
        }

        m_selectedChatId.clear();
        m_selectedChatName.clear();
        m_selectedChatAvatarLocalPath.clear();
        m_loginRequired = true;
        m_historySyncVisible = false;
        m_chatListModel->replaceChats({});
        m_messageListModel->clear();
        m_messageErrorText.clear();
        m_composerErrorText.clear();
        Q_EMIT selectionChanged();
        Q_EMIT chatsChanged();
        Q_EMIT messagesChanged();
        Q_EMIT composerChanged();
        Q_EMIT historySyncChanged();
        emitStateChanged();
    });
}

void AppController::bootstrap()
{
    refresh();
}

bool AppController::ensureChannel()
{
    if (m_channel) {
        return true;
    }

    const QString socketUrl = daemonSocketUrl();
    if (socketUrl.isEmpty()) {
        m_loading = false;
        m_hasStatus = false;
        m_bannerText = i18nc("@info", "XDG runtime directory is unavailable, so the daemon socket cannot be resolved.");
        emitStateChanged();
        return false;
    }

    auto channel = std::make_shared<QGrpcHttp2Channel>(QUrl(socketUrl));
    m_channel = channel;
    attachClients();
    return true;
}

void AppController::attachClients()
{
    if (!m_channel) {
        return;
    }

    if (!m_daemonClient) {
        m_daemonClient = std::make_unique<whatevr::v1::DaemonService::Client>(this);
    }
    if (!m_loginClient) {
        m_loginClient = std::make_unique<whatevr::v1::LoginService::Client>(this);
    }
    if (!m_chatClient) {
        m_chatClient = std::make_unique<whatevr::v1::ChatService::Client>(this);
    }
    if (!m_sendClient) {
        m_sendClient = std::make_unique<whatevr::v1::SendService::Client>(this);
    }

    m_daemonClient->attachChannel(m_channel);
    m_loginClient->attachChannel(m_channel);
    m_chatClient->attachChannel(m_channel);
    m_sendClient->attachChannel(m_channel);
    Q_EMIT composerChanged();
}

void AppController::requestStatus()
{
    if (!m_daemonClient) {
        return;
    }

    m_statusReply = m_daemonClient->GetStatus(GetStatusRequest {});
    auto *reply = m_statusReply.get();

    connect(reply, &QGrpcCallReply::finished, this, [this, reply](const QGrpcStatus &status) {
        if (m_statusReply.get() != reply) {
            return;
        }

        if (!status.isOk()) {
            m_statusReply.reset();
            handleTransportFailure(i18nc("@info", "Unable to read daemon status"), status.message());
            return;
        }

        const auto response = reply->read<GetStatusResponse>();
        m_statusReply.reset();

        if (!response) {
            handleTransportFailure(i18nc("@info", "Unable to decode daemon status"), QString());
            return;
        }

        applyStatusResponse(*response);
    });
}

void AppController::requestReconnect()
{
    if (!m_daemonClient || m_reconnectInFlight) {
        return;
    }

    m_reconnectInFlight = true;
    clearBanner();
    emitStateChanged();

    m_reconnectReply = m_daemonClient->Reconnect(whatevr::v1::ReconnectRequest {});
    auto *reply = m_reconnectReply.get();

    connect(reply, &QGrpcCallReply::finished, this, [this, reply](const QGrpcStatus &status) {
        if (m_reconnectReply.get() != reply) {
            return;
        }

        m_reconnectReply.reset();
        m_reconnectInFlight = false;

        if (!status.isOk()) {
            handleTransportFailure(i18nc("@info", "Reconnect request failed"), status.message());
            return;
        }

        m_bannerText = i18nc("@info", "Reconnect requested. Waiting for daemon updates.");
        emitStateChanged();
        refresh();
    });
}

void AppController::requestChats()
{
    if (!m_chatClient || m_chatsReply) {
        return;
    }

    ListChatsRequest request;
    request.setLimit(100);
    request.setOffset(0);

    m_chatsLoading = true;
    Q_EMIT chatsChanged();

    m_chatsReply = m_chatClient->ListChats(request);
    auto *reply = m_chatsReply.get();

    connect(reply, &QGrpcCallReply::finished, this, [this, reply](const QGrpcStatus &status) {
        if (m_chatsReply.get() != reply) {
            return;
        }

        m_chatsLoading = false;

        if (!status.isOk()) {
            m_chatsReply.reset();
            Q_EMIT chatsChanged();
            handleTransportFailure(i18nc("@info", "Unable to load chats"), status.message());
            return;
        }

        const auto response = reply->read<ListChatsResponse>();
        m_chatsReply.reset();
        if (!response) {
            Q_EMIT chatsChanged();
            handleTransportFailure(i18nc("@info", "Unable to decode chats"), QString());
            return;
        }

        m_chatListModel->replaceChats(response->chats());
        if (!m_selectedChatId.isEmpty() && m_chatListModel->indexOf(m_selectedChatId) < 0) {
            m_selectedChatId.clear();
            m_messageListModel->clear();
            m_messageErrorText.clear();
            Q_EMIT messagesChanged();
        }
        updateSelectedChatData();
        Q_EMIT chatsChanged();
        Q_EMIT selectionChanged();
        Q_EMIT composerChanged();
    });
}

void AppController::requestMessages(const QString &chatId)
{
    if (!m_chatClient || chatId.isEmpty()) {
        return;
    }

    if (m_messagesReply) {
        m_messagesReply.reset();
    }

    static constexpr int kMessageLimit = MessageListModel::MaximumMessageCount;

    GetMessagesRequest request;
    request.setChatId(chatId);
    request.setLimit(kMessageLimit);

    m_messagesLoading = true;
    m_messagesLoadingChatId = chatId;
    m_messageErrorText.clear();
    Q_EMIT messagesChanged();

    m_messagesReply = m_chatClient->GetMessages(request);
    auto *reply = m_messagesReply.get();

    connect(reply, &QGrpcCallReply::finished, this, [this, reply, chatId](const QGrpcStatus &status) {
        if (m_messagesReply.get() != reply) {
            return;
        }

        if (m_messagesLoadingChatId != chatId) {
            m_messagesReply.reset();
            return;
        }

        m_messagesLoading = false;

        if (!status.isOk()) {
            m_messagesReply.reset();
            m_messageErrorText = status.message().isEmpty()
                ? i18nc("@info", "Unable to load messages")
                : status.message();
            Q_EMIT messagesChanged();
            return;
        }

        const auto response = reply->read<GetMessagesResponse>();
        m_messagesReply.reset();
        if (!response) {
            m_messageErrorText = i18nc("@info", "Unable to decode messages");
            Q_EMIT messagesChanged();
            return;
        }

        if (m_selectedChatId == chatId) {
            m_messageListModel->replaceMessages(response->messages());
        }
        Q_EMIT messagesChanged();
    });
}

void AppController::ensureDaemonStream()
{
    if (!m_daemonClient || m_daemonStream) {
        return;
    }

    m_daemonStream = m_daemonClient->SubscribeEvents(SubscribeEventsRequest {});
    auto *stream = m_daemonStream.get();

    connect(stream, &QGrpcServerStream::messageReceived, this, [this, stream] {
        if (m_daemonStream.get() != stream) {
            return;
        }

        const auto event = stream->read<whatevr::v1::DaemonEvent>();
        if (!event) {
            return;
        }

        switch (event->payloadField()) {
        case whatevr::v1::DaemonEvent::PayloadFields::ConnectionChanged:
            applyConnectionChanged(event->connectionChanged());
            break;
        case whatevr::v1::DaemonEvent::PayloadFields::LoginStateChanged:
            applyLoginStateChanged(event->loginStateChanged());
            break;
        case whatevr::v1::DaemonEvent::PayloadFields::ChatUpdated:
            applyChatUpdated(event->chatUpdated());
            break;
        case whatevr::v1::DaemonEvent::PayloadFields::NewMessage:
            applyMessageEvent(event->newMessage().message());
            break;
        case whatevr::v1::DaemonEvent::PayloadFields::MessageUpdated:
            applyMessageEvent(event->messageUpdated().message());
            break;
        case whatevr::v1::DaemonEvent::PayloadFields::HistorySyncProgress:
            applyHistorySyncProgress(event->historySyncProgress());
            break;
        default:
            break;
        }
    });

    connect(stream, &QGrpcServerStream::finished, this, [this, stream](const QGrpcStatus &status) {
        if (m_daemonStream.get() != stream) {
            return;
        }

        m_daemonStream.reset();
        if (!status.isOk()) {
            handleTransportFailure(i18nc("@info", "Daemon event stream ended"), status.message());
            return;
        }

        scheduleRetry();
    });
}

void AppController::ensureLoginStream()
{
    if (!m_loginClient || m_loginStream) {
        return;
    }

    m_loginStream = m_loginClient->SubscribeLoginEvents(SubscribeLoginEventsRequest {});
    auto *stream = m_loginStream.get();

    connect(stream, &QGrpcServerStream::messageReceived, this, [this, stream] {
        if (m_loginStream.get() != stream) {
            return;
        }

        const auto event = stream->read<LoginEvent>();
        if (!event) {
            return;
        }

        applyLoginEvent(*event);
    });

    connect(stream, &QGrpcServerStream::finished, this, [this, stream](const QGrpcStatus &status) {
        if (m_loginStream.get() != stream) {
            return;
        }

        m_loginStream.reset();
        if (!status.isOk()) {
            handleTransportFailure(i18nc("@info", "Login event stream ended"), status.message());
            return;
        }

        scheduleRetry();
    });
}

void AppController::scheduleRetry(int delayMs)
{
    if (!m_retryTimer->isActive()) {
        m_retryTimer->start(delayMs);
    }
}

void AppController::handleTransportFailure(const QString &context, const QString &message)
{
    m_loading = false;

    QStringList lines;
    if (!context.isEmpty()) {
        lines << context;
    }
    if (!message.isEmpty()) {
        lines << message;
    }

    m_bannerText = lines.join(QLatin1String(". "));
    if (m_bannerText.isEmpty()) {
        m_bannerText = i18nc("@info", "The daemon connection dropped.");
    }

    emitStateChanged();
    scheduleRetry();
}

void AppController::applyStatusResponse(const GetStatusResponse &status)
{
    m_hasStatus = true;
    m_loading = false;
    m_canReconnect = status.canReconnect();
    m_daemonStateLabel = !status.stateLabel().isEmpty() ? status.stateLabel() : fallbackStateLabel(status.state());
    m_statusDetail = status.detail();
    m_loginRequired = status.state() == DaemonState::DAEMON_STATE_NEED_LOGIN;

    if (!m_loginRequired) {
        m_qrCode.clear();
        m_qrExpiresAtUnix = 0;
        m_qrExpiryText.clear();
        m_qrTimer->stop();
    }

    if (shellVisible()) {
        requestChats();
    }

    clearBanner();
    emitStateChanged();
}

void AppController::applyConnectionChanged(const ConnectionChanged &change)
{
    m_hasStatus = true;
    m_loading = false;
    m_canReconnect = change.canReconnect();
    m_daemonStateLabel = fallbackStateLabel(change.state());

    QString detail = change.detail();
    const QString retryDetail = formatRetryDetail(change.nextRetryUnix());
    if (!retryDetail.isEmpty()) {
        detail = detail.isEmpty() ? retryDetail : i18nc("@info", "%1\n%2", detail, retryDetail);
    }
    m_statusDetail = detail;
    m_loginRequired = change.state() == DaemonState::DAEMON_STATE_NEED_LOGIN;

    if (change.state() == DaemonState::DAEMON_STATE_ONLINE) {
        clearBanner();
    }

    if (!m_loginRequired) {
        m_qrCode.clear();
        m_qrExpiresAtUnix = 0;
        m_qrExpiryText.clear();
        m_qrTimer->stop();
    }

    if (shellVisible()) {
        requestChats();
    }

    emitStateChanged();
}

void AppController::applyLoginStateChanged(const LoginStateChanged &change)
{
    m_hasStatus = true;
    m_loading = false;
    m_daemonStateLabel = fallbackStateLabel(change.state());
    m_statusDetail = change.detail();
    m_loginRequired = change.state() == DaemonState::DAEMON_STATE_NEED_LOGIN;

    if (!m_loginRequired) {
        m_qrCode.clear();
        m_qrExpiresAtUnix = 0;
        m_qrExpiryText.clear();
        m_qrTimer->stop();
    }

    if (shellVisible()) {
        requestChats();
    }

    emitStateChanged();
}

void AppController::applyLoginEvent(const LoginEvent &event)
{
    switch (event.payloadField()) {
    case LoginEvent::PayloadFields::QrCode: {
        const auto &qr = event.qrCode();
        m_qrCode = qr.code();
        m_qrExpiresAtUnix = qr.expiresAtUnix();
        m_loginRequired = true;
        m_loading = false;
        m_hasStatus = true;
        updateQrExpiryText();
        m_qrTimer->start();
        clearBanner();
        emitStateChanged();
        break;
    }
    case LoginEvent::PayloadFields::LoginStateChanged:
        applyLoginStateChanged(event.loginStateChanged());
        break;
    default:
        break;
    }
}

void AppController::applyChatUpdated(const ChatUpdated &update)
{
    if (!update.hasChat()) {
        return;
    }

    m_chatListModel->upsertChat(update.chat(), update.previousChatId());
    updateSelectedChatData();
    Q_EMIT chatsChanged();
    Q_EMIT selectionChanged();
}

void AppController::applyMessageEvent(const whatevr::v1::Message &message)
{
    if (message.chatId() != m_selectedChatId) {
        return;
    }

    m_messageListModel->upsertMessage(message);
    Q_EMIT messagesChanged();
}

void AppController::applyHistorySyncProgress(const HistorySyncProgress &progress)
{
    if (progress.isComplete()) {
        m_historySyncVisible = false;
        m_historySyncPercent = 100;
        m_historySyncTitle.clear();
        m_historySyncDetail.clear();
        Q_EMIT historySyncChanged();
        requestChats();
        return;
    }

    m_historySyncVisible = true;
    m_historySyncPercent = qBound(0, static_cast<int>(progress.progressPercent()), 100);
    m_historySyncTitle = syncTypeLabel(progress.syncType());

    const QString chunkText = progress.chunkOrder() > 0
        ? i18nc("@info", "Chunk %1", progress.chunkOrder())
        : i18nc("@info", "Processing chunk");
    const QString messagesText = i18ncp("@info", "%1 message", "%1 messages", progress.messagesInChunk());
    const QString conversationsText = i18ncp("@info", "%1 conversation", "%1 conversations", progress.conversationsInChunk());
    m_historySyncDetail = i18nc("@info", "%1 · %2 · %3", chunkText, messagesText, conversationsText);

    Q_EMIT historySyncChanged();
}

void AppController::updateQrExpiryText()
{
    const QString nextText = formatQrExpiry(m_qrExpiresAtUnix);
    if (m_qrExpiryText == nextText) {
        return;
    }

    m_qrExpiryText = nextText;
    emitStateChanged();
}

void AppController::clearBanner()
{
    if (m_bannerText.isEmpty()) {
        return;
    }

    m_bannerText.clear();
    emitStateChanged();
}

void AppController::emitStateChanged()
{
    Q_EMIT stateChanged();
}

void AppController::updateSelectedChatData()
{
    if (m_selectedChatId.isEmpty()) {
        m_selectedChatName.clear();
        m_selectedChatAvatarLocalPath.clear();
        return;
    }

    m_selectedChatName = m_chatListModel->chatName(m_selectedChatId);
    m_selectedChatAvatarLocalPath = m_chatListModel->chatAvatarLocalPath(m_selectedChatId);
}
