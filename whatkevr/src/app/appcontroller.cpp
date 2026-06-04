#include "appcontroller.h"

#include <QClipboard>
#include <QDir>
#include <QDateTime>
#include <QFileInfo>
#include <QGuiApplication>
#include <QImage>
#include <QMimeData>
#include <QQmlEngine>
#include <QLocale>
#include <QStandardPaths>
#include <QTextBoundaryFinder>
#include <QTimer>
#include <QUrl>
#include <QUuid>

#include <KLocalizedString>

#include <QtGrpc/qgrpccallreply.h>
#include <QtGrpc/qgrpchttp2channel.h>
#include <QtGrpc/qtgrpcnamespace.h>
#include <QtGrpc/qgrpcstream.h>

#include <algorithm>

#include "../models/chatlistmodel.h"
#include "../models/emojimodel.h"
#include "../models/messagelistmodel.h"
#include "whatevr/v1/whatevr.qpb.h"
#include "whatevr/v1/whatevr_client.grpc.qpb.h"

using whatevr::v1::ChatUpdated;
using whatevr::v1::AvatarSubjectKindGadget::AvatarSubjectKind;
using whatevr::v1::AvatarUpdated;
using whatevr::v1::ChatPresenceChanged;
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
using whatevr::v1::MarkChatReadRequest;
using whatevr::v1::MediaDownloadChanged;
using whatevr::v1::SetChatPinnedRequest;
using whatevr::v1::SetChatPresenceRequest;
using whatevr::v1::SubscribeChatPresenceRequest;
using whatevr::v1::DownloadMessageMediaRequest;
using whatevr::v1::DownloadMessageMediaResponse;
using whatevr::v1::HoldSessionRequest;
using whatevr::v1::SendMediaRequest;
using whatevr::v1::SendMediaResponse;
using whatevr::v1::SendTextRequest;
using whatevr::v1::SendTextResponse;
using whatevr::v1::SubscribeEventsRequest;
using whatevr::v1::SubscribeLoginEventsRequest;
using whatevr::v1::UpdateSessionStateRequest;

namespace {

AppController *s_appControllerInstance = nullptr;

constexpr int kMessageLimit = MessageListModel::MaximumMessageCount;
constexpr int kCachedChatLimit = 32;
constexpr int kCachedMessagesPerChatLimit = kMessageLimit * 4;

bool isSupportedOutboundImageFile(const QString &filePath)
{
    const QString suffix = QFileInfo(filePath).suffix().toLower();
    return suffix == QStringLiteral("png")
        || suffix == QStringLiteral("jpg")
        || suffix == QStringLiteral("jpeg")
        || suffix == QStringLiteral("webp")
        || suffix == QStringLiteral("gif");
}

QList<whatevr::v1::Message> mergeMessages(const QList<whatevr::v1::Message> &base,
                                           const QList<whatevr::v1::Message> &updates)
{
    QList<whatevr::v1::Message> merged;
    merged.reserve(base.size() + updates.size());

    QHash<QString, qsizetype> indexesById;
    auto appendOrReplace = [&merged, &indexesById](const whatevr::v1::Message &message) {
        const QString &id = message.id_proto();
        if (!id.isEmpty()) {
            const auto existing = indexesById.constFind(id);
            if (existing != indexesById.constEnd()) {
                merged[*existing] = message;
                return;
            }
            indexesById.insert(id, merged.size());
        }
        merged.append(message);
    };

    for (const auto &message : base) {
        appendOrReplace(message);
    }
    for (const auto &message : updates) {
        appendOrReplace(message);
    }

    std::sort(merged.begin(), merged.end(), [](const whatevr::v1::Message &left, const whatevr::v1::Message &right) {
        if (left.timestampUnix() != right.timestampUnix()) {
            return left.timestampUnix() < right.timestampUnix();
        }
        return left.id_proto() < right.id_proto();
    });

    return merged;
}

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
        return {};
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
        return {};
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

QString formatLastSeen(qint64 lastSeenUnix)
{
    if (lastSeenUnix <= 0) {
        return {};
    }

    const QDateTime lastSeen = QDateTime::fromSecsSinceEpoch(lastSeenUnix).toLocalTime();
    if (!lastSeen.isValid()) {
        return {};
    }

    const QDate today = QDate::currentDate();
    if (lastSeen.date() == today) {
        return i18nc("@info chat presence", "last seen today at %1", QLocale().toString(lastSeen.time(), QLocale::ShortFormat));
    }
    if (lastSeen.date() == today.addDays(-1)) {
        return i18nc("@info chat presence", "last seen yesterday at %1", QLocale().toString(lastSeen.time(), QLocale::ShortFormat));
    }

    return i18nc("@info chat presence", "last seen %1", QLocale().toString(lastSeen, QLocale::ShortFormat));
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
    case HistorySyncType::HISTORY_SYNC_TYPE_PROFILE_PICTURE:
        return i18nc("@label", "Syncing profile pictures");
    case HistorySyncType::HISTORY_SYNC_TYPE_OFFLINE_CATCHUP:
        return i18nc("@label", "Syncing missed messages");
    case HistorySyncType::HISTORY_SYNC_TYPE_UNSPECIFIED:
    default:
        return i18nc("@label", "Syncing history");
    }
}

}

void AppController::setInstance(AppController *instance)
{
    s_appControllerInstance = instance;
}

