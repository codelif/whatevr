#include "appcontroller.h"

#include <QClipboard>
#include <QDir>
#include <QDateTime>
#include <QFile>
#include <QFileInfo>
#include <QFileSystemWatcher>
#include <QGuiApplication>
#include <QImage>
#include <QLocalSocket>
#include <QMimeData>
#include <QProcess>
#include <QQmlEngine>
#include <QLocale>
#include <QStandardPaths>
#include <QTextBoundaryFinder>
#include <QTextDocument>
#include <QTimer>
#include <QUrl>
#include <QUuid>

#include <KLocalizedString>

#include <QtGrpc/qgrpccalloptions.h>
#include <QtGrpc/qgrpccallreply.h>
#include <QtGrpc/qgrpchttp2channel.h>
#include <QtGrpc/qtgrpcnamespace.h>
#include <QtGrpc/qgrpcstream.h>

#include <algorithm>
#include <chrono>
#include <optional>

#include "../models/chatlistmodel.h"
#include "../models/emojimodel.h"
#include "../models/messagelistmodel.h"
#include "../models/pinnedmessagesmodel.h"
#include "../models/searchresultsmodel.h"
#include "../models/starredmessagesmodel.h"
#include "stickercontroller.h"
#include "whatevr/v1/whatevr.qpb.h"
#include "whatevr/v1/whatevr_client.grpc.qpb.h"

#include "messagemarkup.h"
#include "richtext.h"

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
using whatevr::v1::SetChatArchivedRequest;
using whatevr::v1::SetChatMutedRequest;
using whatevr::v1::SetChatPinnedRequest;
using whatevr::v1::SetChatPresenceRequest;
using whatevr::v1::SubscribeChatPresenceRequest;
using whatevr::v1::DownloadMessageMediaRequest;
using whatevr::v1::DownloadMessageMediaResponse;
using whatevr::v1::DeleteMessageForMeRequest;
using whatevr::v1::GetMessageInfoRequest;
using whatevr::v1::RevokeMessageRequest;
using whatevr::v1::RevokeMessageResponse;
using whatevr::v1::EditMessageRequest;
using whatevr::v1::EditMessageResponse;
using whatevr::v1::SendReactionRequest;
using whatevr::v1::SendReactionResponse;
using whatevr::v1::SetMessageStarredRequest;
using whatevr::v1::SetMessageStarredResponse;
using whatevr::v1::PinMessageRequest;
using whatevr::v1::PinMessageResponse;
using whatevr::v1::ListStarredMessagesRequest;
using whatevr::v1::ListStarredMessagesResponse;
using whatevr::v1::ListPinnedMessagesRequest;
using whatevr::v1::ListPinnedMessagesResponse;
using whatevr::v1::SearchChatsRequest;
using whatevr::v1::SearchChatsResponse;
using whatevr::v1::SearchMessagesRequest;
using whatevr::v1::SearchMessagesResponse;
using whatevr::v1::CheckPhoneOnWhatsAppRequest;
using whatevr::v1::CheckPhoneOnWhatsAppResponse;
using whatevr::v1::EnsureDirectChatRequest;
using whatevr::v1::EnsureDirectChatResponse;
using whatevr::v1::GetContactInfoRequest;
using whatevr::v1::GetContactInfoResponse;
using whatevr::v1::GetGroupInfoRequest;
using whatevr::v1::GetGroupInfoResponse;
using whatevr::v1::FetchProfilePictureRequest;
using whatevr::v1::FetchProfilePictureResponse;
using whatevr::v1::ForwardMessageRequest;
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
// Hard GetMessages page cap enforced by the daemon (rpc normalizePage).
constexpr int kServerMessageLimit = 200;
// Extra already-read messages requested above an unread region so the unread
// divider opens with a little context above it.
constexpr int kUnreadContextMessages = 12;
constexpr int kCachedChatLimit = 32;
constexpr int kCachedMessagesPerChatLimit = kMessageLimit * 4;

using HistorySyncPhase = whatevr::v1::HistorySyncPhaseGadget::HistorySyncPhase;
using HistorySyncType = whatevr::v1::HistorySyncTypeGadget::HistorySyncType;

// A search query is treated as a phone-number lookup when, after dropping the
// usual punctuation (spaces, dashes, parens, dots) and an optional leading '+',
// it is all digits and within an E.164-ish length. This keeps name searches
// (which contain letters) from hitting the network usync path.
bool looksLikePhoneNumber(const QString &query)
{
    QString digits;
    bool sawPlus = false;
    for (int i = 0; i < query.size(); ++i) {
        const QChar c = query.at(i);
        if (c.isDigit()) {
            digits.append(c);
        } else if (c == QLatin1Char('+') && i == 0) {
            sawPlus = true;
        } else if (c == QLatin1Char(' ') || c == QLatin1Char('-') || c == QLatin1Char('(')
                   || c == QLatin1Char(')') || c == QLatin1Char('.')) {
            continue;
        } else {
            return false;
        }
    }
    Q_UNUSED(sawPlus);
    return digits.size() >= 7 && digits.size() <= 15;
}

bool isSupportedOutboundImageFile(const QString &filePath)
{
    const QString suffix = QFileInfo(filePath).suffix().toLower();
    return suffix == QStringLiteral("png")
        || suffix == QStringLiteral("jpg")
        || suffix == QStringLiteral("jpeg")
        || suffix == QStringLiteral("webp")
        || suffix == QStringLiteral("gif");
}

using whatevr::util::plainTextFromQtRichText;

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
        // Oldest-first here (the cache is trimmed from the front), but the
        // within-second tiebreaker must match the model: sortSeq is the
        // daemon's insertion order; the random id is only a final fallback.
        if (left.sortSeq() != right.sortSeq()) {
            return left.sortSeq() < right.sortSeq();
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

QGrpcCallOptions probeCallOptions()
{
    // Bound only the unary liveness/control calls (GetStatus, Reconnect) so a
    // dead/half-open daemon surfaces as a failure instead of hanging. This must
    // NOT be a channel-wide deadline: that would also force-kill the long-lived
    // server streams every few seconds.
    using namespace std::chrono_literals;
    QGrpcCallOptions options;
    options.setDeadlineTimeout(5s);
    return options;
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

int historySyncPhaseRank(HistorySyncPhase phase)
{
    switch (phase) {
    case HistorySyncPhase::HISTORY_SYNC_PHASE_QUEUED:
        return 1;
    case HistorySyncPhase::HISTORY_SYNC_PHASE_DOWNLOADING:
        return 2;
    case HistorySyncPhase::HISTORY_SYNC_PHASE_PROCESSING:
    case HistorySyncPhase::HISTORY_SYNC_PHASE_UNSPECIFIED:
        return 3;
    case HistorySyncPhase::HISTORY_SYNC_PHASE_COMPLETE:
        return 4;
    }
    return 3;
}

bool isQueuedHistorySyncPhase(HistorySyncPhase phase)
{
    return phase == HistorySyncPhase::HISTORY_SYNC_PHASE_QUEUED;
}

bool isActiveHistorySyncPhase(HistorySyncPhase phase)
{
    return historySyncPhaseRank(phase) >= historySyncPhaseRank(HistorySyncPhase::HISTORY_SYNC_PHASE_DOWNLOADING);
}

bool isAuxiliaryHistorySyncType(HistorySyncType type)
{
    return type == HistorySyncType::HISTORY_SYNC_TYPE_PUSH_NAME
        || type == HistorySyncType::HISTORY_SYNC_TYPE_NON_BLOCKING_DATA;
}

QString syncTypeLabel(HistorySyncType type)
{
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
    m_starredMessagesModel = new StarredMessagesModel(this);
    m_pinnedMessagesModel = new PinnedMessagesModel(this);
    m_searchResultsModel = new SearchResultsModel(this);
    m_stickerController = new StickerController(this);
    connect(m_stickerController, &StickerController::messageSent, this, [this](const whatevr::v1::Message &message) {
        applyMessageEvent(message);
    });
    m_frontendSessionId = QUuid::createUuid().toString(QUuid::WithoutBraces);

    m_retryTimer = new QTimer(this);
    m_retryTimer->setSingleShot(true);
    connect(m_retryTimer, &QTimer::timeout, this, &AppController::refresh);

    m_startupGraceTimer = new QTimer(this);
    m_startupGraceTimer->setSingleShot(true);
    m_startupGraceTimer->setInterval(1000);
    connect(m_startupGraceTimer, &QTimer::timeout, this, [this]() {
        m_startupGrace = false;
        // If the grace window elapsed without connecting, surface the real
        // state (likely "not running") now instead of waiting on a retry tick.
        if (m_phase != Phase::Connected) {
            refresh();
        }
    });

    setupSocketWatcher();

    m_qrTimer = new QTimer(this);
    m_qrTimer->setInterval(1000);
    connect(m_qrTimer, &QTimer::timeout, this, &AppController::updateQrExpiryText);

    // Debounce keystrokes so each character typed in a search box does not fire
    // its own gRPC round-trip.
    m_searchDebounceTimer = new QTimer(this);
    m_searchDebounceTimer->setSingleShot(true);
    m_searchDebounceTimer->setInterval(180);
    connect(m_searchDebounceTimer, &QTimer::timeout, this, &AppController::runSearch);

    m_chatSearchDebounceTimer = new QTimer(this);
    m_chatSearchDebounceTimer->setSingleShot(true);
    m_chatSearchDebounceTimer->setInterval(180);
    connect(m_chatSearchDebounceTimer, &QTimer::timeout, this, &AppController::runChatSearch);

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
        m_pinnedCache.remove(chatId);
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
        // Becoming active does not mark the selected chat read by itself any
        // more; MessageView re-evaluates visibility of the unread region on
        // activation and calls markSelectedChatViewed() when appropriate.
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
    return m_phase == Phase::Connecting;
}

bool AppController::daemonRunning() const
{
    return m_phase != Phase::NotRunning;
}

QString AppController::connectionPhase() const
{
    switch (m_phase) {
    case Phase::Connecting:
        return QStringLiteral("connecting");
    case Phase::Connected:
        return QStringLiteral("connected");
    case Phase::NotRunning:
        return QStringLiteral("not-running");
    case Phase::Error:
        return QStringLiteral("error");
    }
    return QStringLiteral("connecting");
}

QString AppController::daemonInstructions() const
{
    // Cover both supported launch methods (user chose "both"): the systemd user
    // unit and a direct invocation of the binary.
    return i18nc("@info",
                 "Start it with systemd:\n"
                 "    systemctl --user start whatevrd.service\n"
                 "or run it directly:\n"
                 "    whatevrd");
}

QString AppController::daemonServiceCommand() const
{
    return QStringLiteral("systemctl --user start whatevrd.service");
}

QString AppController::daemonBinaryCommand() const
{
    return QStringLiteral("whatevrd");
}

QString AppController::actionError() const
{
    return m_actionError;
}

bool AppController::loginRequired() const
{
    return m_loginRequired;
}

bool AppController::starting() const
{
    // The initial window between launch and the first status reply, before we
    // know anything about the daemon. The shell routes this to a neutral splash
    // so a normal (sub-second) connect never flashes the daemon-status page.
    return m_startupGrace && !m_hasStatus;
}

bool AppController::shellVisible() const
{
    return m_phase == Phase::Connected && !m_loginRequired && m_hasStatus;
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

    switch (m_phase) {
    case Phase::NotRunning:
        return i18nc("@title", "whatevrd isn't running");
    case Phase::Connecting:
        return i18nc("@title", "Connecting to whatevrd");
    case Phase::Connected:
        return shellVisible() ? i18nc("@title", "Daemon session ready")
                              : i18nc("@title", "Waiting for whatevrd");
    case Phase::Error:
        return i18nc("@title", "Can't reach whatevrd");
    }
    return i18nc("@title", "Connecting to whatevrd");
}

QString AppController::statusText() const
{
    if (m_loginRequired) {
        return i18nc("@info", "Use WhatsApp on your phone to scan the QR code below.");
    }

    switch (m_phase) {
    case Phase::NotRunning:
        return i18nc("@info", "The background daemon isn't running. Start it and Whatevr will connect automatically.");
    case Phase::Connecting:
        return i18nc("@info", "Preparing the local daemon connection and reading the current session state.");
    case Phase::Connected:
        return shellVisible()
            ? i18nc("@info", "The daemon is reachable. Chat list and timeline work land next on top of this shell.")
            : i18nc("@info", "Connected to the daemon; waiting for it to come online.");
    case Phase::Error:
        return i18nc("@info", "Whatevr could not reach the daemon. Retrying automatically.");
    }
    return i18nc("@info", "Preparing the local daemon connection and reading the current session state.");
}

QString AppController::detailText() const
{
    QStringList lines;

    // The daemon-reported state/detail is only meaningful while we're actually
    // connected; once the link drops it's stale, so don't show it.
    if (m_phase == Phase::Connected) {
        if (!m_daemonStateLabel.isEmpty()) {
            lines << i18nc("@info", "State: %1", m_daemonStateLabel);
        }

        if (!m_statusDetail.isEmpty()) {
            lines << m_statusDetail;
        }
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
    // Only offer the daemon-side Reconnect RPC when we actually have a live
    // connection to send it on; otherwise the button rebuilds the channel.
    if (m_phase == Phase::Connected && m_canReconnect && !m_loginRequired) {
        return i18nc("@action:button", "Reconnect");
    }
    return i18nc("@action:button", "Retry");
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

QAbstractItemModel *AppController::starredMessagesModel() const
{
    return m_starredMessagesModel;
}

QAbstractItemModel *AppController::pinnedMessagesModel() const
{
    return m_pinnedMessagesModel;
}

QAbstractItemModel *AppController::searchResultsModel() const
{
    return m_searchResultsModel;
}

QString AppController::searchQuery() const
{
    return m_searchQuery;
}

bool AppController::searchActive() const
{
    return !m_searchQuery.trimmed().isEmpty();
}

bool AppController::searchBusy() const
{
    return m_searchBusy;
}

bool AppController::chatSearchActive() const
{
    return m_chatSearchActive;
}

QString AppController::chatSearchQuery() const
{
    return m_chatSearchQuery;
}

int AppController::chatSearchMatchCount() const
{
    return static_cast<int>(m_chatSearchMatchIds.size());
}

int AppController::chatSearchCurrentIndex() const
{
    return m_chatSearchIndex < 0 ? 0 : m_chatSearchIndex + 1;
}

QString AppController::chatSearchActiveMessageId() const
{
    if (m_chatSearchIndex < 0 || m_chatSearchIndex >= m_chatSearchMatchIds.size()) {
        return QString();
    }
    return m_chatSearchMatchIds.at(m_chatSearchIndex);
}

QAbstractItemModel *AppController::emojiModel() const
{
    return m_emojiModel;
}

QObject *AppController::stickers() const
{
    return m_stickerController;
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

int AppController::selectedChatUnreadCount() const
{
    return m_selectedChatUnreadCount;
}

QString AppController::unreadAnchorMessageId() const
{
    return m_unreadAnchorMessageId;
}

int AppController::unreadAnchorCount() const
{
    return m_unreadAnchorCount;
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

    // If the daemon socket isn't there, the daemon isn't running. Surface that
    // directly instead of building a channel and hanging on "Connecting"; the
    // file watcher (and retry timer) reconnect once the socket appears.
    if (!daemonSocketExists()) {
        // During the cold-start grace window the daemon may simply not have
        // created its socket yet; stay on "Connecting" instead of flashing
        // "not running". The socket watcher reconnects the moment it appears.
        if (deferStartupConnect()) {
            return;
        }
        enterNotRunning();
        return;
    }

    // The socket file existing doesn't mean anyone is listening: a SIGKILLed or
    // crashed daemon leaves a stale socket behind. Probe it before building the
    // gRPC channel, since a connection-refused on a dead unix:// socket doesn't
    // reliably surface through QtGrpc and would strand us on "Connecting".
    clearBanner();
    m_phase = Phase::Connecting;
    emitStateChanged();
    probeAndConnect();
}

// Run a quick connect-only liveness probe against the daemon socket. A stale
// socket refuses the connection immediately; a live listener accepts it at the
// kernel level even before the daemon calls accept(). On success we build the
// real channel; on failure we fall through to the "not running" page.
void AppController::probeAndConnect()
{
    if (!m_probeSocket) {
        m_probeSocket = new QLocalSocket(this);
        connect(m_probeSocket, &QLocalSocket::connected, this, [this] {
            m_probeSocket->abort();
            startSession();
        });
        connect(m_probeSocket, &QLocalSocket::errorOccurred, this, [this](QLocalSocket::LocalSocketError) {
            m_probeSocket->abort();
            if (deferStartupConnect()) {
                return;
            }
            enterNotRunning();
        });
    }

    // Cancel any in-flight probe so a stale callback can't drive a later refresh.
    m_probeSocket->abort();
    m_probeSocket->connectToServer(daemonSocketPath());
}

// Build the channel and start the status probe + event streams. Split out of
// refresh() so the liveness probe can defer it until the socket answers.
void AppController::startSession()
{
    if (!ensureChannel()) {
        return;
    }

    requestStatus();
    ensureFrontendSession();
    ensureDaemonStream();
    ensureLoginStream();
}

// While the daemon is first coming up, the socket can be missing, the liveness
// probe can be refused, and the freshly built channel's RPCs can come back
// Unavailable for a beat — all transient. Treat them as "still connecting" and
// retry during the grace window so the UI never flashes the "not running" page
// before the first successful connect. Cleared the moment a status arrives.
bool AppController::deferStartupConnect()
{
    if (!m_startupGrace) {
        return false;
    }
    m_phase = Phase::Connecting;
    clearBanner();
    emitStateChanged();
    refreshSocketWatch();
    scheduleRetry(250);
    return true;
}

// Drop to the "whatevrd isn't running" page and arm a retry. Shared by the
// absent-socket fast path, the stale-socket probe failure, and transport drops.
void AppController::enterNotRunning()
{
    resetChannel();
    m_phase = Phase::NotRunning;
    m_canReconnect = false;
    m_daemonStateLabel.clear();
    m_statusDetail.clear();
    clearBanner();
    refreshSocketWatch();
    emitStateChanged();
    scheduleRetry();
}

void AppController::triggerPrimaryAction()
{
    if (m_phase == Phase::Connected && m_canReconnect && !m_loginRequired) {
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

    // Drop any deferred "show in chat" jump unless this selection is the one it
    // asked for (showMessageInChat sets the pending target, then calls us).
    if (chatId != m_pendingJumpChatId) {
        m_pendingJumpChatId.clear();
        m_pendingJumpMessageId.clear();
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

    // The in-chat search is scoped to one conversation; switching chats ends it.
    if (m_chatSearchActive || !m_chatSearchQuery.isEmpty() || !m_chatSearchMatchIds.isEmpty()) {
        resetChatSearch();
        m_chatSearchActive = false;
        Q_EMIT chatSearchChanged();
    }

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
    // Capture the unread state as it was at open: the divider anchor derives
    // from this snapshot and stays put even as the badge later clears or new
    // messages arrive while the chat stays open.
    m_selectedChatUnreadSnapshot = chatId.isEmpty() ? 0 : m_chatListModel->chatUnreadCount(chatId);
    m_unreadAnchorMessageId.clear();
    m_unreadAnchorCount = 0;
    m_unreadAnchorResolved = false;
    Q_EMIT unreadAnchorChanged();
    updateSelectedChatData();
    const bool restoredMessages = restoreCachedMessages(chatId);
    if (restoredMessages) {
        m_messagesLoading = false;
        m_messagesLoadingChatId.clear();
        resolveUnreadAnchor(false);
    }
    Q_EMIT selectionChanged();
    Q_EMIT messagesChanged();
    Q_EMIT composerChanged();
    updateFrontendSessionState();

    // Prime the pinned-message banner for the chat we're switching into. When a
    // previous open cached its pins, restore them synchronously so the banner is
    // present from the first frame (no late reflow / flicker); the async load
    // below then refreshes. Only fall back to clearing when nothing is cached.
    if (const auto cachedPins = m_pinnedCache.constFind(m_selectedChatId);
            !m_selectedChatId.isEmpty() && cachedPins != m_pinnedCache.constEnd()) {
        m_pinnedMessagesModel->replace(cachedPins.value());
    } else {
        m_pinnedMessagesModel->clear();
    }
    if (!m_selectedChatId.isEmpty()) {
        requestSelectedChatPresence();
        requestMessages(m_selectedChatId);
        loadPinnedMessages(m_selectedChatId);
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

void AppController::showMessageInChat(const QString &chatId, const QString &messageId)
{
    const QString trimmedChat = chatId.trimmed();
    const QString trimmedMessage = messageId.trimmed();
    if (trimmedChat.isEmpty() || trimmedMessage.isEmpty()) {
        return;
    }

    if (m_selectedChatId == trimmedChat) {
        // Already open; jump straight away (loads context around it if needed).
        jumpToMessage(trimmedMessage);
        return;
    }

    // Defer the jump until the chat's first page lands; selectChat kicks off the
    // load, requestMessages consumes the pending jump on success.
    m_pendingJumpChatId = trimmedChat;
    m_pendingJumpMessageId = trimmedMessage;
    selectChat(trimmedChat);
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
    const QString trimmed = plainTextFromQtRichText(text).trimmed();
    if (!m_sendClient || m_sendTextReply || m_selectedChatId.isEmpty() || trimmed.isEmpty()) {
        return;
    }

    setChatComposing(m_selectedChatId, false);
    m_chatListModel->setChatDraft(m_selectedChatId, QString());
    dismissUnreadAnchor();

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
    m_chatListModel->setChatDraft(m_selectedChatId, QString());
    dismissUnreadAnchor();

    SendMediaRequest request;
    request.setChatId(m_selectedChatId);
    request.setFilePath(filePath);
    request.setCaption(plainTextFromQtRichText(caption).trimmed());
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
            handleTransportFailure(i18nc("@info", "Logout failed"), status.message(), status.code());
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
        resetHistorySyncDisplay();
        m_messagesLoading = false;
        m_olderMessagesLoading = false;
        m_canLoadOlderMessages = false;
        m_chatListModel->replaceChats({});
        m_messageListModel->clear();
        m_displayedMessagesChatId.clear();
        m_messageCache.clear();
        m_messageCacheOrder.clear();
        m_pinnedCache.clear();
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
    m_startupGraceTimer->start();
    refresh();
}

bool AppController::ensureChannel()
{
    if (m_channel) {
        return true;
    }

    const QString socketUrl = daemonSocketUrl();
    if (socketUrl.isEmpty()) {
        m_phase = Phase::Error;
        m_hasStatus = false;
        m_bannerText = i18nc("@info", "XDG runtime directory is unavailable, so the daemon socket cannot be resolved.");
        emitStateChanged();
        return false;
    }

    // No channel-wide deadline: that would also bound the long-lived server
    // streams and force-kill them every few seconds. The liveness bound lives
    // on the unary probe instead (see probeCallOptions() / requestStatus()).
    auto channel = std::make_shared<QGrpcHttp2Channel>(QUrl(socketUrl));
    m_channel = channel;
    attachClients();
    return true;
}

void AppController::resetChannel()
{
    // Drop the live streams and in-flight status/reconnect calls so the next
    // ensureChannel() builds a fresh channel; QtGrpc does not transparently
    // recover a channel whose peer (the daemon) went away and came back.
    m_daemonStream.reset();
    m_loginStream.reset();
    m_frontendSessionStream.reset();
    m_statusReply.reset();
    m_reconnectReply.reset();
    m_reconnectInFlight = false;
    m_channel.reset();
    if (resetHistorySyncDisplay()) {
        Q_EMIT historySyncChanged();
    }
}

bool AppController::daemonSocketExists() const
{
    const QString path = daemonSocketPath();
    return !path.isEmpty() && QFileInfo::exists(path);
}

void AppController::setupSocketWatcher()
{
    m_socketWatcher = new QFileSystemWatcher(this);
    connect(m_socketWatcher, &QFileSystemWatcher::directoryChanged, this, [this]() {
        refreshSocketWatch();
        // The socket (re)appeared while we were not connected — connect now
        // rather than waiting for the next retry tick.
        if (m_phase != Phase::Connected && daemonSocketExists()) {
            refresh();
        }
    });
    refreshSocketWatch();
}

void AppController::refreshSocketWatch()
{
    if (!m_socketWatcher) {
        return;
    }

    const QString socketPath = daemonSocketPath();
    if (socketPath.isEmpty()) {
        return;
    }

    // Watch the daemon's runtime directory when it exists (to catch the socket
    // appearing), otherwise watch its parent so we notice the directory itself
    // being created on first daemon start.
    const QDir socketDir = QFileInfo(socketPath).dir();
    QStringList wanted;
    if (socketDir.exists()) {
        wanted << socketDir.absolutePath();
    }
    const QString parent = QFileInfo(socketDir.absolutePath()).dir().absolutePath();
    if (!parent.isEmpty() && QFileInfo::exists(parent)) {
        wanted << parent;
    }

    const QStringList current = m_socketWatcher->directories();
    for (const QString &dir : wanted) {
        if (!current.contains(dir)) {
            m_socketWatcher->addPath(dir);
        }
    }
}

void AppController::startDaemon()
{
    // Prefer the systemd user unit; fall back to launching the binary directly
    // when systemctl is missing or the unit isn't installed/failed to start.
    // Either way the file watcher picks up the socket and connects us.
    m_actionError.clear();

    auto *proc = new QProcess(this);
    // Both handlers can fire for the same process; whichever lands first owns the
    // outcome, so disconnect from `this` immediately to keep the fallback (and
    // the deleteLater) from running twice.
    connect(proc, &QProcess::finished, this, [this, proc](int exitCode, QProcess::ExitStatus exitStatus) {
        proc->disconnect(this);
        if (exitStatus != QProcess::NormalExit || exitCode != 0) {
            launchDaemonBinary();
        }
        proc->deleteLater();
    });
    connect(proc, &QProcess::errorOccurred, this, [this, proc](QProcess::ProcessError) {
        proc->disconnect(this);
        launchDaemonBinary();
        proc->deleteLater();
    });
    proc->start(QStringLiteral("systemctl"),
                {QStringLiteral("--user"), QStringLiteral("start"), QStringLiteral("whatevrd.service")});

    m_bannerText = i18nc("@info", "Starting whatevrd…");
    m_phase = Phase::Connecting;
    emitStateChanged();
    refreshSocketWatch();
    scheduleRetry(500);
}

void AppController::launchDaemonBinary()
{
    if (QProcess::startDetached(QStringLiteral("whatevrd"), {})) {
        return;
    }

    // Neither the systemd unit nor the binary on PATH could be started, so the
    // user's click produced nothing visible. Surface a sticky error and drop
    // back to the "not running" page with its manual instructions.
    m_phase = Phase::NotRunning;
    m_canReconnect = false;
    m_actionError = i18nc("@info",
                          "Couldn't start whatevrd automatically — the systemd service isn't "
                          "installed and the whatevrd binary wasn't found in PATH. Start it "
                          "manually using the commands below.");
    emitStateChanged();
}

void AppController::copyToClipboard(const QString &text)
{
    if (QClipboard *clipboard = QGuiApplication::clipboard()) {
        clipboard->setText(text);
    }
}

void AppController::copyImageToClipboard(const QString &localPath)
{
    if (localPath.isEmpty()) {
        return;
    }
    const QImage image(localPath);
    if (image.isNull()) {
        Q_EMIT messageActionFailed(i18nc("@info", "Unable to copy the image"));
        return;
    }
    if (QClipboard *clipboard = QGuiApplication::clipboard()) {
        clipboard->setImage(image);
    }
}

bool AppController::saveMediaAs(const QString &localPath, const QUrl &destUrl)
{
    if (localPath.isEmpty() || !destUrl.isLocalFile()) {
        return false;
    }
    const QString destination = destUrl.toLocalFile();
    if (destination.isEmpty()) {
        return false;
    }
    // The save dialog already confirmed overwriting; QFile::copy refuses to.
    if (QFile::exists(destination) && !QFile::remove(destination)) {
        Q_EMIT messageActionFailed(i18nc("@info", "Unable to overwrite the existing file"));
        return false;
    }
    if (!QFile::copy(localPath, destination)) {
        Q_EMIT messageActionFailed(i18nc("@info", "Unable to save the file"));
        return false;
    }
    return true;
}

QString AppController::toCommonMark(const QString &text) const
{
    return whatevr::util::whatsAppToCommonMark(text);
}

void AppController::deleteMessageForMe(const QString &messageId)
{
    if (!m_chatClient || messageId.isEmpty()) {
        return;
    }

    DeleteMessageForMeRequest request;
    request.setMessageId(messageId);

    auto reply = m_chatClient->DeleteMessageForMe(request);
    auto *replyPtr = reply.get();
    m_deleteMessageReplies.insert(messageId, std::move(reply));

    connect(replyPtr, &QGrpcCallReply::finished, this, [this, replyPtr, messageId](const QGrpcStatus &status) {
        auto it = m_deleteMessageReplies.find(messageId);
        if (it == m_deleteMessageReplies.end() || it.value().get() != replyPtr) {
            return;
        }
        m_deleteMessageReplies.erase(it);
        if (!status.isOk()) {
            Q_EMIT messageActionFailed(status.message().isEmpty() ? i18nc("@info", "Unable to delete the message") : status.message());
        }
    });
}

void AppController::revokeMessage(const QString &messageId)
{
    if (!m_sendClient || messageId.isEmpty()) {
        return;
    }

    RevokeMessageRequest request;
    request.setMessageId(messageId);

    auto reply = m_sendClient->RevokeMessage(request);
    auto *replyPtr = reply.get();
    // Keyed per message so a multi-select revoke keeps every in-flight call
    // alive; a single shared reply would be overwritten on each loop iteration
    // and only the last message would actually be revoked.
    m_revokeMessageReplies.insert(messageId, std::move(reply));

    connect(replyPtr, &QGrpcCallReply::finished, this, [this, replyPtr, messageId](const QGrpcStatus &status) {
        auto it = m_revokeMessageReplies.find(messageId);
        if (it == m_revokeMessageReplies.end() || it.value().get() != replyPtr) {
            return;
        }
        const auto reply = it.value();
        m_revokeMessageReplies.erase(it);
        if (!status.isOk()) {
            Q_EMIT messageActionFailed(status.message().isEmpty() ? i18nc("@info", "Unable to delete the message for everyone") : status.message());
            return;
        }
        if (const auto response = reply->read<RevokeMessageResponse>()) {
            // The event stream tombstones it too; applying directly avoids a
            // visible delay between the menu action and the bubble change.
            applyMessageEvent(response->message());
        }
    });
}

bool AppController::canEditAt(qint64 timestampUnix) const
{
    // Mirrors whatsmeow.EditWindow (20 minutes); the daemon is authoritative.
    static constexpr qint64 kEditWindowSeconds = 20 * 60;
    if (timestampUnix <= 0) {
        return false;
    }
    const qint64 nowUnix = QDateTime::currentSecsSinceEpoch();
    return nowUnix - timestampUnix <= kEditWindowSeconds;
}

void AppController::editMessage(const QString &messageId, const QString &newText)
{
    if (!m_sendClient || messageId.isEmpty() || newText.trimmed().isEmpty()) {
        return;
    }

    EditMessageRequest request;
    request.setMessageId(messageId);
    request.setText(newText.trimmed());

    // Reflect the edit in the UI immediately; the daemon round-trip then confirms
    // it (success applies the authoritative message) or the error path reverts to
    // the captured original text/edited flag.
    const bool wasEdited = m_messageListModel->messageSnapshot(messageId).value(QStringLiteral("isEdited")).toBool();
    const QString previousText = m_messageListModel->applyOptimisticEdit(messageId, newText.trimmed());

    auto reply = m_sendClient->EditMessage(request);
    auto *replyPtr = reply.get();
    m_editMessageReplies.insert(messageId, std::move(reply));

    connect(replyPtr, &QGrpcCallReply::finished, this, [this, replyPtr, messageId, previousText, wasEdited](const QGrpcStatus &status) {
        auto it = m_editMessageReplies.find(messageId);
        if (it == m_editMessageReplies.end() || it.value().get() != replyPtr) {
            return;
        }
        const auto reply = it.value();
        m_editMessageReplies.erase(it);
        if (!status.isOk()) {
            // Roll back the optimistic update so the bubble reflects reality again.
            m_messageListModel->restoreText(messageId, previousText, wasEdited);
            Q_EMIT messageActionFailed(status.message().isEmpty() ? i18nc("@info", "Unable to edit the message") : status.message());
            return;
        }
        if (const auto response = reply->read<EditMessageResponse>()) {
            // The event stream also delivers this update; applying directly avoids
            // a visible delay between saving and the bubble settling.
            applyMessageEvent(response->message());
        }
    });
}

void AppController::sendReaction(const QString &messageId, const QString &emoji)
{
    if (!m_sendClient || messageId.isEmpty()) {
        return;
    }
    dismissUnreadAnchor();

    // Reflect the reaction in the UI immediately; the daemon round-trip then
    // confirms it (success path applies the authoritative message) or the error
    // path reverts to this captured state.
    const QVariantList previousReactions = m_messageListModel->applyOptimisticReaction(messageId, emoji);

    // Keyed per message and serialized so reacting in quick succession (emoji
    // A -> B -> remove) reconciles to the last intent instead of racing.
    auto send = [this, messageId, emoji, previousReactions]() {
        SendReactionRequest request;
        request.setMessageId(messageId);
        request.setEmoji(emoji);

        auto reply = m_sendClient->SendReaction(request);
        auto *replyPtr = reply.get();
        m_reactionReplies[messageId].inFlight = std::move(reply);

        connect(replyPtr, &QGrpcCallReply::finished, this, [this, replyPtr, messageId, previousReactions](const QGrpcStatus &status) {
            auto it = m_reactionReplies.find(messageId);
            if (it == m_reactionReplies.end() || it.value().inFlight.get() != replyPtr) {
                return;
            }
            const auto reply = it->inFlight;
            if (!status.isOk()) {
                if (!hasPendingSerial(m_reactionReplies, messageId)) {
                    // Roll back the optimistic update so the pill reflects reality again.
                    m_messageListModel->restoreReactions(messageId, previousReactions);
                    Q_EMIT messageActionFailed(status.message().isEmpty() ? i18nc("@info", "Unable to react to the message") : status.message());
                }
            } else if (const auto response = reply->read<SendReactionResponse>()) {
                // The event stream also delivers this update; applying directly avoids
                // a visible delay between tapping the emoji and the pill appearing.
                applyMessageEvent(response->message());
            }
            finishSerial(m_reactionReplies, messageId);
        });
    };

    enqueueSerial(m_reactionReplies, messageId, std::move(send));
}

void AppController::setMessageStarred(const QString &messageId, bool starred)
{
    if (!m_sendClient || messageId.isEmpty()) {
        return;
    }

    // Flip the bubble's star immediately; the daemon round-trip then confirms
    // (applies the authoritative message) or reverts to the captured state.
    const bool previousStarred = m_messageListModel->applyOptimisticStar(messageId, starred);

    auto send = [this, messageId, starred, previousStarred]() {
        SetMessageStarredRequest request;
        request.setMessageId(messageId);
        request.setStarred(starred);

        auto reply = m_sendClient->SetMessageStarred(request);
        auto *replyPtr = reply.get();
        m_setMessageStarredReplies[messageId].inFlight = std::move(reply);

        connect(replyPtr, &QGrpcCallReply::finished, this, [this, replyPtr, messageId, previousStarred](const QGrpcStatus &status) {
            auto it = m_setMessageStarredReplies.find(messageId);
            if (it == m_setMessageStarredReplies.end() || it.value().inFlight.get() != replyPtr) {
                return;
            }
            const auto reply = it->inFlight;
            if (!status.isOk()) {
                if (!hasPendingSerial(m_setMessageStarredReplies, messageId)) {
                    m_messageListModel->restoreStar(messageId, previousStarred);
                    Q_EMIT messageActionFailed(status.message().isEmpty() ? i18nc("@info", "Unable to star the message") : status.message());
                }
            } else if (const auto response = reply->read<SetMessageStarredResponse>()) {
                applyMessageEvent(response->message());
            }
            finishSerial(m_setMessageStarredReplies, messageId);
        });
    };

    enqueueSerial(m_setMessageStarredReplies, messageId, std::move(send));
}

void AppController::pinMessage(const QString &messageId, int durationSecs)
{
    if (!m_sendClient || messageId.isEmpty() || durationSecs <= 0) {
        return;
    }

    // Show the pin in the banner/bubble immediately; the daemon round-trip then
    // confirms (applies the authoritative message) or reverts to the original.
    std::optional<whatevr::v1::Message> previous;
    whatevr::v1::Message optimistic;
    if (findCachedMessage(messageId, optimistic)) {
        previous = optimistic;
        optimistic.setPinnedUntilUnix(QDateTime::currentSecsSinceEpoch() + durationSecs);
        optimistic.setIsPinned(true);
        applyMessageEvent(optimistic);
    }

    // Pin and unpin share this slot (same pinned-state) so they serialize
    // against each other instead of racing to the daemon.
    auto send = [this, messageId, durationSecs, previous]() {
        PinMessageRequest request;
        request.setMessageId(messageId);
        request.setPinned(true);
        request.setDurationSecs(static_cast<quint32>(durationSecs));

        auto reply = m_sendClient->PinMessage(request);
        auto *replyPtr = reply.get();
        m_pinMessageReplies[messageId].inFlight = std::move(reply);

        connect(replyPtr, &QGrpcCallReply::finished, this, [this, replyPtr, messageId, previous](const QGrpcStatus &status) {
            auto it = m_pinMessageReplies.find(messageId);
            if (it == m_pinMessageReplies.end() || it.value().inFlight.get() != replyPtr) {
                return;
            }
            const auto reply = it->inFlight;
            if (!status.isOk()) {
                if (!hasPendingSerial(m_pinMessageReplies, messageId)) {
                    if (previous) {
                        applyMessageEvent(*previous);
                    }
                    Q_EMIT messageActionFailed(status.message().isEmpty() ? i18nc("@info", "Unable to pin the message") : status.message());
                }
            } else if (const auto response = reply->read<PinMessageResponse>()) {
                applyMessageEvent(response->message());
            }
            finishSerial(m_pinMessageReplies, messageId);
        });
    };

    enqueueSerial(m_pinMessageReplies, messageId, std::move(send));
}

void AppController::unpinMessage(const QString &messageId)
{
    if (!m_sendClient || messageId.isEmpty()) {
        return;
    }

    // Drop the pin from the banner/bubble immediately; the daemon round-trip then
    // confirms or reverts to the original.
    std::optional<whatevr::v1::Message> previous;
    whatevr::v1::Message optimistic;
    if (findCachedMessage(messageId, optimistic)) {
        previous = optimistic;
        optimistic.setPinnedUntilUnix(0);
        optimistic.setIsPinned(false);
        applyMessageEvent(optimistic);
    }

    auto send = [this, messageId, previous]() {
        PinMessageRequest request;
        request.setMessageId(messageId);
        request.setPinned(false);

        auto reply = m_sendClient->PinMessage(request);
        auto *replyPtr = reply.get();
        m_pinMessageReplies[messageId].inFlight = std::move(reply);

        connect(replyPtr, &QGrpcCallReply::finished, this, [this, replyPtr, messageId, previous](const QGrpcStatus &status) {
            auto it = m_pinMessageReplies.find(messageId);
            if (it == m_pinMessageReplies.end() || it.value().inFlight.get() != replyPtr) {
                return;
            }
            const auto reply = it->inFlight;
            if (!status.isOk()) {
                if (!hasPendingSerial(m_pinMessageReplies, messageId)) {
                    if (previous) {
                        applyMessageEvent(*previous);
                    }
                    Q_EMIT messageActionFailed(status.message().isEmpty() ? i18nc("@info", "Unable to unpin the message") : status.message());
                }
            } else if (const auto response = reply->read<PinMessageResponse>()) {
                applyMessageEvent(response->message());
            }
            finishSerial(m_pinMessageReplies, messageId);
        });
    };

    enqueueSerial(m_pinMessageReplies, messageId, std::move(send));
}

void AppController::loadStarredMessages(const QString &chatId)
{
    if (!m_chatClient) {
        return;
    }

    ListStarredMessagesRequest request;
    request.setChatId(chatId);

    auto reply = m_chatClient->ListStarredMessages(request);
    auto *replyPtr = reply.get();
    m_listStarredReply = std::move(reply);

    connect(replyPtr, &QGrpcCallReply::finished, this, [this, replyPtr](const QGrpcStatus &status) {
        if (m_listStarredReply.get() != replyPtr) {
            return;
        }
        const auto reply = std::move(m_listStarredReply);
        if (!status.isOk()) {
            Q_EMIT messageActionFailed(status.message().isEmpty() ? i18nc("@info", "Unable to load starred messages") : status.message());
            return;
        }
        if (const auto response = reply->read<ListStarredMessagesResponse>()) {
            m_starredMessagesModel->replace(response->items());
        }
    });
}

void AppController::loadPinnedMessages(const QString &chatId)
{
    if (!m_chatClient || chatId.isEmpty()) {
        return;
    }

    ListPinnedMessagesRequest request;
    request.setChatId(chatId);

    auto reply = m_chatClient->ListPinnedMessages(request);
    auto *replyPtr = reply.get();
    m_listPinnedReply = std::move(reply);

    connect(replyPtr, &QGrpcCallReply::finished, this, [this, replyPtr, chatId](const QGrpcStatus &status) {
        if (m_listPinnedReply.get() != replyPtr) {
            return;
        }
        const auto reply = std::move(m_listPinnedReply);
        if (!status.isOk()) {
            return;
        }
        // Ignore a stale response if the user has since switched chats.
        if (chatId != m_selectedChatId) {
            return;
        }
        if (const auto response = reply->read<ListPinnedMessagesResponse>()) {
            const auto &messages = response->messages();
            // Skip the replace (and its modelReset/banner reflow) when the
            // refresh matches what the cache already painted on open.
            const auto cached = m_pinnedCache.constFind(chatId);
            if (cached == m_pinnedCache.constEnd() || cached.value() != messages) {
                m_pinnedCache.insert(chatId, messages);
                m_pinnedMessagesModel->replace(messages);
            }
        }
    });
}

void AppController::forwardMessage(const QString &messageId, const QStringList &chatIds)
{
    if (!m_sendClient || messageId.isEmpty() || chatIds.isEmpty()) {
        return;
    }

    ForwardMessageRequest request;
    request.setSourceMessageId(messageId);
    request.setTargetChatIds(chatIds);

    // A multi-select forward dispatches one call per selected message in a
    // synchronous loop, so the hash is empty only at the start of a batch.
    // Track the batch there so the "Forwarded to N chats" toast fires once for
    // the whole selection instead of once per message.
    if (m_forwardMessageReplies.isEmpty()) {
        m_forwardBatchChatCount = static_cast<int>(chatIds.size());
        m_forwardBatchFailed = false;
    }

    auto reply = m_sendClient->ForwardMessage(request);
    auto *replyPtr = reply.get();
    m_forwardMessageReplies.insert(messageId, std::move(reply));

    connect(replyPtr, &QGrpcCallReply::finished, this, [this, replyPtr, messageId](const QGrpcStatus &status) {
        auto it = m_forwardMessageReplies.find(messageId);
        if (it == m_forwardMessageReplies.end() || it.value().get() != replyPtr) {
            return;
        }
        m_forwardMessageReplies.erase(it);
        if (!status.isOk()) {
            if (!m_forwardBatchFailed) {
                m_forwardBatchFailed = true;
                Q_EMIT messageActionFailed(status.message().isEmpty() ? i18nc("@info", "Unable to forward the message") : status.message());
            }
        }
        // Report success once, after the last message in the batch settles.
        if (m_forwardMessageReplies.isEmpty() && !m_forwardBatchFailed) {
            Q_EMIT messageForwarded(m_forwardBatchChatCount);
        }
    });
}

void AppController::setSearchQuery(const QString &query)
{
    if (m_searchQuery == query) {
        return;
    }
    m_searchQuery = query;
    Q_EMIT searchChanged();

    if (query.trimmed().isEmpty()) {
        m_searchDebounceTimer->stop();
        m_searchChatsReply.reset();
        m_searchMessagesReply.reset();
        m_checkPhoneReply.reset();
        m_searchResultsModel->clear();
        if (m_searchBusy) {
            m_searchBusy = false;
            Q_EMIT searchChanged();
        }
        return;
    }
    m_searchDebounceTimer->start();
}

void AppController::clearSearch()
{
    setSearchQuery(QString());
}

void AppController::runSearch()
{
    if (!m_chatClient) {
        return;
    }
    const QString query = m_searchQuery.trimmed();
    if (query.isEmpty()) {
        m_searchResultsModel->clear();
        return;
    }

    m_searchBusy = true;
    Q_EMIT searchChanged();

    {
        SearchChatsRequest request;
        request.setQuery(query);
        auto reply = m_chatClient->SearchChats(request);
        auto *replyPtr = reply.get();
        m_searchChatsReply = std::move(reply);
        connect(replyPtr, &QGrpcCallReply::finished, this, [this, replyPtr](const QGrpcStatus &status) {
            if (m_searchChatsReply.get() != replyPtr) {
                return;
            }
            const auto reply = std::move(m_searchChatsReply);
            if (status.isOk()) {
                if (const auto response = reply->read<SearchChatsResponse>()) {
                    m_searchResultsModel->setChats(response->chats());
                }
            }
            // Clear the spinner once both halves have landed.
            if (!m_searchMessagesReply && m_searchBusy) {
                m_searchBusy = false;
                Q_EMIT searchChanged();
            }
        });
    }
    {
        SearchMessagesRequest request;
        request.setQuery(query);
        auto reply = m_chatClient->SearchMessages(request);
        auto *replyPtr = reply.get();
        m_searchMessagesReply = std::move(reply);
        connect(replyPtr, &QGrpcCallReply::finished, this, [this, replyPtr](const QGrpcStatus &status) {
            if (m_searchMessagesReply.get() != replyPtr) {
                return;
            }
            const auto reply = std::move(m_searchMessagesReply);
            if (status.isOk()) {
                if (const auto response = reply->read<SearchMessagesResponse>()) {
                    m_searchResultsModel->setMessages(response->results());
                }
            }
            if (!m_searchChatsReply && m_searchBusy) {
                m_searchBusy = false;
                Q_EMIT searchChanged();
            }
        });
    }

    // Phone-number lookup: only when the query is numeric, so plain name
    // searches never hit the network. Result lands above chat/message matches
    // and does not gate the busy spinner (it is a fast secondary lookup).
    if (looksLikePhoneNumber(query)) {
        CheckPhoneOnWhatsAppRequest request;
        request.setPhone(query);
        auto reply = m_chatClient->CheckPhoneOnWhatsApp(request);
        auto *replyPtr = reply.get();
        m_checkPhoneReply = std::move(reply);
        connect(replyPtr, &QGrpcCallReply::finished, this, [this, replyPtr](const QGrpcStatus &status) {
            if (m_checkPhoneReply.get() != replyPtr) {
                return;
            }
            const auto reply = std::move(m_checkPhoneReply);
            if (!status.isOk()) {
                m_searchResultsModel->clearNumber();
                return;
            }
            if (const auto response = reply->read<CheckPhoneOnWhatsAppResponse>()) {
                m_searchResultsModel->setNumber(response->jid(), response->phone(),
                                                response->displayName(), response->registered());
            }
        });
    } else {
        m_checkPhoneReply.reset();
        m_searchResultsModel->clearNumber();
    }
}

void AppController::startDirectChat(const QString &jid)
{
    if (!m_chatClient || jid.trimmed().isEmpty()) {
        return;
    }
    EnsureDirectChatRequest request;
    request.setJid(jid);
    auto reply = m_chatClient->EnsureDirectChat(request);
    auto *replyPtr = reply.get();
    m_startDirectChatReply = std::move(reply);
    connect(replyPtr, &QGrpcCallReply::finished, this, [this, replyPtr](const QGrpcStatus &status) {
        if (m_startDirectChatReply.get() != replyPtr) {
            return;
        }
        const auto reply = std::move(m_startDirectChatReply);
        if (!status.isOk()) {
            Q_EMIT messageActionFailed(status.message().isEmpty()
                                           ? i18nc("@info", "Unable to start chat")
                                           : status.message());
            return;
        }
        const auto response = reply->read<EnsureDirectChatResponse>();
        if (!response) {
            Q_EMIT messageActionFailed(i18nc("@info", "Unable to start chat"));
            return;
        }
        const auto chat = response->chat();
        m_chatListModel->upsertChat(chat);
        clearSearch();
        selectChat(chat.id_proto());
        // Drive column navigation the same way notification/deep-link opens do
        // (the chat is already selected above).
        Q_EMIT openChatRequested(chat.id_proto());
    });
}

void AppController::openContactInfo(const QString &jid)
{
    if (!m_chatClient || jid.trimmed().isEmpty()) {
        return;
    }
    GetContactInfoRequest request;
    request.setJid(jid);
    auto reply = m_chatClient->GetContactInfo(request);
    auto *replyPtr = reply.get();
    m_contactInfoReply = std::move(reply);
    connect(replyPtr, &QGrpcCallReply::finished, this, [this, replyPtr, jid](const QGrpcStatus &status) {
        if (m_contactInfoReply.get() != replyPtr) {
            return;
        }
        const auto reply = std::move(m_contactInfoReply);
        if (!status.isOk()) {
            Q_EMIT contactInfoFailed(jid, status.message().isEmpty()
                                              ? i18nc("@info", "Unable to load contact info")
                                              : status.message());
            return;
        }
        const auto response = reply->read<GetContactInfoResponse>();
        if (!response) {
            Q_EMIT contactInfoFailed(jid, i18nc("@info", "Unable to load contact info"));
            return;
        }
        const QVariantMap info {
            {QStringLiteral("jid"), response->jid()},
            {QStringLiteral("phoneNumber"), response->phoneNumber()},
            {QStringLiteral("savedName"), response->savedName()},
            {QStringLiteral("pushName"), response->pushName()},
            {QStringLiteral("businessName"), response->businessName()},
            {QStringLiteral("avatarLocalPath"), response->avatarLocalPath()},
            {QStringLiteral("isBusiness"), response->isBusiness()},
            {QStringLiteral("statusText"), response->statusText()},
        };
        Q_EMIT contactInfoReceived(info);
    });
}

void AppController::openGroupInfo(const QString &chatId)
{
    if (!m_chatClient || chatId.trimmed().isEmpty()) {
        return;
    }
    GetGroupInfoRequest request;
    request.setChatId(chatId);
    auto reply = m_chatClient->GetGroupInfo(request);
    auto *replyPtr = reply.get();
    m_groupInfoReply = std::move(reply);
    connect(replyPtr, &QGrpcCallReply::finished, this, [this, replyPtr, chatId](const QGrpcStatus &status) {
        if (m_groupInfoReply.get() != replyPtr) {
            return;
        }
        const auto reply = std::move(m_groupInfoReply);
        if (!status.isOk()) {
            Q_EMIT groupInfoFailed(chatId, status.message().isEmpty()
                                               ? i18nc("@info", "Unable to load group info")
                                               : status.message());
            return;
        }
        const auto response = reply->read<GetGroupInfoResponse>();
        if (!response) {
            Q_EMIT groupInfoFailed(chatId, i18nc("@info", "Unable to load group info"));
            return;
        }
        QVariantList members;
        for (const auto &member : response->members()) {
            members.append(QVariantMap {
                {QStringLiteral("jid"), member.jid()},
                {QStringLiteral("displayName"), member.displayName()},
                {QStringLiteral("phoneNumber"), member.phoneNumber()},
                {QStringLiteral("avatarLocalPath"), member.avatarLocalPath()},
                {QStringLiteral("isAdmin"), member.isAdmin()},
                {QStringLiteral("isSuperAdmin"), member.isSuperAdmin()},
            });
        }
        const QVariantMap info {
            {QStringLiteral("chatId"), chatId},
            {QStringLiteral("subject"), response->subject()},
            {QStringLiteral("description"), response->description()},
            {QStringLiteral("avatarLocalPath"), response->avatarLocalPath()},
            {QStringLiteral("createdUnix"), static_cast<qint64>(response->createdUnix())},
            {QStringLiteral("members"), members},
        };
        Q_EMIT groupInfoReceived(info);
    });
}

void AppController::viewProfilePicture(const QString &jid)
{
    if (!m_chatClient || jid.trimmed().isEmpty()) {
        return;
    }
    FetchProfilePictureRequest request;
    request.setJid(jid);
    auto reply = m_chatClient->FetchProfilePicture(request);
    auto *replyPtr = reply.get();
    m_profilePictureReply = std::move(reply);
    connect(replyPtr, &QGrpcCallReply::finished, this, [this, replyPtr, jid](const QGrpcStatus &status) {
        if (m_profilePictureReply.get() != replyPtr) {
            return;
        }
        const auto reply = std::move(m_profilePictureReply);
        if (!status.isOk()) {
            Q_EMIT profilePictureFailed(jid, status.message().isEmpty()
                                                 ? i18nc("@info", "Unable to load profile picture")
                                                 : status.message());
            return;
        }
        const auto response = reply->read<FetchProfilePictureResponse>();
        if (!response || response->localPath().isEmpty()) {
            Q_EMIT profilePictureFailed(jid, i18nc("@info", "No profile picture available"));
            return;
        }
        Q_EMIT profilePictureReady(jid, response->localPath());
    });
}

void AppController::openChatSearch()
{
    if (m_selectedChatId.isEmpty() || m_chatSearchActive) {
        return;
    }
    m_chatSearchActive = true;
    Q_EMIT chatSearchChanged();
}

void AppController::closeChatSearch()
{
    if (!m_chatSearchActive && m_chatSearchQuery.isEmpty() && m_chatSearchMatchIds.isEmpty()) {
        return;
    }
    resetChatSearch();
    m_chatSearchActive = false;
    Q_EMIT chatSearchChanged();
}

void AppController::resetChatSearch()
{
    m_chatSearchDebounceTimer->stop();
    m_chatSearchReply.reset();
    m_chatSearchQuery.clear();
    m_chatSearchMatchIds.clear();
    m_chatSearchIndex = -1;
}

void AppController::setChatSearchQuery(const QString &query)
{
    if (m_chatSearchQuery == query) {
        return;
    }
    m_chatSearchQuery = query;
    Q_EMIT chatSearchChanged();

    if (query.trimmed().isEmpty()) {
        m_chatSearchDebounceTimer->stop();
        m_chatSearchReply.reset();
        m_chatSearchMatchIds.clear();
        m_chatSearchIndex = -1;
        Q_EMIT chatSearchChanged();
        return;
    }
    m_chatSearchDebounceTimer->start();
}

void AppController::runChatSearch()
{
    if (!m_chatClient) {
        return;
    }
    const QString query = m_chatSearchQuery.trimmed();
    const QString chatId = m_selectedChatId;
    if (query.isEmpty() || chatId.isEmpty()) {
        m_chatSearchMatchIds.clear();
        m_chatSearchIndex = -1;
        Q_EMIT chatSearchChanged();
        return;
    }

    SearchMessagesRequest request;
    request.setQuery(query);
    request.setChatId(chatId);
    request.setLimit(100);

    auto reply = m_chatClient->SearchMessages(request);
    auto *replyPtr = reply.get();
    m_chatSearchReply = std::move(reply);
    connect(replyPtr, &QGrpcCallReply::finished, this, [this, replyPtr, chatId](const QGrpcStatus &status) {
        if (m_chatSearchReply.get() != replyPtr) {
            return;
        }
        const auto reply = std::move(m_chatSearchReply);
        // Ignore a stale response if the user has switched chats meanwhile.
        if (!status.isOk() || chatId != m_selectedChatId) {
            return;
        }
        m_chatSearchMatchIds.clear();
        if (const auto response = reply->read<SearchMessagesResponse>()) {
            for (const auto &result : response->results()) {
                m_chatSearchMatchIds.append(result.message().id_proto());
            }
        }
        m_chatSearchIndex = m_chatSearchMatchIds.isEmpty() ? -1 : 0;
        // The conversation view scrolls/glows the focused match off
        // chatSearchActiveMessageId; the bubble highlights off chatSearchQuery.
        Q_EMIT chatSearchChanged();
    });
}

void AppController::chatSearchNext()
{
    if (m_chatSearchMatchIds.isEmpty()) {
        return;
    }
    m_chatSearchIndex = (m_chatSearchIndex + 1) % m_chatSearchMatchIds.size();
    Q_EMIT chatSearchChanged();
}

void AppController::chatSearchPrevious()
{
    if (m_chatSearchMatchIds.isEmpty()) {
        return;
    }
    m_chatSearchIndex = (m_chatSearchIndex - 1 + m_chatSearchMatchIds.size()) % m_chatSearchMatchIds.size();
    Q_EMIT chatSearchChanged();
}

void AppController::requestMessageInfo(const QString &messageId)
{
    if (!m_chatClient || messageId.isEmpty()) {
        return;
    }

    GetMessageInfoRequest request;
    request.setMessageId(messageId);

    m_messageInfoReply = m_chatClient->GetMessageInfo(request);
    auto *replyPtr = m_messageInfoReply.get();
    connect(replyPtr, &QGrpcCallReply::finished, this, [this, replyPtr, messageId](const QGrpcStatus &status) {
        if (m_messageInfoReply.get() != replyPtr) {
            return;
        }
        const auto reply = std::move(m_messageInfoReply);
        if (!status.isOk()) {
            Q_EMIT messageInfoFailed(messageId,
                                     status.message().isEmpty() ? i18nc("@info", "Unable to load message info") : status.message());
            return;
        }
        const auto response = reply->read<whatevr::v1::GetMessageInfoResponse>();
        if (!response) {
            Q_EMIT messageInfoFailed(messageId, i18nc("@info", "Unable to load message info"));
            return;
        }

        QVariantList receipts;
        for (const auto &receipt : response->receipts()) {
            receipts.append(QVariantMap {
                {QStringLiteral("jid"), receipt.jid()},
                {QStringLiteral("displayName"), receipt.displayName()},
                {QStringLiteral("avatarLocalPath"), receipt.avatarLocalPath()},
                {QStringLiteral("deliveredTsUnix"), static_cast<qint64>(receipt.deliveredTsUnix())},
                {QStringLiteral("readTsUnix"), static_cast<qint64>(receipt.readTsUnix())},
                {QStringLiteral("playedTsUnix"), static_cast<qint64>(receipt.playedTsUnix())},
            });
        }
        const QVariantMap info {
            {QStringLiteral("status"), static_cast<int>(response->status())},
            {QStringLiteral("sentTsUnix"), static_cast<qint64>(response->sentTsUnix())},
            {QStringLiteral("deliveredTsUnix"), static_cast<qint64>(response->deliveredTsUnix())},
            {QStringLiteral("readTsUnix"), static_cast<qint64>(response->readTsUnix())},
            {QStringLiteral("isGroup"), response->isGroup()},
            {QStringLiteral("receipts"), receipts},
        };
        Q_EMIT messageInfoReceived(messageId, info);
    });
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
    m_stickerController->attachChannel(m_channel);
    Q_EMIT composerChanged();
}

void AppController::requestStatus()
{
    if (!m_daemonClient) {
        return;
    }

    m_statusReply = m_daemonClient->GetStatus(GetStatusRequest {}, probeCallOptions());
    auto *reply = m_statusReply.get();

    connect(reply, &QGrpcCallReply::finished, this, [this, reply](const QGrpcStatus &status) {
        if (m_statusReply.get() != reply) {
            return;
        }

        if (!status.isOk()) {
            m_statusReply.reset();
            handleTransportFailure(i18nc("@info", "Unable to read daemon status"), status.message(), status.code());
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

    m_reconnectReply = m_daemonClient->Reconnect(whatevr::v1::ReconnectRequest {}, probeCallOptions());
    auto *reply = m_reconnectReply.get();

    connect(reply, &QGrpcCallReply::finished, this, [this, reply](const QGrpcStatus &status) {
        if (m_reconnectReply.get() != reply) {
            return;
        }

        m_reconnectReply.reset();
        m_reconnectInFlight = false;

        if (!status.isOk()) {
            handleTransportFailure(i18nc("@info", "Reconnect request failed"), status.message(), status.code());
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
            handleTransportFailure(i18nc("@info", "Unable to load chats"), status.message(), status.code());
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

    // A chat opened with more unread messages than one page holds gets a
    // larger first page (capped by the daemon's 200-message limit), so the
    // unread divider anchor — and some context above it — is actually loaded.
    int limit = kMessageLimit;
    if (chatId == m_selectedChatId && !m_unreadAnchorResolved
        && m_selectedChatUnreadSnapshot + kUnreadContextMessages > kMessageLimit) {
        limit = qMin(kServerMessageLimit, m_selectedChatUnreadSnapshot + kUnreadContextMessages);
    }

    GetMessagesRequest request;
    request.setChatId(chatId);
    request.setLimit(limit);

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

    connect(reply, &QGrpcCallReply::finished, this, [this, reply, chatId, limit](const QGrpcStatus &status) {
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

        cacheMessages(chatId, visibleMessages, response->messages().size() >= limit);
        if (m_selectedChatId == chatId) {
            m_displayedMessagesChatId = chatId;
            m_messageListModel->replaceMessages(visibleMessages);
            m_canLoadOlderMessages = response->messages().size() >= limit;
            resolveUnreadAnchor(true);
            // A deferred "show in chat" jump for this chat fires now that its
            // first page is loaded (jumpToMessage loads around it if off-page).
            if (m_pendingJumpChatId == chatId && !m_pendingJumpMessageId.isEmpty()) {
                const QString pendingMessageId = m_pendingJumpMessageId;
                m_pendingJumpChatId.clear();
                m_pendingJumpMessageId.clear();
                jumpToMessage(pendingMessageId);
            }
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

    // The view re-reports visibility on every scroll frame while the badge is
    // up; restarting the debounce each time would push the send out forever.
    if (m_markChatReadTimer->isActive() && m_pendingMarkChatReadId == m_selectedChatId) {
        return;
    }

    m_pendingMarkChatReadId = m_selectedChatId;
    m_markChatReadTimer->start();
}

void AppController::markSelectedChatViewed()
{
    requestSelectedChatReadIfActive();
}

void AppController::resolveUnreadAnchor(bool authoritative)
{
    if (m_unreadAnchorResolved || m_selectedChatId.isEmpty() || m_displayedMessagesChatId != m_selectedChatId) {
        return;
    }
    if (m_selectedChatUnreadSnapshot <= 0) {
        m_unreadAnchorResolved = true;
        return;
    }

    bool complete = false;
    const QString anchor = m_messageListModel->unreadAnchorCandidate(m_selectedChatUnreadSnapshot, &complete);
    if (anchor.isEmpty()) {
        // Badge is up but no incoming message is loaded (e.g. all unread were
        // since deleted). The fresh response is the final word: stop looking.
        m_unreadAnchorResolved = authoritative;
        return;
    }
    if (!complete && !authoritative) {
        // The cached window may be missing part of the unread region; wait for
        // the (possibly enlarged) network page before placing the divider.
        return;
    }

    m_unreadAnchorMessageId = anchor;
    m_unreadAnchorCount = m_selectedChatUnreadSnapshot;
    m_unreadAnchorResolved = true;
    Q_EMIT unreadAnchorChanged();
}

void AppController::dismissUnreadAnchor()
{
    const bool changed = !m_unreadAnchorMessageId.isEmpty() || m_unreadAnchorCount != 0;
    m_selectedChatUnreadSnapshot = 0;
    m_unreadAnchorMessageId.clear();
    m_unreadAnchorCount = 0;
    m_unreadAnchorResolved = true;
    if (changed) {
        Q_EMIT unreadAnchorChanged();
    }
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

void AppController::enqueueSerial(QHash<QString, SerialSlot> &slots, const QString &key,
                                  std::function<void()> send)
{
    SerialSlot &slot = slots[key];
    if (slot.inFlight) {
        // A call is already in flight for this key: stash the latest intent,
        // overwriting any earlier-queued one (intermediate states are stale).
        slot.pending = std::move(send);
        slot.hasPending = true;
        return;
    }
    send();
}

void AppController::finishSerial(QHash<QString, SerialSlot> &slots, const QString &key)
{
    auto it = slots.find(key);
    if (it == slots.end()) {
        return;
    }
    it->inFlight.reset();
    if (it->hasPending) {
        auto pending = std::move(it->pending);
        it->pending = nullptr;
        it->hasPending = false;
        pending();  // re-arms inFlight for this key
        return;
    }
    slots.erase(it);
}

bool AppController::hasPendingSerial(const QHash<QString, SerialSlot> &slots, const QString &key) const
{
    auto it = slots.constFind(key);
    return it != slots.constEnd() && it->hasPending;
}

void AppController::setChatPinned(const QString &chatId, bool pinned)
{
    if (!m_chatClient || chatId.isEmpty()) {
        return;
    }

    // Optimistic: reflect the change now; revert if the daemon rejects it.
    const bool previousPinned = m_chatListModel->setChatPinnedLocal(chatId, pinned);

    auto send = [this, chatId, pinned, previousPinned]() {
        SetChatPinnedRequest request;
        request.setChatId(chatId);
        request.setPinned(pinned);

        auto reply = m_chatClient->SetChatPinned(request);
        auto *replyPtr = reply.get();
        m_setChatPinnedReplies[chatId].inFlight = std::move(reply);

        connect(replyPtr, &QGrpcCallReply::finished, this, [this, replyPtr, chatId, pinned, previousPinned](const QGrpcStatus &status) {
            auto it = m_setChatPinnedReplies.find(chatId);
            if (it == m_setChatPinnedReplies.end() || it.value().inFlight.get() != replyPtr) {
                return;
            }
            if (!status.isOk() && !hasPendingSerial(m_setChatPinnedReplies, chatId)) {
                m_chatListModel->setChatPinnedLocal(chatId, previousPinned);
                m_bannerText = status.message().isEmpty()
                    ? (pinned ? i18nc("@info", "Unable to pin chat") : i18nc("@info", "Unable to unpin chat"))
                    : status.message();
                emitStateChanged();
            }
            finishSerial(m_setChatPinnedReplies, chatId);
        });
    };

    enqueueSerial(m_setChatPinnedReplies, chatId, std::move(send));
}

void AppController::setChatArchived(const QString &chatId, bool archived)
{
    if (!m_chatClient || chatId.isEmpty()) {
        return;
    }

    // Optimistic: reflect the change now; revert if the daemon rejects it.
    const bool previousArchived = m_chatListModel->setChatArchivedLocal(chatId, archived);

    auto send = [this, chatId, archived, previousArchived]() {
        SetChatArchivedRequest request;
        request.setChatId(chatId);
        request.setArchived(archived);

        auto reply = m_chatClient->SetChatArchived(request);
        auto *replyPtr = reply.get();
        m_setChatArchivedReplies[chatId].inFlight = std::move(reply);

        connect(replyPtr, &QGrpcCallReply::finished, this, [this, replyPtr, chatId, archived, previousArchived](const QGrpcStatus &status) {
            auto it = m_setChatArchivedReplies.find(chatId);
            if (it == m_setChatArchivedReplies.end() || it.value().inFlight.get() != replyPtr) {
                return;
            }
            if (!status.isOk() && !hasPendingSerial(m_setChatArchivedReplies, chatId)) {
                m_chatListModel->setChatArchivedLocal(chatId, previousArchived);
                m_bannerText = status.message().isEmpty()
                    ? (archived ? i18nc("@info", "Unable to archive chat") : i18nc("@info", "Unable to unarchive chat"))
                    : status.message();
                emitStateChanged();
            }
            finishSerial(m_setChatArchivedReplies, chatId);
        });
    };

    enqueueSerial(m_setChatArchivedReplies, chatId, std::move(send));
}

void AppController::setChatMuted(const QString &chatId, bool muted, int durationSecs)
{
    if (!m_chatClient || chatId.isEmpty()) {
        return;
    }

    // Optimistic: reflect the change now; revert if the daemon rejects it.
    const bool previousMuted = m_chatListModel->setChatMutedLocal(chatId, muted);

    auto send = [this, chatId, muted, durationSecs, previousMuted]() {
        SetChatMutedRequest request;
        request.setChatId(chatId);
        request.setMuted(muted);
        // 0 with muted=true means "forever"; ignored by the daemon when unmuting.
        request.setMuteDurationSecs(muted ? durationSecs : 0);

        auto reply = m_chatClient->SetChatMuted(request);
        auto *replyPtr = reply.get();
        m_setChatMutedReplies[chatId].inFlight = std::move(reply);

        connect(replyPtr, &QGrpcCallReply::finished, this, [this, replyPtr, chatId, muted, previousMuted](const QGrpcStatus &status) {
            auto it = m_setChatMutedReplies.find(chatId);
            if (it == m_setChatMutedReplies.end() || it.value().inFlight.get() != replyPtr) {
                return;
            }
            if (!status.isOk() && !hasPendingSerial(m_setChatMutedReplies, chatId)) {
                m_chatListModel->setChatMutedLocal(chatId, previousMuted);
                m_bannerText = status.message().isEmpty()
                    ? (muted ? i18nc("@info", "Unable to mute chat") : i18nc("@info", "Unable to unmute chat"))
                    : status.message();
                emitStateChanged();
            }
            finishSerial(m_setChatMutedReplies, chatId);
        });
    };

    enqueueSerial(m_setChatMutedReplies, chatId, std::move(send));
}

void AppController::setChatDraft(const QString &chatId, const QString &text)
{
    if (chatId.isEmpty()) {
        return;
    }
    m_chatListModel->setChatDraft(chatId, text);
}

QString AppController::chatDraft(const QString &chatId) const
{
    return m_chatListModel->chatDraft(chatId);
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

    connect(stream, &QGrpcServerStream::messageReceived, this, [this, stream] {
        if (m_frontendSessionStream.get() != stream) {
            return;
        }

        const auto event = stream->read<whatevr::v1::FrontendSessionEvent>();
        if (!event || !event->hasOpenChat()) {
            return;
        }

        const QString chatId = event->openChat().chatId();
        if (chatId.isEmpty()) {
            return;
        }

        // The daemon (e.g. a clicked notification) asked us to surface this
        // chat. Reuse the deep-link path so a running instance just raises its
        // window and switches chats (only if different) — no second instance.
        m_pendingDeepLinkChatId = chatId;
        Q_EMIT activateWindowRequested();
        tryApplyPendingDeepLink();
    });

    connect(stream, &QGrpcServerStream::finished, this, [this, stream](const QGrpcStatus &status) {
        if (m_frontendSessionStream.get() != stream) {
            return;
        }

        m_frontendSessionStream.reset();
        if (!status.isOk()) {
            handleTransportFailure(i18nc("@info", "Frontend session stream ended"), status.message(), status.code());
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
        case whatevr::v1::DaemonEvent::PayloadFields::MessageDeleted:
            applyMessageDeleted(event->messageDeleted().chatId(), event->messageDeleted().messageId());
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
        case whatevr::v1::DaemonEvent::PayloadFields::ContactInfoUpdated:
            applyContactInfoUpdated(event->contactInfoUpdated());
            break;
        case whatevr::v1::DaemonEvent::PayloadFields::GroupInfoUpdated:
            applyGroupInfoUpdated(event->groupInfoUpdated());
            break;
        case whatevr::v1::DaemonEvent::PayloadFields::StickerLibraryChanged:
            m_stickerController->handleLibraryChanged(event->stickerLibraryChanged().source());
            break;
        case whatevr::v1::DaemonEvent::PayloadFields::StickerDownloadChanged:
            m_stickerController->handleDownloadChanged(event->stickerDownloadChanged().sticker(),
                                                       event->stickerDownloadChanged().errorText());
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
            handleTransportFailure(i18nc("@info", "Daemon event stream ended"), status.message(), status.code());
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
            handleTransportFailure(i18nc("@info", "Login event stream ended"), status.message(), status.code());
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

void AppController::handleTransportFailure(const QString &context, const QString &message, QtGrpc::StatusCode code)
{
    // A dropped/dead channel can't be reused — tear it down so the next
    // refresh() (button or retry) builds a fresh one and reconnects.
    resetChannel();
    m_canReconnect = false;

    // The login event stream just died with the channel, so any QR on screen is
    // stale and login is no longer in progress. Abandon it so page routing
    // (Main.qml appMode(), which checks loginRequired first) falls through to
    // the status page instead of stranding the user on a dead QR.
    clearLoginState();

    // Unavailable, or the socket having vanished, means the daemon is simply not
    // there: present it as "not running" (with start instructions) rather than a
    // generic error banner.
    if (code == QtGrpc::StatusCode::Unavailable || !daemonSocketExists()) {
        // A warm-up Unavailable on the just-built channel before the first
        // successful status is transient — keep "Connecting" and retry rather
        // than flashing "not running".
        if (deferStartupConnect()) {
            return;
        }
        enterNotRunning();
        return;
    }

    m_phase = Phase::Error;

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
    // First successful status: the startup window is over, so later drops surface
    // immediately as "not running"/error instead of being deferred as warm-up.
    m_startupGrace = false;
    if (m_startupGraceTimer) {
        m_startupGraceTimer->stop();
    }

    m_hasStatus = true;
    m_phase = Phase::Connected;
    m_actionError.clear();
    m_canReconnect = status.canReconnect();
    m_daemonStateLabel = !status.stateLabel().isEmpty() ? status.stateLabel() : fallbackStateLabel(status.state());
    m_statusDetail = status.detail();
    m_loginRequired = status.state() == DaemonState::DAEMON_STATE_NEED_LOGIN;

    if (!m_loginRequired) {
        clearLoginState();
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
    m_phase = Phase::Connected;
    m_actionError.clear();
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
        clearLoginState();
    }

    if (shellVisible()) {
        requestChats();
    }

    emitStateChanged();
}

void AppController::applyLoginStateChanged(const LoginStateChanged &change)
{
    m_hasStatus = true;
    m_phase = Phase::Connected;
    m_daemonStateLabel = fallbackStateLabel(change.state());
    m_statusDetail = change.detail();
    m_loginRequired = change.state() == DaemonState::DAEMON_STATE_NEED_LOGIN;

    if (!m_loginRequired) {
        clearLoginState();
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
        m_phase = Phase::Connected;
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
        Q_EMIT senderAvatarUpdated(id, localPath);
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

void AppController::applyContactInfoUpdated(const whatevr::v1::ContactInfoUpdated &update)
{
    Q_EMIT contactInfoUpdated(update.jid(), update.statusText());
}

void AppController::applyGroupInfoUpdated(const whatevr::v1::GroupInfoUpdated &update)
{
    QVariantList members;
    for (const auto &member : update.members()) {
        members.append(QVariantMap {
            {QStringLiteral("jid"), member.jid()},
            {QStringLiteral("displayName"), member.displayName()},
            {QStringLiteral("phoneNumber"), member.phoneNumber()},
            {QStringLiteral("avatarLocalPath"), member.avatarLocalPath()},
            {QStringLiteral("isAdmin"), member.isAdmin()},
            {QStringLiteral("isSuperAdmin"), member.isSuperAdmin()},
        });
    }
    const QVariantMap info {
        {QStringLiteral("chatId"), update.chatId()},
        {QStringLiteral("subject"), update.subject()},
        {QStringLiteral("description"), update.description()},
        {QStringLiteral("createdUnix"), static_cast<qint64>(update.createdUnix())},
        {QStringLiteral("members"), members},
    };
    Q_EMIT groupInfoUpdated(info);
}

void AppController::applyChatPresenceChanged(const ChatPresenceChanged &presence)
{
    const int availability = static_cast<int>(presence.availability());
    if (availability == 0) {
        m_chatListModel->setChatTyping(presence.chatId(), presence.isComposing());
    }

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
    m_messageListModel->setMediaDownloadState(messageId, download.downloading(), errorText,
                                              static_cast<qint64>(download.receivedBytes()),
                                              static_cast<qint64>(download.totalBytes()));

    if (wasDownloading != download.downloading()) {
        Q_EMIT mediaDownloadChanged(messageId);
    }
    if (!download.downloading() && !download.errorText().isEmpty()) {
        Q_EMIT mediaDownloadFailed(messageId, download.errorText());
    }
}

bool AppController::findCachedMessage(const QString &messageId, whatevr::v1::Message &out) const
{
    const auto cached = m_messageCache.constFind(m_selectedChatId);
    if (cached == m_messageCache.constEnd()) {
        return false;
    }
    for (const auto &message : cached->messages) {
        if (message.id_proto() == messageId) {
            out = message;
            return true;
        }
    }
    return false;
}

void AppController::applyMessageEvent(const whatevr::v1::Message &message)
{
    // The starred view is cross-chat, so keep it current regardless of which
    // chat is open (e.g. a message unstarred from another conversation).
    m_starredMessagesModel->applyMessage(message);

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
    // Keep the open chat's pinned banner live as pins come and go. Drop the
    // cached pin snapshot so a later reopen refetches rather than restoring a
    // stale set; the banner is already correct live for the open chat.
    m_pinnedMessagesModel->applyMessage(message);
    m_pinnedCache.remove(message.chatId());
    dismissUnreadAnchor();
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
    // Incoming messages no longer mark the chat read here; MessageView calls
    // markSelectedChatViewed() once the user is actually viewing the unread
    // region (WhatsApp behaviour).
}

void AppController::applyMessageDeleted(const QString &chatId, const QString &messageId)
{
    auto cached = m_messageCache.find(chatId);
    if (cached != m_messageCache.end()) {
        for (int i = 0; i < cached->messages.size(); ++i) {
            if (cached->messages.at(i).id_proto() == messageId) {
                cached->messages.removeAt(i);
                break;
            }
        }
    }

    if (chatId != m_selectedChatId) {
        return;
    }
    dismissUnreadAnchor();
    if (m_displayedMessagesChatId != chatId) {
        return;
    }
    const bool wasEmpty = m_messageListModel->isEmpty();
    if (m_messageListModel->removeMessage(messageId) && !wasEmpty && m_messageListModel->isEmpty()) {
        Q_EMIT messagesChanged();
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
        m_pinnedCache.remove(evictChatId);
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

bool AppController::shouldDisplayHistorySyncProgress(const HistorySyncProgress &progress) const
{
    if (!m_historySyncCursorActive) {
        return true;
    }

    const auto incomingType = progress.syncType();
    const auto incomingPhase = progress.phase();
    if (isAuxiliaryHistorySyncType(incomingType) && !isAuxiliaryHistorySyncType(m_historySyncCursorSyncType)) {
        return false;
    }
    if (isQueuedHistorySyncPhase(incomingPhase)) {
        return false;
    }

    const bool currentActive = isActiveHistorySyncPhase(m_historySyncCursorPhase);
    const bool incomingActive = isActiveHistorySyncPhase(incomingPhase);
    if (incomingType != m_historySyncCursorSyncType) {
        return incomingActive;
    }

    const std::uint32_t incomingChunk = progress.chunkOrder();
    if (incomingChunk < m_historySyncCursorChunkOrder) {
        return !currentActive && incomingActive;
    }
    if (incomingChunk == m_historySyncCursorChunkOrder
        && historySyncPhaseRank(incomingPhase) < historySyncPhaseRank(m_historySyncCursorPhase)) {
        return false;
    }

    return true;
}

bool AppController::shouldCompleteHistorySyncDisplay(const HistorySyncProgress &progress) const
{
    if (!m_historySyncVisible) {
        return false;
    }
    if (!m_historySyncCursorActive) {
        return true;
    }
    return progress.syncType() == m_historySyncCursorSyncType || progress.progressPercent() >= 100;
}

bool AppController::resetHistorySyncDisplay(int percent)
{
    const int boundedPercent = qBound(0, percent, 100);
    const bool changed = m_historySyncVisible
        || m_historySyncPercent != boundedPercent
        || !m_historySyncTitle.isEmpty()
        || !m_historySyncDetail.isEmpty()
        || m_historySyncCursorActive
        || m_historySyncCursorSyncType != HistorySyncType::HISTORY_SYNC_TYPE_UNSPECIFIED
        || m_historySyncCursorChunkOrder != 0
        || m_historySyncCursorPhase != HistorySyncPhase::HISTORY_SYNC_PHASE_UNSPECIFIED;

    m_historySyncVisible = false;
    m_historySyncPercent = boundedPercent;
    m_historySyncTitle.clear();
    m_historySyncDetail.clear();
    m_historySyncCursorActive = false;
    m_historySyncCursorSyncType = HistorySyncType::HISTORY_SYNC_TYPE_UNSPECIFIED;
    m_historySyncCursorChunkOrder = 0;
    m_historySyncCursorPhase = HistorySyncPhase::HISTORY_SYNC_PHASE_UNSPECIFIED;

    return changed;
}

void AppController::applyHistorySyncProgress(const HistorySyncProgress &progress)
{
    if (progress.isComplete()) {
        if (shouldCompleteHistorySyncDisplay(progress)) {
            resetHistorySyncDisplay(100);
            Q_EMIT historySyncChanged();
        }
        requestChats();
        return;
    }

    if (!shouldDisplayHistorySyncProgress(progress)) {
        return;
    }

    const bool wasVisible = m_historySyncVisible;
    const int progressPercent = qBound(0, static_cast<int>(progress.progressPercent()), 100);
    m_historySyncCursorActive = true;
    m_historySyncCursorSyncType = progress.syncType();
    m_historySyncCursorChunkOrder = progress.chunkOrder();
    m_historySyncCursorPhase = progress.phase();
    m_historySyncVisible = true;
    m_historySyncPercent = wasVisible ? qMax(m_historySyncPercent, progressPercent) : progressPercent;
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
    m_pinnedCache.remove(chatId);
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

void AppController::clearLoginState()
{
    m_loginRequired = false;
    m_qrCode.clear();
    m_qrExpiresAtUnix = 0;
    m_qrExpiryText.clear();
    m_qrTimer->stop();
}

void AppController::clearBanner()
{
    if (m_bannerText.isEmpty()) {
        return;
    }

    m_bannerText.clear();
    emitStateChanged();
}

void AppController::handleCommandLine(const QStringList &arguments)
{
    QString uri;
    for (const QString &arg : arguments) {
        if (arg.startsWith(QStringLiteral("whatevr:"), Qt::CaseInsensitive)) {
            uri = arg;
            break;
        }
    }
    if (uri.isEmpty()) {
        Q_EMIT activateWindowRequested();
        return;
    }
    openChatFromUri(uri);
}

void AppController::openChatFromUri(const QString &uri)
{
    // Expected form: whatevr://chat/<percent-encoded-chat-id> (emitted by the
    // daemon's notification handler).
    const QUrl url(uri);
    if (url.scheme().compare(QStringLiteral("whatevr"), Qt::CaseInsensitive) != 0
        || url.host() != QStringLiteral("chat")) {
        Q_EMIT activateWindowRequested();
        return;
    }

    QString chatId = url.path(QUrl::FullyDecoded);
    if (chatId.startsWith(QLatin1Char('/'))) {
        chatId = chatId.mid(1);
    }
    if (chatId.isEmpty()) {
        Q_EMIT activateWindowRequested();
        return;
    }

    m_pendingDeepLinkChatId = chatId;
    Q_EMIT activateWindowRequested();
    tryApplyPendingDeepLink();
}

void AppController::tryApplyPendingDeepLink()
{
    if (m_pendingDeepLinkChatId.isEmpty()) {
        return;
    }
    // A chat can only be opened once the chat shell is up; otherwise keep the
    // request pending and retry on the next state change (e.g. after the daemon
    // connects or login completes following a cold start from a notification).
    if (!shellVisible()) {
        return;
    }

    const QString chatId = m_pendingDeepLinkChatId;
    m_pendingDeepLinkChatId.clear();
    selectChat(chatId);
    Q_EMIT openChatRequested(chatId);
}

void AppController::emitStateChanged()
{
    Q_EMIT stateChanged();
    tryApplyPendingDeepLink();
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
        m_selectedChatUnreadCount = 0;
        return;
    }

    m_selectedChatName = m_chatListModel->chatName(m_selectedChatId);
    m_selectedChatAvatarLocalPath = m_chatListModel->chatAvatarLocalPath(m_selectedChatId);
    m_selectedChatIsGroup = m_chatListModel->chatIsGroup(m_selectedChatId);
    m_selectedChatUnreadCount = m_chatListModel->chatUnreadCount(m_selectedChatId);
    m_messageListModel->setGroupChat(m_selectedChatIsGroup);
}