AppController *AppController::create(QQmlEngine *qmlEngine, QJSEngine *jsEngine)
{
    Q_UNUSED(qmlEngine)
    Q_UNUSED(jsEngine)

    Q_ASSERT(s_appControllerInstance);
    QQmlEngine::setObjectOwnership(s_appControllerInstance, QQmlEngine::CppOwnership);
    return s_appControllerInstance;
}

AppController::AppController(QObject *parent)
    : QObject(parent)
{
    m_chatListModel = new ChatListModel(this);
    m_emojiModel = new EmojiModel(this);
    m_messageListModel = new MessageListModel(this);
    m_frontendSessionId = QUuid::createUuid().toString(QUuid::WithoutBraces);

    m_retryTimer = new QTimer(this);
    m_retryTimer->setSingleShot(true);
    connect(m_retryTimer, &QTimer::timeout, this, &AppController::refresh);

    m_qrTimer = new QTimer(this);
    m_qrTimer->setInterval(1000);
    connect(m_qrTimer, &QTimer::timeout, this, &AppController::updateQrExpiryText);

    m_selectedChatReloadTimer = new QTimer(this);
    m_selectedChatReloadTimer->setSingleShot(true);
    m_selectedChatReloadTimer->setInterval(300);
    connect(m_selectedChatReloadTimer, &QTimer::timeout, this, [this]() {
        const QString chatId = m_pendingSelectedChatReloadId;
        if (chatId.isEmpty() || chatId != m_selectedChatId) {
            m_pendingSelectedChatReloadId.clear();
            return;
        }
        if (m_messagesLoading || m_olderMessagesLoading) {
            m_selectedChatReloadTimer->start();
            return;
        }
        m_pendingSelectedChatReloadId.clear();
        m_messageCache.remove(chatId);
        m_messageCacheOrder.removeAll(chatId);
        requestMessages(chatId);
    });

    m_updateSessionStateTimer = new QTimer(this);
    m_updateSessionStateTimer->setSingleShot(true);
    m_updateSessionStateTimer->setInterval(75);
    connect(m_updateSessionStateTimer, &QTimer::timeout, this, &AppController::sendFrontendSessionState);

    m_markChatReadTimer = new QTimer(this);
    m_markChatReadTimer->setSingleShot(true);
    m_markChatReadTimer->setInterval(120);
    connect(m_markChatReadTimer, &QTimer::timeout, this, &AppController::sendSelectedChatReadIfActive);

    connect(qGuiApp, &QGuiApplication::applicationStateChanged, this, [this](Qt::ApplicationState state) {
        updateFrontendSessionState();
        if (state != Qt::ApplicationActive && !m_localComposingChatId.isEmpty()) {
            setChatComposing(m_localComposingChatId, false);
        }
        if (state == Qt::ApplicationActive) {
            requestSelectedChatReadIfActive();
        }
    });

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
        return {};
    }

    return QDir(runtimePath).filePath(QStringLiteral("whatevrd/whatevrd.sock"));
}

QString AppController::daemonSocketUrl() const
{
    const QString socketPath = daemonSocketPath();
    if (socketPath.isEmpty()) {
        return {};
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

QAbstractItemModel *AppController::emojiModel() const
{
    return m_emojiModel;
}

bool AppController::messagesLoading() const
{
    return m_messagesLoading;
}

bool AppController::olderMessagesLoading() const
{
    return m_olderMessagesLoading;
}

bool AppController::canLoadOlderMessages() const
{
    return m_canLoadOlderMessages;
}

bool AppController::messagesEmpty() const
{
    return m_messageListModel->isEmpty();
}

QString AppController::displayedMessagesChatId() const
{
    return m_displayedMessagesChatId;
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

QString AppController::selectedChatPresenceText() const
{
    if (!hasSelectedChat()) {
        return {};
    }
    if (m_selectedChatComposing) {
        return i18nc("@info chat presence", "typing...");
    }
    if (m_selectedChatAvailability == 1) {
        return i18nc("@info chat presence", "online");
    }
    return formatLastSeen(m_selectedChatLastSeenUnix);
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
    ensureFrontendSession();
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

    if (!m_localComposingChatId.isEmpty()) {
        setChatComposing(m_localComposingChatId, false);
    }

    m_selectedChatReloadTimer->stop();
    m_pendingSelectedChatReloadId.clear();
    m_messagesReply.reset();
    m_messagesLoadingChatId.clear();
    m_markChatReadReply.reset();
    m_markChatReadChatId.clear();
    m_pendingMarkChatReadId.clear();
    m_markChatReadTimer->stop();
    m_subscribeChatPresenceReply.reset();

    m_selectedChatId = chatId;
    m_selectedChatComposing = false;
    m_selectedChatAvailability = 0;
    m_selectedChatLastSeenUnix = 0;
    m_messageErrorText.clear();
    m_composerErrorText.clear();
    m_messagesLoading = !chatId.isEmpty();
    m_olderMessagesLoading = false;
    m_canLoadOlderMessages = false;
    m_olderMessagesLoadingChatId.clear();
    m_olderMessagesReply.reset();
    m_jumpToMessageChatId.clear();
    m_jumpToMessageId.clear();
    m_jumpToMessageReply.reset();
    updateSelectedChatData();
    const bool restoredMessages = restoreCachedMessages(chatId);
    if (restoredMessages) {
        m_messagesLoading = false;
        m_messagesLoadingChatId.clear();
    }
    Q_EMIT selectionChanged();
    Q_EMIT messagesChanged();
    Q_EMIT composerChanged();
    updateFrontendSessionState();

    if (!m_selectedChatId.isEmpty()) {
        requestSelectedChatPresence();
        requestMessages(m_selectedChatId);
        requestSelectedChatReadIfActive();
    }
}

void AppController::retryMessages()
{
    if (!m_selectedChatId.isEmpty()) {
        requestMessages(m_selectedChatId);
    }
}

void AppController::loadOlderMessages()
{
    requestOlderMessages();
}

void AppController::jumpToMessage(const QString &messageId)
{
    const QString trimmed = messageId.trimmed();
    if (trimmed.isEmpty() || m_selectedChatId.isEmpty()) {
        Q_EMIT messageJumpUnavailable(trimmed);
        return;
    }

    if (m_messageListModel->indexOf(trimmed) >= 0) {
        Q_EMIT messageJumpReady(trimmed);
        return;
    }

    if (!m_chatClient) {
        Q_EMIT messageJumpUnavailable(trimmed);
        return;
    }

    if (m_jumpToMessageReply) {
        m_jumpToMessageReply.reset();
    }
    if (m_messagesReply) {
        m_messagesReply.reset();
        m_messagesLoading = false;
        m_messagesLoadingChatId.clear();
    }
    if (m_olderMessagesReply) {
        m_olderMessagesReply.reset();
        m_olderMessagesLoading = false;
        m_olderMessagesLoadingChatId.clear();
    }

    GetMessagesRequest request;
    request.setChatId(m_selectedChatId);
    request.setLimit(kMessageLimit);
    request.setAroundMessageId(trimmed);

    m_jumpToMessageChatId = m_selectedChatId;
    m_jumpToMessageId = trimmed;
    m_jumpToMessageReply = m_chatClient->GetMessages(request);
    auto *reply = m_jumpToMessageReply.get();
    const QString chatId = m_selectedChatId;

    connect(reply, &QGrpcCallReply::finished, this, [this, reply, chatId, trimmed](const QGrpcStatus &status) {
        if (m_jumpToMessageReply.get() != reply) {
            return;
        }

        if (m_jumpToMessageChatId != chatId || m_jumpToMessageId != trimmed || m_selectedChatId != chatId) {
            m_jumpToMessageReply.reset();
            return;
        }

        if (!status.isOk()) {
            m_jumpToMessageReply.reset();
            m_jumpToMessageChatId.clear();
            m_jumpToMessageId.clear();
            Q_EMIT messageJumpUnavailable(trimmed);
            return;
        }

        const auto response = reply->read<GetMessagesResponse>();
        m_jumpToMessageReply.reset();
        m_jumpToMessageChatId.clear();
        m_jumpToMessageId.clear();
        if (!response || response->messages().isEmpty()) {
            Q_EMIT messageJumpUnavailable(trimmed);
            return;
        }

        QList<whatevr::v1::Message> visibleMessages = response->messages();
        const auto cached = m_messageCache.constFind(chatId);
        if (cached != m_messageCache.constEnd()) {
            visibleMessages = mergeMessages(cached->messages, response->messages());
        }

        cacheMessages(chatId, visibleMessages, response->messages().size() >= kMessageLimit);
        if (m_selectedChatId == chatId) {
            m_displayedMessagesChatId = chatId;
            m_messageListModel->replaceMessages(visibleMessages);
            m_canLoadOlderMessages = response->messages().size() >= kMessageLimit;
            m_messagesLoading = false;
            m_olderMessagesLoading = false;
            Q_EMIT messagesChanged();
            QTimer::singleShot(0, this, [this, trimmed] {
                Q_EMIT messageJumpReady(trimmed);
            });
        }
    });
}

void AppController::sendText(const QString &text, const QString &replyToMessageId)
{
    const QString trimmed = text.trimmed();
    if (!m_sendClient || m_sendTextReply || m_selectedChatId.isEmpty() || trimmed.isEmpty()) {
        return;
    }

    setChatComposing(m_selectedChatId, false);

    SendTextRequest request;
    request.setChatId(m_selectedChatId);
    request.setText(trimmed);
    request.setReplyToMessageId(replyToMessageId.trimmed());

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

void AppController::sendImage(const QString &fileUrl, const QString &caption, const QString &replyToMessageId)
{
    if (!m_sendClient || m_sendMediaReply || m_selectedChatId.isEmpty() || fileUrl.isEmpty()) {
        return;
    }

    const QUrl url(fileUrl);
    const QString filePath = url.isLocalFile() ? url.toLocalFile() : fileUrl;
    if (filePath.isEmpty()) {
        return;
    }

    setChatComposing(m_selectedChatId, false);

    SendMediaRequest request;
    request.setChatId(m_selectedChatId);
    request.setFilePath(filePath);
    request.setCaption(caption.trimmed());
    request.setReplyToMessageId(replyToMessageId.trimmed());

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

void AppController::addRecentEmoji(const QString &emoji)
{
    m_emojiModel->addRecentEmoji(emoji);
}

int AppController::previousGraphemeBoundary(const QString &text, int cursorPosition) const
{
    const int position = qBound(0, cursorPosition, text.size());
    if (position <= 0) {
        return 0;
    }

    QTextBoundaryFinder finder(QTextBoundaryFinder::Grapheme, text);
    finder.setPosition(position);
    const int boundary = finder.toPreviousBoundary();
    if (boundary >= 0 && boundary < position) {
        return boundary;
    }

    return qMax(0, position - 1);
}

bool AppController::sendClipboardImage(const QString &caption, const QString &replyToMessageId)
{
    if (!m_sendClient || m_sendMediaReply || m_selectedChatId.isEmpty()) {
        return false;
    }

    const QClipboard *clipboard = QGuiApplication::clipboard();
    if (!clipboard) {
        return false;
    }

    const QMimeData *mimeData = clipboard->mimeData();
    if (!mimeData) {
        return false;
    }

    if (mimeData->hasImage()) {
        const QImage image = qvariant_cast<QImage>(mimeData->imageData());
        if (image.isNull()) {
            return false;
        }

        QString cacheRoot = QStandardPaths::writableLocation(QStandardPaths::CacheLocation);
        if (cacheRoot.isEmpty()) {
            cacheRoot = QStandardPaths::writableLocation(QStandardPaths::TempLocation);
        }
        if (cacheRoot.isEmpty()) {
            m_composerErrorText = i18nc("@info", "Unable to paste image");
            Q_EMIT composerChanged();
            return true;
        }

        QDir cacheDir(cacheRoot);
        if (!cacheDir.mkpath(QStringLiteral("clipboard"))) {
            m_composerErrorText = i18nc("@info", "Unable to paste image");
            Q_EMIT composerChanged();
            return true;
        }

        const QString fileName = QStringLiteral("pasted-%1-%2.png")
            .arg(QDateTime::currentMSecsSinceEpoch())
            .arg(QUuid::createUuid().toString(QUuid::WithoutBraces));
        const QString filePath = cacheDir.filePath(QStringLiteral("clipboard/%1").arg(fileName));
        if (!image.save(filePath, "PNG")) {
            m_composerErrorText = i18nc("@info", "Unable to paste image");
            Q_EMIT composerChanged();
            return true;
        }

        sendImage(filePath, caption, replyToMessageId);
        return true;
    }

    if (mimeData->hasUrls()) {
        const QList<QUrl> urls = mimeData->urls();
        for (const QUrl &url : urls) {
            if (!url.isLocalFile()) {
                continue;
            }
            const QString filePath = url.toLocalFile();
            if (isSupportedOutboundImageFile(filePath)) {
                sendImage(filePath, caption, replyToMessageId);
                return true;
            }
        }
    }

    return false;
}

void AppController::setSelectedChatComposing(bool composing)
{
    if (m_selectedChatId.isEmpty()) {
        return;
    }

    setChatComposing(m_selectedChatId, composing);
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
    m_mediaDownloadingMessageIds.insert(id);
    m_messageListModel->setMediaDownloadState(id, true);
    Q_EMIT mediaDownloadChanged(id);

    auto *replyPtr = reply.get();
    connect(replyPtr, &QGrpcCallReply::finished, this, [this, replyPtr, id](const QGrpcStatus &status) {
        auto it = m_mediaDownloadReplies.constFind(id);
        if (it == m_mediaDownloadReplies.cend() || it.value().get() != replyPtr) {
            return;
        }

        if (!status.isOk()) {
            m_mediaDownloadReplies.remove(id);
            m_mediaDownloadingMessageIds.remove(id);
            const QString errorText = status.message().isEmpty()
                ? i18nc("@info", "Unable to download image")
                : status.message();
            m_messageListModel->setMediaDownloadState(id, false, errorText);
            Q_EMIT mediaDownloadFailed(id, errorText);
            return;
        }

        const auto response = replyPtr->read<DownloadMessageMediaResponse>();
        m_mediaDownloadReplies.remove(id);
        m_mediaDownloadingMessageIds.remove(id);
        m_messageListModel->setMediaDownloadState(id, false);
        if (response && response->hasMessage()) {
            applyMessageEvent(response->message());
        }
    });
}

bool AppController::isMessageMediaDownloading(const QString &messageId) const
{
    return m_mediaDownloadingMessageIds.contains(messageId.trimmed());
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
        m_selectedChatIsGroup = false;
        m_messageListModel->setGroupChat(false);
        m_selectedChatComposing = false;
        m_selectedChatAvailability = 0;
        m_selectedChatLastSeenUnix = 0;
        m_loginRequired = true;
        m_historySyncVisible = false;
        m_messagesLoading = false;
        m_olderMessagesLoading = false;
        m_canLoadOlderMessages = false;
        m_chatListModel->replaceChats({});
        m_messageListModel->clear();
        m_displayedMessagesChatId.clear();
        m_messageCache.clear();
        m_messageCacheOrder.clear();
        const auto downloadingIds = m_mediaDownloadingMessageIds.values();
        m_mediaDownloadingMessageIds.clear();
        m_mediaDownloadReplies.clear();
        m_messageErrorText.clear();
        m_composerErrorText.clear();
        Q_EMIT selectionChanged();
        Q_EMIT chatsChanged();
        Q_EMIT messagesChanged();
        Q_EMIT composerChanged();
        Q_EMIT historySyncChanged();
        for (const auto &id : downloadingIds) {
            Q_EMIT mediaDownloadChanged(id);
        }
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
    if (!m_frontendClient) {
        m_frontendClient = std::make_unique<whatevr::v1::FrontendService::Client>(this);
    }
    if (!m_chatClient) {
        m_chatClient = std::make_unique<whatevr::v1::ChatService::Client>(this);
    }
    if (!m_sendClient) {
        m_sendClient = std::make_unique<whatevr::v1::SendService::Client>(this);
    }

    m_daemonClient->attachChannel(m_channel);
    m_loginClient->attachChannel(m_channel);
    m_frontendClient->attachChannel(m_channel);
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
            m_displayedMessagesChatId.clear();
            m_canLoadOlderMessages = false;
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
    if (m_olderMessagesReply) {
        m_olderMessagesReply.reset();
    }

    GetMessagesRequest request;
    request.setChatId(chatId);
    request.setLimit(kMessageLimit);

    m_messagesLoading = true;
    m_olderMessagesLoading = false;
    if (m_displayedMessagesChatId != chatId) {
        m_canLoadOlderMessages = false;
    }
    m_messagesLoadingChatId = chatId;
    m_olderMessagesLoadingChatId.clear();
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

        QList<whatevr::v1::Message> visibleMessages = response->messages();
        const auto cached = m_messageCache.constFind(chatId);
        if (cached != m_messageCache.constEnd()) {
            visibleMessages = mergeMessages(cached->messages, response->messages());
        }

        cacheMessages(chatId, visibleMessages, response->messages().size() >= kMessageLimit);
        if (m_selectedChatId == chatId) {
            m_displayedMessagesChatId = chatId;
            m_messageListModel->replaceMessages(visibleMessages);
            m_canLoadOlderMessages = response->messages().size() >= kMessageLimit;
        }
        Q_EMIT messagesChanged();
    });
}

void AppController::requestOlderMessages()
{
    if (!m_chatClient || m_selectedChatId.isEmpty() || m_olderMessagesLoading || !m_canLoadOlderMessages) {
        return;
    }

    const QString beforeMessageId = m_messageListModel->oldestMessageId();
    if (beforeMessageId.isEmpty()) {
        m_canLoadOlderMessages = false;
        Q_EMIT messagesChanged();
        return;
    }

    GetMessagesRequest request;
    request.setChatId(m_selectedChatId);
    request.setLimit(kMessageLimit);
    request.setBeforeMessageId(beforeMessageId);

    m_olderMessagesLoading = true;
    m_olderMessagesLoadingChatId = m_selectedChatId;
    Q_EMIT messagesChanged();

    m_olderMessagesReply = m_chatClient->GetMessages(request);
    auto *reply = m_olderMessagesReply.get();
    const QString chatId = m_selectedChatId;

    connect(reply, &QGrpcCallReply::finished, this, [this, reply, chatId](const QGrpcStatus &status) {
        if (m_olderMessagesReply.get() != reply) {
            return;
        }

        if (m_olderMessagesLoadingChatId != chatId) {
            m_olderMessagesReply.reset();
            return;
        }

        m_olderMessagesLoading = false;

        if (!status.isOk()) {
            m_olderMessagesReply.reset();
            m_canLoadOlderMessages = false;
            Q_EMIT messagesChanged();
            return;
        }

        const auto response = reply->read<GetMessagesResponse>();
        m_olderMessagesReply.reset();
        if (!response) {
            m_canLoadOlderMessages = false;
            Q_EMIT messagesChanged();
            return;
        }

        if (m_selectedChatId == chatId) {
            const auto cached = m_messageCache.constFind(chatId);
            const QList<whatevr::v1::Message> baseMessages = cached != m_messageCache.constEnd()
                ? cached->messages
                : response->messages();
            cacheMessages(chatId,
                          mergeMessages(baseMessages, response->messages()),
                          response->messages().size() >= kMessageLimit);
            m_messageListModel->appendOlderMessages(response->messages());
            m_canLoadOlderMessages = response->messages().size() >= kMessageLimit;
        }
        Q_EMIT messagesChanged();
    });
}

void AppController::requestSelectedChatReadIfActive()
{
    if (!m_chatClient || m_selectedChatId.isEmpty() || QGuiApplication::applicationState() != Qt::ApplicationActive) {
        return;
    }

    m_pendingMarkChatReadId = m_selectedChatId;
    m_markChatReadTimer->start();
}

void AppController::sendSelectedChatReadIfActive()
{
    if (!m_chatClient || m_pendingMarkChatReadId.isEmpty() || m_pendingMarkChatReadId != m_selectedChatId || QGuiApplication::applicationState() != Qt::ApplicationActive) {
        m_pendingMarkChatReadId.clear();
        return;
    }

    const QString chatId = m_pendingMarkChatReadId;
    m_pendingMarkChatReadId.clear();
    if (m_markChatReadReply && m_markChatReadChatId == chatId) {
        return;
    }
    if (m_markChatReadReply) {
        m_markChatReadReply.reset();
        m_markChatReadChatId.clear();
    }

    MarkChatReadRequest request;
    request.setChatId(chatId);

    m_markChatReadReply = m_chatClient->MarkChatRead(request);
    auto *reply = m_markChatReadReply.get();
    if (!reply) {
        m_markChatReadChatId.clear();
        return;
    }
    m_markChatReadChatId = chatId;

    connect(reply, &QGrpcCallReply::finished, this, [this, reply, chatId](const QGrpcStatus &status) {
        if (m_markChatReadReply.get() != reply) {
            return;
        }

        m_markChatReadReply.reset();
        m_markChatReadChatId.clear();
        Q_UNUSED(status)
        Q_UNUSED(chatId)
    });
}

void AppController::requestSelectedChatPresence()
{
    if (!m_chatClient || m_selectedChatId.isEmpty()) {
        return;
    }

    SubscribeChatPresenceRequest request;
    request.setChatId(m_selectedChatId);

    m_subscribeChatPresenceReply.reset();
    m_subscribeChatPresenceReply = m_chatClient->SubscribeChatPresence(request);
    auto *reply = m_subscribeChatPresenceReply.get();
    if (!reply) {
        return;
    }
    const QString chatId = m_selectedChatId;

    connect(reply, &QGrpcCallReply::finished, this, [this, reply, chatId](const QGrpcStatus &status) {
        if (m_subscribeChatPresenceReply.get() != reply) {
            return;
        }

        m_subscribeChatPresenceReply.reset();
        Q_UNUSED(status)
        Q_UNUSED(chatId)
    });
}

void AppController::setChatPinned(const QString &chatId, bool pinned)
{
    if (!m_chatClient || chatId.isEmpty()) {
        return;
    }

    SetChatPinnedRequest request;
    request.setChatId(chatId);
    request.setPinned(pinned);

    auto reply = m_chatClient->SetChatPinned(request);
    auto *replyPtr = reply.get();
    m_setChatPinnedReplies.insert(chatId, std::move(reply));

    connect(replyPtr, &QGrpcCallReply::finished, this, [this, replyPtr, chatId, pinned](const QGrpcStatus &status) {
        auto it = m_setChatPinnedReplies.find(chatId);
        if (it == m_setChatPinnedReplies.end() || it.value().get() != replyPtr) {
            return;
        }

        m_setChatPinnedReplies.erase(it);
        if (!status.isOk()) {
            m_bannerText = status.message().isEmpty()
                ? (pinned ? i18nc("@info", "Unable to pin chat") : i18nc("@info", "Unable to unpin chat"))
                : status.message();
            emitStateChanged();
        }
    });
}

void AppController::setChatComposing(const QString &chatId, bool composing)
{
    if (!m_chatClient || chatId.isEmpty()) {
        return;
    }
    if (!composing && m_localComposingChatId != chatId) {
        return;
    }

    SetChatPresenceRequest request;
    request.setChatId(chatId);
    request.setComposing(composing);

    auto reply = m_chatClient->SetChatPresence(request);
    auto *replyPtr = reply.get();
    m_setChatPresenceReplies.insert(chatId, std::move(reply));

    if (composing) {
        m_localComposingChatId = chatId;
    } else {
        m_localComposingChatId.clear();
    }

    connect(replyPtr, &QGrpcCallReply::finished, this, [this, replyPtr, chatId](const QGrpcStatus &) {
        auto it = m_setChatPresenceReplies.find(chatId);
        if (it == m_setChatPresenceReplies.end() || it.value().get() != replyPtr) {
            return;
        }

        m_setChatPresenceReplies.erase(it);
    });
}

void AppController::ensureFrontendSession()
{
    if (!m_frontendClient || m_frontendSessionStream || m_frontendSessionId.isEmpty()) {
        return;
    }

    HoldSessionRequest request;
    request.setClientName(applicationDisplayName());
    request.setSessionId(m_frontendSessionId);

    m_frontendSessionStream = m_frontendClient->HoldSession(request);
    auto *stream = m_frontendSessionStream.get();
    updateFrontendSessionState();

    connect(stream, &QGrpcServerStream::finished, this, [this, stream](const QGrpcStatus &status) {
        if (m_frontendSessionStream.get() != stream) {
            return;
        }

        m_frontendSessionStream.reset();
        if (!status.isOk()) {
            handleTransportFailure(i18nc("@info", "Frontend session stream ended"), status.message());
            return;
        }

        scheduleRetry();
    });
}

void AppController::updateFrontendSessionState()
{
    if (!m_frontendClient || m_frontendSessionId.isEmpty() || !m_updateSessionStateTimer) {
        return;
    }

    m_updateSessionStateTimer->start();
}

void AppController::sendFrontendSessionState()
{
    if (!m_frontendClient || m_frontendSessionId.isEmpty()) {
        return;
    }

    UpdateSessionStateRequest request;
    request.setSessionId(m_frontendSessionId);
    request.setFocused(QGuiApplication::applicationState() == Qt::ApplicationActive);
    request.setActiveChatId(m_selectedChatId);

    m_updateSessionStateReply.reset();
    m_updateSessionStateReply = m_frontendClient->UpdateSessionState(request);
    auto *reply = m_updateSessionStateReply.get();
    if (!reply) {
        return;
    }
    connect(reply, &QGrpcCallReply::finished, this, [this, reply](const QGrpcStatus &) {
        if (m_updateSessionStateReply.get() == reply) {
            m_updateSessionStateReply.reset();
        }
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
        case whatevr::v1::DaemonEvent::PayloadFields::ChatPresenceChanged:
            applyChatPresenceChanged(event->chatPresenceChanged());
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
        case whatevr::v1::DaemonEvent::PayloadFields::HistoryBackfilled:
            applyHistoryBackfilled(event->historyBackfilled());
            break;
        case whatevr::v1::DaemonEvent::PayloadFields::MediaDownloadChanged:
            applyMediaDownloadChanged(event->mediaDownloadChanged());
            break;
        case whatevr::v1::DaemonEvent::PayloadFields::AvatarUpdated:
            applyAvatarUpdated(event->avatarUpdated());
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

void AppController::applyAvatarUpdated(const AvatarUpdated &update)
{
    if (!update.hasAvatar()) {
        return;
    }

    const auto &avatar = update.avatar();
    const QString &id = avatar.id_proto();
    const QString &localPath = avatar.localPath();
    if (id.isEmpty()) {
        return;
    }

    bool chatAvatarChanged = false;
    bool messageAvatarChanged = false;
    switch (avatar.kind()) {
    case AvatarSubjectKind::AVATAR_SUBJECT_KIND_CHAT:
        chatAvatarChanged = m_chatListModel->updateAvatar(id, localPath);
        if (id == m_selectedChatId && m_selectedChatAvatarLocalPath != localPath) {
            m_selectedChatAvatarLocalPath = localPath;
            Q_EMIT selectionChanged();
        }
        break;
    case AvatarSubjectKind::AVATAR_SUBJECT_KIND_SENDER:
        messageAvatarChanged = m_messageListModel->updateSenderAvatar(id, localPath);
        for (auto &cachedMessages : m_messageCache) {
            for (auto &message : cachedMessages.messages) {
                if (message.senderId() == id && message.senderAvatarLocalPath() != localPath) {
                    message.setSenderAvatarLocalPath(localPath);
                }
            }
        }
        break;
    default:
        break;
    }

    if (chatAvatarChanged) {
        Q_EMIT chatsChanged();
    }
    if (messageAvatarChanged) {
        Q_EMIT messagesChanged();
    }
}

void AppController::applyChatPresenceChanged(const ChatPresenceChanged &presence)
{
    const int availability = static_cast<int>(presence.availability());
    if (presence.chatId() != m_selectedChatId) {
        return;
    }

    bool changed = false;
    if (availability == 0 && m_selectedChatComposing != presence.isComposing()) {
        m_selectedChatComposing = presence.isComposing();
        changed = true;
    }
    if (availability != 0 && m_selectedChatAvailability != availability) {
        m_selectedChatAvailability = availability;
        changed = true;
    }
    if (presence.lastSeenUnix() > 0 && m_selectedChatLastSeenUnix != presence.lastSeenUnix()) {
        m_selectedChatLastSeenUnix = presence.lastSeenUnix();
        changed = true;
    }

    if (changed) {
        Q_EMIT selectionChanged();
    }
}

void AppController::applyMediaDownloadChanged(const MediaDownloadChanged &download)
{
    const QString messageId = download.messageId().trimmed();
    if (messageId.isEmpty()) {
        return;
    }

    const bool wasDownloading = m_mediaDownloadingMessageIds.contains(messageId);
    if (download.downloading()) {
        m_mediaDownloadingMessageIds.insert(messageId);
    } else {
        m_mediaDownloadingMessageIds.remove(messageId);
    }

    const QString errorText = download.downloading() ? QString() : download.errorText();
    m_messageListModel->setMediaDownloadState(messageId, download.downloading(), errorText);

    if (wasDownloading != download.downloading()) {
        Q_EMIT mediaDownloadChanged(messageId);
    }
    if (!download.downloading() && !download.errorText().isEmpty()) {
        Q_EMIT mediaDownloadFailed(messageId, download.errorText());
    }
}

void AppController::applyMessageEvent(const whatevr::v1::Message &message)
{
    auto cached = m_messageCache.find(message.chatId());
    if (cached != m_messageCache.end()) {
        int existingIndex = -1;
        for (int i = 0; i < cached->messages.size(); ++i) {
            if (cached->messages.at(i).id_proto() == message.id_proto()) {
                existingIndex = i;
                break;
            }
        }
        if (existingIndex >= 0) {
            cached->messages[existingIndex] = message;
        } else {
            cached->messages.append(message);
            const int cachedMessageLimit = message.chatId() == m_selectedChatId ? kCachedMessagesPerChatLimit : kMessageLimit;
            while (cached->messages.size() > cachedMessageLimit) {
                cached->messages.removeFirst();
            }
        }
    }

    if (message.chatId() != m_selectedChatId) {
        return;
    }
    if (m_displayedMessagesChatId != message.chatId()) {
        return;
    }

    const bool wasEmpty = m_messageListModel->isEmpty();
    const int previousMessageCount = m_messageListModel->messageCount();
    m_messageListModel->upsertMessage(message);
    if (m_messageListModel->messageCount() > previousMessageCount
        && message.direction() == whatevr::v1::MessageDirectionGadget::MessageDirection::MESSAGE_DIRECTION_OUTGOING) {
        Q_EMIT outgoingMessageAddedToSelectedChat();
    }
    if (wasEmpty) {
        Q_EMIT messagesChanged();
    }
    if (message.direction() == whatevr::v1::MessageDirectionGadget::MessageDirection::MESSAGE_DIRECTION_INCOMING) {
        requestSelectedChatReadIfActive();
    }
}

void AppController::cacheMessages(const QString &chatId, const QList<whatevr::v1::Message> &messages, bool canLoadOlderMessages)
{
    if (chatId.isEmpty()) {
        return;
    }

    QList<whatevr::v1::Message> cachedMessages = messages;
    const bool truncatedCachedMessages = cachedMessages.size() > kCachedMessagesPerChatLimit;
    if (truncatedCachedMessages) {
        cachedMessages = cachedMessages.mid(cachedMessages.size() - kCachedMessagesPerChatLimit);
    }

    m_messageCache.insert(chatId, CachedMessages {
        .messages = cachedMessages,
        .canLoadOlderMessages = canLoadOlderMessages || truncatedCachedMessages,
    });
    m_messageCacheOrder.removeAll(chatId);
    m_messageCacheOrder.append(chatId);

    while (m_messageCacheOrder.size() > kCachedChatLimit) {
        const QString evictChatId = m_messageCacheOrder.takeFirst();
        if (evictChatId == m_selectedChatId) {
            m_messageCacheOrder.append(evictChatId);
            if (m_messageCacheOrder.size() <= kCachedChatLimit) {
                break;
            }
            continue;
        }
        m_messageCache.remove(evictChatId);
    }
}

bool AppController::restoreCachedMessages(const QString &chatId)
{
    if (chatId.isEmpty()) {
        m_messageListModel->clear();
        m_displayedMessagesChatId.clear();
        m_canLoadOlderMessages = false;
        return false;
    }

    const auto cached = m_messageCache.constFind(chatId);
    if (cached == m_messageCache.constEnd()) {
        return false;
    }

    m_displayedMessagesChatId = chatId;
    m_messageListModel->replaceMessages(cached->messages);
    m_canLoadOlderMessages = cached->canLoadOlderMessages;
    return true;
}

void AppController::scheduleSelectedChatMessageReload(const QString &chatId)
{
    if (chatId.isEmpty() || m_selectedChatReloadTimer == nullptr) {
        return;
    }
    m_pendingSelectedChatReloadId = chatId;
    m_selectedChatReloadTimer->setInterval(m_historySyncVisible ? 1000 : 300);
    m_selectedChatReloadTimer->start();
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

    if (progress.syncType() == whatevr::v1::HistorySyncTypeGadget::HistorySyncType::HISTORY_SYNC_TYPE_PROFILE_PICTURE) {
        m_historySyncDetail = i18nc("@info", "%1 of %2 profile pictures", progress.conversationsInChunk(), progress.messagesInChunk());
    } else if (progress.syncType() == whatevr::v1::HistorySyncTypeGadget::HistorySyncType::HISTORY_SYNC_TYPE_OFFLINE_CATCHUP) {
        const QString messagesText = progress.messagesInChunk() > 0
            ? i18nc("@info", "%1/%2 messages", progress.processedMessages(), progress.messagesInChunk())
            : i18ncp("@info", "%1 message", "%1 messages", progress.processedMessages());
        const QString eventsText = progress.conversationsInChunk() > 0
            ? i18nc("@info", "%1/%2 events", progress.processedConversations(), progress.conversationsInChunk())
            : i18ncp("@info", "%1 event", "%1 events", progress.processedConversations());
        m_historySyncDetail = i18nc("@info", "%1 · %2", messagesText, eventsText);
    } else {
        const QString chunkText = progress.chunkOrder() > 0
            ? i18nc("@info", "Chunk %1", progress.chunkOrder())
            : i18nc("@info", "Processing chunk");
        switch (progress.phase()) {
        case whatevr::v1::HistorySyncPhaseGadget::HistorySyncPhase::HISTORY_SYNC_PHASE_QUEUED:
            m_historySyncDetail = i18nc("@info", "%1 · Queued", chunkText);
            break;
        case whatevr::v1::HistorySyncPhaseGadget::HistorySyncPhase::HISTORY_SYNC_PHASE_DOWNLOADING:
            m_historySyncDetail = i18nc("@info", "%1 · Downloading", chunkText);
            break;
        case whatevr::v1::HistorySyncPhaseGadget::HistorySyncPhase::HISTORY_SYNC_PHASE_PROCESSING:
        case whatevr::v1::HistorySyncPhaseGadget::HistorySyncPhase::HISTORY_SYNC_PHASE_UNSPECIFIED:
        default: {
            QStringList details;
            details << chunkText;
            if (progress.conversationsInChunk() > 0) {
                details << i18nc("@info", "%1/%2 conversations", progress.processedConversations(), progress.conversationsInChunk());
            }
            if (progress.messagesInChunk() > 0) {
                details << i18nc("@info", "%1/%2 messages", progress.processedMessages(), progress.messagesInChunk());
            }
            if (details.size() == 1) {
                details << i18nc("@info", "Processing");
            }
            m_historySyncDetail = details.join(i18nc("@info list separator", " · "));
            break;
        }
        }
    }

    Q_EMIT historySyncChanged();
}

void AppController::applyHistoryBackfilled(const whatevr::v1::HistoryBackfilled &backfilled)
{
    const QString chatId = backfilled.chatId().trimmed();
    if (chatId.isEmpty()) {
        return;
    }

    if (m_selectedChatId == chatId) {
        if (!m_messagesLoading) {
            scheduleSelectedChatMessageReload(chatId);
        }
        return;
    }

    m_messageCache.remove(chatId);
    m_messageCacheOrder.removeAll(chatId);
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
        m_selectedChatIsGroup = false;
        m_messageListModel->setGroupChat(false);
        m_selectedChatComposing = false;
        m_selectedChatAvailability = 0;
        m_selectedChatLastSeenUnix = 0;
        return;
    }

    m_selectedChatName = m_chatListModel->chatName(m_selectedChatId);
    m_selectedChatAvatarLocalPath = m_chatListModel->chatAvatarLocalPath(m_selectedChatId);
    m_selectedChatIsGroup = m_chatListModel->chatIsGroup(m_selectedChatId);
    m_messageListModel->setGroupChat(m_selectedChatIsGroup);
}
