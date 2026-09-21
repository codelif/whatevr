#include "protocolcontroller.h"

#include <QClipboard>
#include <QCoreApplication>
#include <QDateTime>
#include <QDir>
#include <QFile>
#include <QDesktopServices>
#include <QFileInfo>
#include <QGuiApplication>
#include <QImage>
#include <QJsonArray>
#include <QLocale>
#include <QMimeData>
#include <QPointer>
#include <QProcess>
#include <QQmlEngine>
#include <QRegularExpression>
#include <QSet>
#include <QSettings>
#include <QStandardPaths>
#include <QStringList>
#include <QTextBoundaryFinder>
#include <QTimeZone>
#include <QTimer>
#include <QUrl>
#include <QUuid>

#include <KLocalizedString>

#include <utility>

#include "collectionviewmodel.h"
#include "emojimodel.h"
#include "messagemarkup.h"
#include "messagerow.h"
#include "objectviewmodel.h"
#include "protocolclient.h"
#include "protocolmessagemodel.h"
#include "protocolsearchmodel.h"
#include "protocolstickercontroller.h"
#include "richtext.h"

using whatevr::proto::CollectionViewModel;
using whatevr::proto::ObjectViewModel;
using whatevr::proto::ProtocolClient;
using whatevr::proto::ProtocolError;
using whatevr::proto::Subscription;
using whatevr::util::plainTextFromQtRichText;

namespace
{
ProtocolController *s_instance = nullptr;

// Cold-start grace: hold the neutral splash rather than flashing the
// "not running" page while the daemon socket may still be appearing right
// after launch.
constexpr int kStartupGraceMs = 1000;
constexpr int kMessagePageSize = 80;
constexpr int kMarkReadDebounceMs = 120;
constexpr int kPhoneHistoryTimeoutMs = 45'000;
constexpr int kSearchDebounceMs = 180;
// The chat list is windowed (DN6). An unbounded `chats` subscribe made the
// daemon serialise the whole roster — measured at 917 rows / 326 KB / ~30 ms
// on a real account — once at startup and again on every filter switch, all to
// paint the dozen rows that fit on screen. One page comfortably overfills any
// sidebar; the rest arrives by `extend` as the list scrolls.
constexpr int kChatPageSize = 24;
// Starred rows are a windowed view like any other collection; the page extends
// as it scrolls instead of asking the daemon for every star at once.
constexpr int kStarredPageSize = 50;
// A gallery page is a grid, so it shows more per screen than the starred list.
constexpr int kChatMediaPageSize = 60;
// The status feed is windowed like any other collection; statuses expire after
// 24h server-side, so one page already spans most of a day's stories.
constexpr int kStatusPageSize = 100;
// In-chat search asks for one generous page of matches: the match cursor walks
// that list, it is not a scrollable surface.
constexpr int kChatSearchLimit = 100;

// Composer drafts and the preference gating their persistence. Settings owns
// the preference key; this reads it directly, the way EmojiModel reads its own
// QSettings-backed presentation state.
constexpr auto kDraftsKey = "settings/drafts";
constexpr auto kPersistDraftsKey = "settings/persistDrafts";

// Renders the QR countdown text, mirroring AppController::formatQrExpiry so the
// login page reads identically on either stack during the migration.
QString formatQrExpiry(qint64 expiresAtUnix)
{
    if (expiresAtUnix <= 0) {
        return {};
    }
    const qint64 secondsLeft = expiresAtUnix - QDateTime::currentSecsSinceEpoch();
    if (secondsLeft <= 0) {
        return i18nc("@info", "QR code expired. Refresh to request a new one.");
    }
    if (secondsLeft < 60) {
        return i18ncp("@info countdown", "Expires in %1 second", "Expires in %1 seconds", secondsLeft);
    }
    const qint64 minutes = (secondsLeft + 59) / 60;
    return i18ncp("@info countdown", "Expires in %1 minute", "Expires in %1 minutes", minutes);
}

// Renders a contact's last-seen time, mirroring AppController::formatLastSeen so
// the conversation header reads identically on either stack.
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
        return i18nc("@info chat presence", "last seen today at %1",
                     QLocale().toString(lastSeen.time(), QLocale::ShortFormat));
    }
    if (lastSeen.date() == today.addDays(-1)) {
        return i18nc("@info chat presence", "last seen yesterday at %1",
                     QLocale().toString(lastSeen.time(), QLocale::ShortFormat));
    }
    return i18nc("@info chat presence", "last seen %1", QLocale().toString(lastSeen, QLocale::ShortFormat));
}

// Human label for a history-sync type (the `sync` view's `type` string). Mirrors
// AppController::syncTypeLabel so the strip reads identically on either stack.
QString syncTypeLabel(const QString &type)
{
    if (type == QLatin1String("initial_bootstrap")) {
        return i18nc("@label", "Initial history sync");
    }
    if (type == QLatin1String("initial_status_v3")) {
        return i18nc("@label", "Status history sync");
    }
    if (type == QLatin1String("full")) {
        return i18nc("@label", "Full history sync");
    }
    if (type == QLatin1String("recent")) {
        return i18nc("@label", "Recent history sync");
    }
    if (type == QLatin1String("push_name")) {
        return i18nc("@label", "Updating names");
    }
    if (type == QLatin1String("non_blocking_data")) {
        return i18nc("@label", "Syncing background data");
    }
    if (type == QLatin1String("on_demand")) {
        return i18nc("@label", "Loading requested history");
    }
    if (type == QLatin1String("offline_catchup")) {
        return i18nc("@label", "Syncing missed messages");
    }
    return i18nc("@label", "Syncing history");
}

// Whether a search query is a phone number worth a `contacts.check_phone`
// lookup, so a plain name search never hits the network. Mirrors
// AppController::looksLikePhoneNumber.
bool looksLikePhoneNumber(const QString &query)
{
    QString digits;
    for (int i = 0; i < query.size(); ++i) {
        const QChar c = query.at(i);
        if (c.isDigit()) {
            digits.append(c);
        } else if (c == QLatin1Char('+') && i == 0) {
            continue;
        } else if (c == QLatin1Char(' ') || c == QLatin1Char('-') || c == QLatin1Char('(')
                   || c == QLatin1Char(')') || c == QLatin1Char('.')) {
            continue;
        } else {
            return false;
        }
    }
    return digits.size() >= 7 && digits.size() <= 15;
}

// The sender label of a daemon message row, with outgoing messages rendered as
// "You" (the row carries the real sender either way).
QString messageRowSenderName(const QVariantMap &item)
{
    if (item.value(QStringLiteral("direction")).toString() == QLatin1String("outgoing")) {
        return i18nc("@item:intext message sender, the local user", "You");
    }
    return item.value(QStringLiteral("sender")).toMap().value(QStringLiteral("name")).toString();
}
} // namespace

void ProtocolController::setInstance(ProtocolController *instance)
{
    s_instance = instance;
}

ProtocolController *ProtocolController::create(QQmlEngine *qmlEngine, QJSEngine *jsEngine)
{
    Q_UNUSED(qmlEngine)
    Q_UNUSED(jsEngine)

    Q_ASSERT(s_instance);
    QQmlEngine::setObjectOwnership(s_instance, QQmlEngine::CppOwnership);
    return s_instance;
}

ProtocolController::ProtocolController(QObject *parent)
    : ProtocolController(daemonSocketPath(), parent)
{
}

ProtocolController::ProtocolController(QString socketPath, QObject *parent)
    : QObject(parent)
    , m_socketPath(std::move(socketPath))
{
    m_client = new ProtocolClient(m_socketPath, QStringLiteral("whatkevr"), this);
    connect(m_client, &ProtocolClient::ready, this, &ProtocolController::onClientReady);
    connect(m_client, &ProtocolClient::disconnected, this, &ProtocolController::onClientDisconnected);
    connect(m_client, &ProtocolClient::openChatRequested, this, &ProtocolController::openChatRequested);
    connect(m_client, &ProtocolClient::activateWindowRequested, this,
            &ProtocolController::activateWindowRequested);
    connect(m_client, &ProtocolClient::showTrayMenuRequested, this,
            &ProtocolController::showTrayMenuRequested);
    connect(m_client, &ProtocolClient::mediaStreamUpdated, this,
            [this](const QString &streamId,
                   const QString &messageId,
                   const QString &state,
                   const QString &path,
                   const QString &error) {
                if (streamId.isEmpty() || m_mediaStreamMessages.value(streamId) != messageId) {
                    return;
                }
                m_mediaStreamMessages.remove(streamId);
                Q_EMIT mediaStreamUpdated(streamId, messageId, state, path, error);
            });
    // Every failed connect attempt also lands here (the client funnels connect
    // errors through disconnected()); recomputing phase is idempotent.

    // A deep link can arrive before the shell exists (a notification click may
    // cold-start the app); every state change is a chance to apply it.
    connect(this, &ProtocolController::stateChanged, this, &ProtocolController::tryApplyPendingDeepLink);

    loadPersistedDrafts();

    m_connectionModel = new ObjectViewModel(this);
    m_loginModel = new ObjectViewModel(this);
    connect(m_connectionModel, &ObjectViewModel::valueChanged, this, &ProtocolController::onConnectionValueChanged);
    connect(m_loginModel, &ObjectViewModel::valueChanged, this, &ProtocolController::onLoginValueChanged);

    // The chat-list model (D2b1). loading/empty are derived from its ready/count,
    // so fan those into chatsChanged for the QML placeholder bindings.
    m_chatsModel = new CollectionViewModel(this);
    connect(m_chatsModel, &CollectionViewModel::readyChanged, this, &ProtocolController::chatsChanged);
    connect(m_chatsModel, &CollectionViewModel::countChanged, this, &ProtocolController::chatsChanged);

    // Archived chats (D2b2): a sibling `chats` collection; archivedCount tracks
    // its row count for the section header.
    m_archivedModel = new CollectionViewModel(this);
    connect(m_archivedModel, &CollectionViewModel::countChanged, this, &ProtocolController::archivedChanged);
    connect(m_archivedModel, &CollectionViewModel::readyChanged, this, &ProtocolController::archivedChanged);
    m_chatFoldersModel = new CollectionViewModel(this);

    // The selected conversation has its own `chat` object view, independent of
    // the sidebar's current filter and loaded window.
    m_selectedChatModel = new ObjectViewModel(this);
    m_groupPolicyModel = new ObjectViewModel(this);
    connect(m_groupPolicyModel, &ObjectViewModel::valueChanged, this, [this] { Q_EMIT composerChanged(); });
    connect(m_selectedChatModel, &ObjectViewModel::valueChanged, this, [this] {
        if (!m_selectedChatId.isEmpty()) {
            if (m_session->phoneHistoryRequesting && selectedChatHistoryExhausted()) {
                m_session->phoneHistoryRequesting = false;
                m_phoneHistoryTimer->stop();
                m_phoneHistorySettleTimer->stop();
                Q_EMIT messagesChanged();
            }
            Q_EMIT selectionChanged();
            if (m_waitingForSelectedChatItem && m_selectedChatModel->isPresent()) {
                m_waitingForSelectedChatItem = false;
                const QString anchor = selectedChatUnreadCount() > 0
                    ? QStringLiteral("unread")
                    : QStringLiteral("latest");
                setSelectedChat(m_selectedChatId, anchor, {});
            } else if (!m_selectedChatModel->isPresent()) {
                const QString chatId = m_selectedChatId;
                QTimer::singleShot(0, this, [this, chatId] {
                    // A remove leaves the view ready; a reconnect reset turns it
                    // unready before this queued check and must keep selection.
                    if (m_selectedChatId == chatId && m_selectedChatModel->isReady()
                        && !m_selectedChatModel->isPresent()) {
                        setSelectedChat({}, {}, {});
                    }
                });
            }
        }
    });
    connect(m_selectedChatModel, &ObjectViewModel::readyChanged, this, [this] {
        if (m_waitingForSelectedChatItem && m_selectedChatModel->isReady()
            && !m_selectedChatModel->isPresent()) {
            selectedChatLookupFailed(m_selectedChatId, QStringLiteral("not_found"),
                                     QStringLiteral("chat disappeared before its initial fill"));
        }
    });

    // Typing overlay (D2b2): the global `typing` collection. Any change (a chat
    // starting/stopping, or a reset) bumps typingRevision so per-row isTyping
    // bindings re-evaluate.
    m_typingModel = new CollectionViewModel(this);
    const auto bumpTyping = [this] {
        ++m_typingRevision;
        Q_EMIT typingChanged();
        // The conversation header composes typing over availability, so a typing
        // change is also a presence change for the selected chat.
        Q_EMIT presenceChanged();
    };
    connect(m_typingModel, &CollectionViewModel::countChanged, this, bumpTyping);
    connect(m_typingModel, &CollectionViewModel::modelReset, this, bumpTyping);
    connect(m_typingModel, &CollectionViewModel::dataChanged, this, bumpTyping);

    // History-sync strip (D2b2): the `sync` object view; the strip state is
    // derived from its single item.
    m_syncModel = new ObjectViewModel(this);
    connect(m_syncModel, &ObjectViewModel::valueChanged, this, &ProtocolController::recomputeHistorySync);

    // The transcript models are per warm chat and built on demand; the pool
    // starts empty with one slot per pane the conversation will park.
    m_messageWindows.resize(kWarmChatWindows);

    // An empty transcript that is never filled and never subscribed, so that
    // "no chat is open" is a model saying it holds nothing rather than a null
    // pointer. Around a dozen readers of the active transcript are property
    // getters that QML evaluates before any chat has been chosen, and a
    // placeholder answers all of them correctly (no rows, not ready, no such
    // message) for the cost of one object.
    m_idleMessagesModel = new CollectionViewModel(this);
    m_idleMessagesModel->setReverseOrder(true);
    m_idleMessagePresentation = new ProtocolMessageModel(m_idleMessagesModel, this);
    // The stand-in with no chat in it. It is never in the pool and never
    // subscribed; it exists so that nothing reading the active session has to
    // check for null.
    m_idleSession = new ConversationSession(this);
    m_idleSession->source = m_idleMessagesModel;
    m_idleSession->presentation = m_idleMessagePresentation;
    m_session = m_idleSession;
    m_messagesModel = m_idleMessagesModel;
    m_messagePresentationModel = m_idleMessagePresentation;

    // Media transfers (D4c): the global `transfers` view is what makes a
    // downloading bubble show progress. The timeline model reads it through by
    // message id at render time — the two views are never merged into one row.
    m_transfersModel = new CollectionViewModel(this);

    // Conversation-header presence (D3c): one item for the selected chat's
    // counterpart while a conversation is on screen.
    m_presenceModel = new CollectionViewModel(this);
    connect(m_presenceModel, &CollectionViewModel::countChanged, this, &ProtocolController::presenceChanged);
    connect(m_presenceModel, &CollectionViewModel::dataChanged, this, &ProtocolController::presenceChanged);
    connect(m_presenceModel, &CollectionViewModel::modelReset, this, &ProtocolController::presenceChanged);

    // Message-info dialog receipts (D3c): a participant roster the dialog reads
    // through messageReceipts(); a revision tick makes those reads re-evaluate.
    m_receiptsModel = new CollectionViewModel(this);
    const auto bumpReceipts = [this] {
        ++m_receiptsRevision;
        Q_EMIT messageReceiptsChanged();
    };
    connect(m_receiptsModel, &CollectionViewModel::countChanged, this, bumpReceipts);
    connect(m_receiptsModel, &CollectionViewModel::dataChanged, this, bumpReceipts);
    connect(m_receiptsModel, &CollectionViewModel::modelReset, this, bumpReceipts);
    connect(m_receiptsModel, &CollectionViewModel::readyChanged, this, bumpReceipts);

    // Pinned banner (D4b): the displayed chat's pins. The banner reads rows by
    // index, so every shape change (fill, pin, unpin, expiry) is one signal.
    m_liveLocationsModel = new CollectionViewModel(this);
    connect(m_liveLocationsModel, &CollectionViewModel::countChanged, this, &ProtocolController::liveLocationsChanged);
    connect(m_liveLocationsModel, &CollectionViewModel::dataChanged, this, &ProtocolController::liveLocationsChanged);
    connect(m_liveLocationsModel, &CollectionViewModel::modelReset, this, &ProtocolController::liveLocationsChanged);

    m_pinnedModel = new CollectionViewModel(this);
    connect(m_pinnedModel, &CollectionViewModel::countChanged, this, &ProtocolController::pinnedMessagesChanged);
    connect(m_pinnedModel, &CollectionViewModel::dataChanged, this, &ProtocolController::pinnedMessagesChanged);
    connect(m_pinnedModel, &CollectionViewModel::modelReset, this, &ProtocolController::pinnedMessagesChanged);
    connect(m_pinnedModel, &CollectionViewModel::readyChanged, this, &ProtocolController::pinnedMessagesChanged);

    // Forward picker (D4b): its own `chats` collection, read through
    // forwardChatTargets() with a revision tick like the receipts roster.
    m_forwardTargetsModel = new CollectionViewModel(this);
    const auto bumpForwardTargets = [this] {
        ++m_forwardTargetsRevision;
        Q_EMIT forwardTargetsChanged();
    };
    connect(m_forwardTargetsModel, &CollectionViewModel::countChanged, this, bumpForwardTargets);
    connect(m_forwardTargetsModel, &CollectionViewModel::dataChanged, this, bumpForwardTargets);
    connect(m_forwardTargetsModel, &CollectionViewModel::modelReset, this, bumpForwardTargets);

    // Starred page (D5): a windowed `starred` collection the page binds
    // directly; loading/exhausted drive its spinner and scroll-extend.
    m_starredModel = new CollectionViewModel(this);
    m_chatMediaModel = new CollectionViewModel(this);
    connect(m_chatMediaModel, &CollectionViewModel::countChanged, this, &ProtocolController::chatMediaChanged);
    connect(m_chatMediaModel, &CollectionViewModel::readyChanged, this, &ProtocolController::chatMediaChanged);
    connect(m_chatMediaModel, &CollectionViewModel::modelReset, this, &ProtocolController::chatMediaChanged);
    connect(m_starredModel, &CollectionViewModel::countChanged, this, &ProtocolController::starredMessagesChanged);
    connect(m_starredModel, &CollectionViewModel::readyChanged, this, &ProtocolController::starredMessagesChanged);
    connect(m_starredModel, &CollectionViewModel::modelReset, this, &ProtocolController::starredMessagesChanged);

    // Status and calls tabs: plain collection models over the `status` and
    // `calls` views. The status page groups rows per contact itself; the calls
    // count doubles as the rail badge.
    m_statusModel = new CollectionViewModel(this);
    connect(m_statusModel, &CollectionViewModel::countChanged, this, &ProtocolController::statusChanged);
    connect(m_statusModel, &CollectionViewModel::readyChanged, this, &ProtocolController::statusChanged);
    connect(m_statusModel, &CollectionViewModel::modelReset, this, &ProtocolController::statusChanged);
    // Row content changes (viewed flags, downloaded paths) carry no count or
    // ready edge but must still refresh pages holding snapshots — notably the
    // status viewer, which otherwise keeps showing Load after the bytes land.
    connect(m_statusModel, &CollectionViewModel::dataChanged, this, &ProtocolController::statusChanged);
    // Kept senders ride the status tab's lifetime; any churn rebuilds groups.
    m_keptStatusModel = new CollectionViewModel(this);
    connect(m_keptStatusModel, &CollectionViewModel::countChanged, this, &ProtocolController::statusChanged);
    connect(m_keptStatusModel, &CollectionViewModel::readyChanged, this, &ProtocolController::statusChanged);
    connect(m_keptStatusModel, &CollectionViewModel::modelReset, this, &ProtocolController::statusChanged);
    connect(m_keptStatusModel, &CollectionViewModel::dataChanged, this, &ProtocolController::statusChanged);
    // Muted senders ride the same lifetime into the page's Muted section.
    m_mutedStatusModel = new CollectionViewModel(this);
    connect(m_mutedStatusModel, &CollectionViewModel::countChanged, this, &ProtocolController::statusChanged);
    connect(m_mutedStatusModel, &CollectionViewModel::readyChanged, this, &ProtocolController::statusChanged);
    connect(m_mutedStatusModel, &CollectionViewModel::modelReset, this, &ProtocolController::statusChanged);
    connect(m_mutedStatusModel, &CollectionViewModel::dataChanged, this, &ProtocolController::statusChanged);
    m_callsModel = new CollectionViewModel(this);
    connect(m_callsModel, &CollectionViewModel::countChanged, this, &ProtocolController::callsChanged);
    connect(m_callsModel, &CollectionViewModel::readyChanged, this, &ProtocolController::callsChanged);
    connect(m_callsModel, &CollectionViewModel::modelReset, this, &ProtocolController::callsChanged);

    m_channelsModel = new CollectionViewModel(this);
    connect(m_channelsModel, &CollectionViewModel::countChanged, this, &ProtocolController::channelsChanged);
    connect(m_channelsModel, &CollectionViewModel::readyChanged, this, &ProtocolController::channelsChanged);
    connect(m_channelsModel, &CollectionViewModel::modelReset, this, &ProtocolController::channelsChanged);
    m_channelMessagesModel = new CollectionViewModel(this);
    connect(m_channelMessagesModel, &CollectionViewModel::countChanged, this, &ProtocolController::channelMessagesChanged);
    connect(m_channelMessagesModel, &CollectionViewModel::readyChanged, this, &ProtocolController::channelMessagesChanged);
    connect(m_channelMessagesModel, &CollectionViewModel::modelReset, this, &ProtocolController::channelMessagesChanged);

    m_logsModel = new CollectionViewModel(this);

    // Info card (D5): one object view (either `contact` or `group`, whichever
    // the open dialog asked for) plus the group's member roster. Two-phase
    // enrichment arrives as ordinary upserts on both.
    m_infoCardModel = new ObjectViewModel(this);
    connect(m_infoCardModel, &ObjectViewModel::valueChanged, this, &ProtocolController::infoCardChanged);
    connect(m_infoCardModel, &ObjectViewModel::readyChanged, this, &ProtocolController::infoCardChanged);

    m_groupMembersModel = new CollectionViewModel(this);
    const auto bumpGroupMembers = [this] {
        ++m_groupMembersRevision;
        Q_EMIT groupMembersChanged();
    };
    connect(m_groupMembersModel, &CollectionViewModel::countChanged, this, bumpGroupMembers);
    connect(m_groupMembersModel, &CollectionViewModel::dataChanged, this, bumpGroupMembers);
    connect(m_groupMembersModel, &CollectionViewModel::modelReset, this, bumpGroupMembers);

    // The displayed conversation's own roster, for the composer's `@`-mention
    // picker. Same view, different lifetime: this one follows the conversation.
    m_chatMembersModel = new CollectionViewModel(this);
    const auto bumpChatMembers = [this] {
        ++m_chatMembersRevision;
        Q_EMIT chatMembersChanged();
    };
    connect(m_chatMembersModel, &CollectionViewModel::countChanged, this, bumpChatMembers);
    connect(m_chatMembersModel, &CollectionViewModel::dataChanged, this, bumpChatMembers);
    connect(m_chatMembersModel, &CollectionViewModel::modelReset, this, bumpChatMembers);

    // Blocked state is membership in the `blocklist` view, composed into the
    // contact card at render time (the D2b2 typing-in-a-chat-row shape) rather
    // than copied into card state.
    m_blocklistModel = new CollectionViewModel(this);
    const auto blocklistChanged = [this] {
        Q_EMIT infoCardChanged();
        Q_EMIT this->blocklistChanged();
    };
    connect(m_blocklistModel, &CollectionViewModel::countChanged, this, blocklistChanged);
    connect(m_blocklistModel, &CollectionViewModel::dataChanged, this, blocklistChanged);
    connect(m_blocklistModel, &CollectionViewModel::modelReset, this, blocklistChanged);

    m_privacyModel = new ObjectViewModel(this);
    m_preferencesModel = new ObjectViewModel(this);
    m_selfModel = new ObjectViewModel(this);
    connect(m_privacyModel, &ObjectViewModel::valueChanged, this, &ProtocolController::privacySettingsChanged);
    connect(m_preferencesModel, &ObjectViewModel::valueChanged, this, &ProtocolController::appPreferencesChanged);
    connect(m_selfModel, &ObjectViewModel::valueChanged, this, &ProtocolController::selfProfileChanged);

    m_stickerController = new ProtocolStickerController(m_client, this);
    // Stickers are sent by their own controller, so the timeline hears about
    // them here. This fires on the ack rather than on the request (there is no
    // earlier signal), which only means the row has already landed by the time
    // the view follows it.
    connect(m_stickerController, &ProtocolStickerController::stickerSent, this,
            [this] { Q_EMIT messageSent(); });

    m_searchResultsModel = new ProtocolSearchModel(this);

    m_startupGraceTimer = new QTimer(this);
    m_startupGraceTimer->setSingleShot(true);
    m_startupGraceTimer->setInterval(kStartupGraceMs);
    connect(m_startupGraceTimer, &QTimer::timeout, this, [this] {
        m_startupGrace = false;
        Q_EMIT stateChanged();
    });

    // While a QR is on screen, re-emit once a second so the countdown text the
    // login page derives stays live (qrExpiryText() recomputes on read).
    m_qrTimer = new QTimer(this);
    m_qrTimer->setInterval(1000);
    connect(m_qrTimer, &QTimer::timeout, this, &ProtocolController::refreshQrExpiry);

    m_readTimer = new QTimer(this);
    m_readTimer->setSingleShot(true);
    m_readTimer->setInterval(kMarkReadDebounceMs);
    connect(m_readTimer, &QTimer::timeout, this, [this] {
        if (m_selectedChatId.isEmpty() || m_session->pendingReadWatermark.isEmpty()) {
            return;
        }
        const QString chatId = m_selectedChatId;
        const QString watermark = std::exchange(m_session->pendingReadWatermark, {});
        m_session->lastReadWatermark = watermark;
        m_client->request(QStringLiteral("chat.mark_read"),
                          {{QStringLiteral("chat_id"), chatId},
                           {QStringLiteral("up_to_message_id"), watermark}});
    });

    m_phoneHistoryTimer = new QTimer(this);
    m_phoneHistoryTimer->setSingleShot(true);
    m_phoneHistoryTimer->setInterval(kPhoneHistoryTimeoutMs);
    connect(m_phoneHistoryTimer, &QTimer::timeout, this, [this] {
        if (m_session->phoneHistoryRequesting) {
            m_session->phoneHistoryRequesting = false;
            Q_EMIT messagesChanged();
        }
    });

    m_phoneHistorySettleTimer = new QTimer(this);
    m_phoneHistorySettleTimer->setSingleShot(true);
    m_phoneHistorySettleTimer->setInterval(50);
    connect(m_phoneHistorySettleTimer, &QTimer::timeout, this, [this] {
        if (m_session->phoneHistoryRequesting && m_session->phoneHistoryGeneration == m_session->generation) {
            m_session->phoneHistoryRequesting = false;
            m_phoneHistoryTimer->stop();
            Q_EMIT messagesChanged();
        }
    });

    // Search debounce (D5): same 180 ms window AppController used, so typing
    // feels identical on either stack while both are in the tree.
    m_searchDebounceTimer = new QTimer(this);
    m_searchDebounceTimer->setSingleShot(true);
    m_searchDebounceTimer->setInterval(kSearchDebounceMs);
    connect(m_searchDebounceTimer, &QTimer::timeout, this, &ProtocolController::runSearch);

    m_chatSearchDebounceTimer = new QTimer(this);
    m_chatSearchDebounceTimer->setSingleShot(true);
    m_chatSearchDebounceTimer->setInterval(kSearchDebounceMs);
    connect(m_chatSearchDebounceTimer, &QTimer::timeout, this, &ProtocolController::runChatSearch);

    if (auto *app = qobject_cast<QGuiApplication *>(QCoreApplication::instance())) {
        connect(app, &QGuiApplication::applicationStateChanged, this, [this] {
            sendSessionUpdate();
        });
    }
}

ProtocolController::~ProtocolController()
{
    // Tear the subscriptions down while their sinks (the view models) are still
    // alive, so no late event is routed to a dangling sink during member
    // destruction.
    delete m_connectionSub;
    delete m_loginSub;
    delete m_chatsSub;
    delete m_archivedSub;
    delete m_chatFoldersSub;
    delete m_typingSub;
    delete m_syncSub;
    // The transcript subscriptions belong to the warm windows, one each.
    closeAllMessageWindows();
    delete m_groupPolicySub;
    delete m_presenceSub;
    delete m_receiptsSub;
    delete m_pinnedSub;
    delete m_liveLocationsSub;
    delete m_forwardTargetsSub;
    delete m_transfersSub;
    delete m_chatMediaSub;
    delete m_starredSub;
    delete m_infoCardSub;
    delete m_groupMembersSub;
    delete m_chatMembersSub;
    delete m_blocklistSub;
    delete m_privacySub;
    delete m_preferencesSub;
    delete m_selfSub;
    delete m_logsSub;
    delete m_channelsSub;
    delete m_channelMessagesSub;
    m_chatMembersSub = nullptr;
    m_starredSub = nullptr;
    m_chatMediaSub = nullptr;
    m_infoCardSub = nullptr;
    m_groupMembersSub = nullptr;
    m_blocklistSub = nullptr;
    m_privacySub = nullptr;
    m_preferencesSub = nullptr;
    m_selfSub = nullptr;
    m_logsSub = nullptr;
    m_connectionSub = nullptr;
    m_loginSub = nullptr;
    m_chatsSub = nullptr;
    m_archivedSub = nullptr;
    m_chatFoldersSub = nullptr;
    m_chatsExtendPending = false;
    m_archivedExtendPending = false;
    m_typingSub = nullptr;
    m_syncSub = nullptr;
    m_messagesSub = nullptr;
    m_groupPolicySub = nullptr;
    m_presenceSub = nullptr;
    m_receiptsSub = nullptr;
    m_pinnedSub = nullptr;
    // Both banner subscriptions skip re-subscribing when the target chat has
    // not changed, so the remembered target has to be cleared here too: after a
    // teardown the subscription is gone but the chat is still selected, and
    // without this the banner never comes back on reconnect.
    m_pinnedChatId.clear();
    m_liveLocationsSub = nullptr;
    m_liveLocationsChatId.clear();
    m_forwardTargetsSub = nullptr;
    m_transfersSub = nullptr;
    if (m_client) {
        m_client->stop();
    }
    if (s_instance == this) {
        s_instance = nullptr;
    }
}

QString ProtocolController::daemonSocketPath()
{
    const QString runtimePath = QStandardPaths::writableLocation(QStandardPaths::RuntimeLocation);
    if (runtimePath.isEmpty()) {
        return {};
    }
    // The whatevr protocol socket lives under whatevr/ — see
    // whatevrd/internal/app/paths.go.
    return QDir(runtimePath).filePath(QStringLiteral("whatevr/whatevrd.sock"));
}

bool ProtocolController::daemonSocketExists() const
{
    return !m_socketPath.isEmpty() && QFileInfo::exists(m_socketPath);
}

void ProtocolController::start()
{
    if (m_connectionSub) {
        return; // already started
    }
    m_startupGraceTimer->start();
    // Both views are tiny object views observed for the whole session: the
    // connection view is the authoritative state source; subscribing the login
    // view attaches to the daemon's QR pairing flow while logged out (and simply
    // reports state otherwise).
    m_connectionSub = m_client->subscribe(QStringLiteral("connection"), {}, m_connectionModel);
    m_loginSub = m_client->subscribe(QStringLiteral("login"), {}, m_loginModel);
    m_chatFoldersSub = m_client->subscribe(QStringLiteral("chat_folders"), {}, m_chatFoldersModel);
    subscribeChats();
    // The typing, sync and transfers views are global (unfiltered) and observed
    // for the whole session, like connection/login. `transfers` carries only
    // downloads that are running right now, so it is empty almost always and
    // costs nothing to hold open.
    m_typingSub = m_client->subscribe(QStringLiteral("typing"), {}, m_typingModel);
    m_syncSub = m_client->subscribe(QStringLiteral("sync"), {}, m_syncModel);
    m_transfersSub = m_client->subscribe(QStringLiteral("transfers"), {}, m_transfersModel);
    // Own profile is used by the sidebar and preferences drive auto-download,
    // so both object views remain live for the whole frontend session.
    m_selfSub = m_client->subscribe(QStringLiteral("self"), {}, m_selfModel);
    m_preferencesSub = m_client->subscribe(QStringLiteral("preferences"), {}, m_preferencesModel);
    m_client->start();
}

// --- chat list (D2b1) ------------------------------------------------------

QString ProtocolController::chatFilterName() const
{
    switch (m_chatFilter) {
    case 1:
        return QStringLiteral("direct");
    case 2:
        return QStringLiteral("groups");
    case 3:
        return QStringLiteral("unread");
    case 4:
        return QStringLiteral("favorite");
    default:
        return QStringLiteral("all");
    }
}

void ProtocolController::subscribeChats()
{
    // A filter switch is a fresh subscription with new params; drop the old rows
    // first so the list never briefly shows the previous filter (rule 1: the
    // frontend does no filtering itself — the daemon returns exactly the window).
    delete m_chatsSub;
    delete m_archivedSub;
    m_chatsSub = nullptr;
    m_archivedSub = nullptr;
    m_chatsExtendPending = false;
    m_archivedExtendPending = false;
    m_chatsModel->onReset();
    m_archivedModel->onReset();

    // Active and archived are two disjoint `chats` subscriptions; both honour the
    // selected filter so the archived section narrows with the sidebar the same
    // way the active list does.
    m_chatsSub = m_client->subscribe(
        QStringLiteral("chats"),
         {{QStringLiteral("filter"), chatFilterName()},
          {QStringLiteral("archived"), false},
          {QStringLiteral("folder_id"), m_chatFolder > 0 ? QJsonValue(m_chatFolder) : QJsonValue()},
         {QStringLiteral("limit"), kChatPageSize}},
        m_chatsModel);
    m_archivedSub = m_client->subscribe(
        QStringLiteral("chats"),
         {{QStringLiteral("filter"), chatFilterName()},
          {QStringLiteral("archived"), true},
          {QStringLiteral("folder_id"), m_chatFolder > 0 ? QJsonValue(m_chatFolder) : QJsonValue()},
         {QStringLiteral("limit"), kChatPageSize}},
        m_archivedModel);
    // A rejected extend must not leave the list stuck refusing to ask again.
    connect(m_chatsSub, &Subscription::extendFailed, this,
            [this](const QString &code, const QString &message) {
                qWarning() << "protocol: chats extend failed:" << code << message;
                m_chatsExtendPending = false;
                Q_EMIT chatsChanged();
            });
    connect(m_archivedSub, &Subscription::extendFailed, this,
            [this](const QString &code, const QString &message) {
                qWarning() << "protocol: archived chats extend failed:" << code << message;
                m_archivedExtendPending = false;
                Q_EMIT archivedChanged();
            });
    // `ready` covers the latest subscribe/extend, so it is what clears the
    // in-flight guard and lets the next page be requested.
    connect(m_chatsModel, &CollectionViewModel::readyReceived, this, [this](bool) {
        m_chatsExtendPending = false;
    });
    connect(m_archivedModel, &CollectionViewModel::readyReceived, this, [this](bool) {
        m_archivedExtendPending = false;
    });
}

// Both chat lists are prefix windows, so they only grow away from the top of
// the list — PROTOCOL.md's `older` direction. The guards mirror the starred
// page: never extend before the first fill, past exhaustion, or twice over.
void ProtocolController::loadMoreChats()
{
    if (!m_chatsSub || m_chatsExtendPending || !m_chatsModel->isReady() || m_chatsModel->isExhausted()) {
        return;
    }
    m_chatsExtendPending = true;
    m_chatsSub->extend(kChatPageSize, QStringLiteral("older"));
}

void ProtocolController::loadMoreArchivedChats()
{
    if (!m_archivedSub || m_archivedExtendPending || !m_archivedModel->isReady()
        || m_archivedModel->isExhausted()) {
        return;
    }
    m_archivedExtendPending = true;
    m_archivedSub->extend(kChatPageSize, QStringLiteral("older"));
}

QAbstractItemModel *ProtocolController::chatsModel() const
{
    return m_chatsModel;
}

QAbstractItemModel *ProtocolController::chatFoldersModel() const
{
    return m_chatFoldersModel;
}

void ProtocolController::setChatFolder(int folder)
{
    if (folder < 0 || folder == m_chatFolder) return;
    m_chatFolder = folder;
    if (m_chatsSub) subscribeChats();
    Q_EMIT chatFolderChanged();
}

QAbstractItemModel *ProtocolController::archivedChatsModel() const
{
    return m_archivedModel;
}

int ProtocolController::archivedCount() const
{
    return m_archivedModel->count();
}

bool ProtocolController::archivedExhausted() const
{
    return m_archivedSub == nullptr || m_archivedModel->isExhausted();
}

bool ProtocolController::archivedLoading() const
{
    return m_archivedSub != nullptr && !m_archivedModel->isReady();
}

bool ProtocolController::chatTyping(const QString &chatId) const
{
    // The typing view is keyed by chat_id; a present row means someone is
    // composing in that chat.
    return !chatId.isEmpty() && m_typingModel->indexOfId(chatId) >= 0;
}

void ProtocolController::setChatFilter(int filter)
{
    if (filter < 0 || filter > 4) {
        filter = 0;
    }
    if (filter == m_chatFilter) {
        return;
    }
    m_chatFilter = filter;
    // Only resubscribe once started (the connection/chats subs exist); before
    // start() the new filter is picked up by the initial subscribeChats().
    if (m_chatsSub) {
        subscribeChats();
    }
    Q_EMIT chatFilterChanged();
    Q_EMIT chatsChanged();
}

bool ProtocolController::chatsLoading() const
{
    // Subscribed but the initial window hasn't landed yet.
    return !m_chatsModel->isReady();
}

bool ProtocolController::chatsEmpty() const
{
    return m_chatsModel->count() == 0;
}

bool ProtocolController::chatsExhausted() const
{
    return m_chatsSub == nullptr || m_chatsModel->isExhausted();
}

void ProtocolController::setChatPinned(const QString &chatId, bool pinned)
{
    if (chatId.isEmpty()) {
        return;
    }
    m_client->request(QStringLiteral("chat.pin"),
                      {{QStringLiteral("chat_id"), chatId}, {QStringLiteral("pinned"), pinned}});
}

void ProtocolController::setChatFavorite(const QString &chatId, bool favorite)
{
    if (chatId.isEmpty()) {
        return;
    }
    m_client->request(QStringLiteral("chat.favorite"),
                      {{QStringLiteral("chat_id"), chatId}, {QStringLiteral("favorite"), favorite}});
}

void ProtocolController::setChatArchived(const QString &chatId, bool archived)
{
    if (chatId.isEmpty()) {
        return;
    }
    m_client->request(QStringLiteral("chat.archive"),
                      {{QStringLiteral("chat_id"), chatId}, {QStringLiteral("archived"), archived}});
}

void ProtocolController::setChatMuted(const QString &chatId, bool muted, int durationSecs)
{
    if (chatId.isEmpty()) {
        return;
    }
    m_client->request(QStringLiteral("chat.mute"),
                      {{QStringLiteral("chat_id"), chatId},
                       {QStringLiteral("muted"), muted},
                       {QStringLiteral("duration_secs"), durationSecs}});
}

void ProtocolController::createChatFolder(const QString &name)
{
    m_client->request(QStringLiteral("chat_folder.create"), {{QStringLiteral("name"), name}},
                      [this](const QJsonObject &, const ProtocolError &) {
                          delete m_chatFoldersSub;
                          m_chatFoldersModel->onReset();
                          m_chatFoldersSub = m_client->subscribe(QStringLiteral("chat_folders"), {}, m_chatFoldersModel);
                      });
}
void ProtocolController::renameChatFolder(qint64 id, const QString &name)
{
    m_client->request(QStringLiteral("chat_folder.rename"), {{QStringLiteral("id"), id}, {QStringLiteral("name"), name}},
                      [this](const QJsonObject &, const ProtocolError &) {
                          delete m_chatFoldersSub;
                          m_chatFoldersModel->onReset();
                          m_chatFoldersSub = m_client->subscribe(QStringLiteral("chat_folders"), {}, m_chatFoldersModel);
                      });
}
void ProtocolController::deleteChatFolder(qint64 id)
{
    m_client->request(QStringLiteral("chat_folder.delete"), {{QStringLiteral("id"), id}},
                      [this](const QJsonObject &, const ProtocolError &) {
                          delete m_chatFoldersSub;
                          m_chatFoldersModel->onReset();
                          m_chatFoldersSub = m_client->subscribe(QStringLiteral("chat_folders"), {}, m_chatFoldersModel);
                      });
}
void ProtocolController::assignChatFolder(const QString &chatId, qint64 id)
{
    QJsonObject params{{QStringLiteral("chat_id"), chatId}};
    if (id > 0) params.insert(QStringLiteral("folder_id"), id);
    m_client->request(QStringLiteral("chat_folder.set_chat"), params);
}

// --- conversation + messages (D3b) ---------------------------------------

QAbstractItemModel *ProtocolController::messageListModel() const
{
    return m_messagePresentationModel;
}

QVariantMap ProtocolController::selectedChatItem() const
{
    if (m_selectedChatId.isEmpty()) {
        return {};
    }
    // The `chat` view is authoritative, but an open no longer waits for it, so
    // it may not have answered yet. Until it does, the chat-list row is the
    // same row from the same daemon-side data — good enough to name the chat
    // and count its unread while the first frame renders.
    const QVariantMap value = m_selectedChatModel->value();
    return value.isEmpty() ? knownChatRow(m_selectedChatId) : value;
}

void ProtocolController::selectedChatLookupFailed(const QString &chatId, const QString &code,
                                                  const QString &message)
{
    if (m_selectedChatId != chatId || (code == QLatin1String("io") && m_selectedChatSub)) {
        return;
    }
    qWarning() << "protocol: selected chat subscribe failed:" << code << message;
    QTimer::singleShot(0, this, [this, chatId] {
        if (m_selectedChatId == chatId && !m_selectedChatModel->isPresent()) {
            m_waitingForSelectedChatItem = false;
            setSelectedChat({}, {}, {});
        }
    });
}

QString ProtocolController::selectedChatName() const
{
    const QString name = selectedChatItem().value(QStringLiteral("name")).toString();
    return name.isEmpty() ? m_selectedChatId.section(QLatin1Char('@'), 0, 0) : name;
}

QString ProtocolController::selectedChatAvatarLocalPath() const
{
    return selectedChatItem().value(QStringLiteral("avatar_path")).toString();
}

int ProtocolController::selectedChatUnreadCount() const
{
    return selectedChatItem().value(QStringLiteral("unread")).toInt();
}

QVariantMap ProtocolController::knownChatRow(const QString &chatId) const
{
    // Both chat-list windows are the same `chats` view under different params,
    // so either may hold the row. Empty when the chat is outside both windows —
    // the caller then falls back to the `chat` object view.
    const QVariantMap row = m_chatsModel->itemById(chatId);
    return row.isEmpty() ? m_archivedModel->itemById(chatId) : row;
}

bool ProtocolController::selectedChatHistoryExhausted() const
{
    return selectedChatItem().value(QStringLiteral("history_exhausted")).toBool();
}

bool ProtocolController::messagesLoading() const
{
    return hasSelectedChat() && (m_session->waitingInitialMessages || !m_messagesModel->isReady());
}

bool ProtocolController::messagesEmpty() const
{
    return m_messagesModel->count() == 0;
}

void ProtocolController::selectChat(const QString &chatId)
{
    if (chatId == m_selectedChatId) {
        return;
    }
    if (chatId.isEmpty()) {
        setSelectedChat({}, {}, {});
        return;
    }

    setSelectedChat(chatId, {}, {});
}

void ProtocolController::setSelectedChat(const QString &chatId, const QString &anchor, const QString &jumpMessageId)
{
    const bool selectionChanged = chatId != m_selectedChatId;
    // An anchor derived from the chat-list row when the caller supplied none;
    // it lets an open skip waiting on the `chat` view (see below).
    QString resolvedAnchor;
    m_selectedChatId = chatId;
    m_stickerController->setChatId(chatId);
    if (selectionChanged) {
        m_session->pendingReadWatermark.clear();
        m_session->lastReadWatermark.clear();
        m_readTimer->stop();
        // The in-chat search is scoped to one conversation; switching ends it.
        closeChatSearch();

        m_waitingForSelectedChatItem = false;
        delete m_selectedChatSub;
        m_selectedChatSub = nullptr;
        delete m_groupPolicySub;
        m_groupPolicySub = nullptr;
        m_groupPolicyModel->onReset();
        m_selectedChatModel->onReset();
        if (!chatId.isEmpty()) {
            // Waiting on the `chat` view before asking for messages made every
            // open two serialized round trips, for no reason other than reading
            // the unread count to pick the anchor. The chat list already holds
            // that row — the user just clicked it — so take the anchor from
            // there and let both subscriptions fly together. The wait survives
            // only for opens where the chat is not in a loaded window
            // (notification click, whatevr:// URI).
            if (anchor.isEmpty()) {
                const QVariantMap row = knownChatRow(chatId);
                if (!row.isEmpty()) {
                    resolvedAnchor = row.value(QStringLiteral("unread")).toInt() > 0
                        ? QStringLiteral("unread")
                        : QStringLiteral("latest");
                }
            }
            m_waitingForSelectedChatItem = anchor.isEmpty() && resolvedAnchor.isEmpty();
            m_selectedChatSub = m_client->subscribe(
                QStringLiteral("chat"), {{QStringLiteral("chat_id"), chatId}}, m_selectedChatModel);
            if (chatId.endsWith(QStringLiteral("@g.us"))) {
                m_groupPolicySub = m_client->subscribe(
                    QStringLiteral("group"), {{QStringLiteral("chat_id"), chatId}}, m_groupPolicyModel);
            }
            connect(m_selectedChatSub, &Subscription::failed, this,
                    [this, chatId](const QString &code, const QString &message) {
                        selectedChatLookupFailed(chatId, code, message);
                    });
        }
    }
    m_session->phoneHistoryRequesting = false;
    m_phoneHistoryTimer->stop();
    m_phoneHistorySettleTimer->stop();

    if (selectionChanged) {
        Q_EMIT this->selectionChanged();
        // composerEnabled reads the selection but is notified by composerChanged,
        // so a QML binding on it only re-evaluates when this fires.
        Q_EMIT composerChanged();
    }
    sendSessionUpdate();
    updatePresenceSubscription();
    updatePinnedSubscription();
    updateLiveLocationsSubscription();
    updateChatMembersSubscription();

    if (chatId.isEmpty()) {
        detachVisibleMessageWindow();
        Q_EMIT unreadAnchorChanged();
        Q_EMIT messagesChanged();
        return;
    }

    // effectiveAnchor is the caller's, or the one derived from the chat-list
    // row above. Only a genuinely unknown chat leaves it empty, and only then
    // do we park until the `chat` view answers.
    const QString effectiveAnchor = anchor.isEmpty() ? resolvedAnchor : anchor;

    if (effectiveAnchor.isEmpty()) {
        detachVisibleMessageWindow();
        m_session->waitingInitialMessages = true;
        Q_EMIT messagesChanged();
        return;
    }

    if (!m_conversationVisible) {
        detachVisibleMessageWindow();
        m_session->requestedAnchor = effectiveAnchor;
        m_session->effectiveAnchor = effectiveAnchor;
        m_session->pendingJumpMessageId = jumpMessageId;
        Q_EMIT messagesChanged();
        return;
    }

    subscribeMessages(effectiveAnchor, jumpMessageId);
}

// --- warm transcript pool ---------------------------------------------------

QVariantList ProtocolController::warmWindows() const
{
    QVariantList out;
    out.reserve(m_messageWindows.size());
    for (int i = 0; i < m_messageWindows.size(); ++i) {
        out.append(warmWindowAt(i));
    }
    return out;
}

QVariantMap ProtocolController::warmWindowAt(int index) const
{
    if (index < 0 || index >= m_messageWindows.size()) {
        return {};
    }
    const ConversationSession *window = m_messageWindows.at(index);
    if (!window) {
        return {{QStringLiteral("chatId"), QString()},
                {QStringLiteral("active"), false},
                {QStringLiteral("session"), QVariant::fromValue<QObject *>(nullptr)},
                {QStringLiteral("model"), QVariant::fromValue<QObject *>(nullptr)}};
    }
    // Which pane is the conversation, decided here rather than in QML.
    //
    // A window is keyed by chat *and* anchor, so a jump into a chat's history
    // leaves that chat holding two of them: the live edge it was opened at and
    // the anchored one it jumped to. Both are warm on purpose, which is what
    // makes going back to the bottom free. But the panes used to work out which
    // of them was on screen by comparing chat ids, and both matched: two panes
    // drew at once, both claimed the conversation's message-view pointer, and
    // both answered every jump result. The one that did not hold the target
    // announced it missing and kept its highlight. Only the window the
    // controller is actually driving is the current one.
    return {{QStringLiteral("chatId"), window->chatId},
            {QStringLiteral("active"), window == m_session},
            {QStringLiteral("session"), QVariant::fromValue<QObject *>(const_cast<ConversationSession *>(window))},
            {QStringLiteral("model"), QVariant::fromValue<QObject *>(window->presentation)}};
}

ConversationSession *ProtocolController::warmWindowFor(const QString &chatId,
                                                                    const QString &anchor) const
{
    if (chatId.isEmpty()) {
        return nullptr;
    }
    for (ConversationSession *window : m_messageWindows) {
        // A window whose subscribe was rejected is not warm, whatever else it
        // still holds: taking it back would hand the reader the same error
        // again and never ask the daemon, which is what made Retry do nothing.
        if (window && !window->failed && window->chatId == chatId && window->anchor == anchor) {
            return window;
        }
    }
    return nullptr;
}


// Reopening a chat that is still parked reopens it where it was parked.
//
// Without this, leaving a conversation and coming back recomputes the anchor
// from the unread badge, so a chat parked at `latest` would be asked for at
// `unread` and rebuilt from scratch, having been warm the whole time. Keeping
// the reader's place is also the better answer on its own terms: they were just
// here, and the divider is for a chat you are arriving at, not returning to.
QString ProtocolController::anchorForOpening(const QString &chatId, const QString &requested) const
{
    for (ConversationSession *window : m_messageWindows) {
        if (window && window->chatId == chatId) {
            return window->anchor;
        }
    }
    return requested;
}

void ProtocolController::connectMessageWindow(ConversationSession *window)
{
    // Every session stays connected while it is warm, so an off-screen chat
    // keeps its transcript correct. Each handler writes only its own session,
    // so nothing a background chat does can reach the one on screen.
    CollectionViewModel *source = window->source;
    connect(source, &CollectionViewModel::readyReceived, this, [this, window](bool exhausted) {
        onMessagesReady(window, exhausted);
    });
    connect(source, &QAbstractItemModel::modelReset, this, [this, window] {
        onMessagesReset(window);
    });
    connect(source, &CollectionViewModel::countChanged, this, [this, window] {
        publishSession(window);
    });
    connect(source, &QAbstractItemModel::rowsInserted, this,
            [this, window](const QModelIndex &, int, int) {
        if (window != m_session) {
            return;
        }
        // The first row of an open is the moment the window stops being a round
        // trip and starts being a rendering problem, so it is the phase that
        // splits the open budget in two.
        if (m_openClock.isValid() && m_openPhaseRows == 0) {
            markChatOpenPhase(QStringLiteral("first-row"));
        }
        if (window->phoneHistoryRequesting && window->phoneHistoryGeneration == window->generation
            && window->presentation->oldestMessageId() != window->phoneHistoryOldestId) {
            // Backfills arrive as a burst of individual upserts. Restore the
            // viewport only after that burst settles, not after its first row.
            m_phoneHistorySettleTimer->start();
        }
    });
}

ConversationSession *ProtocolController::openMessageWindow(const QString &chatId,
                                                                        const QString &anchor)
{
    // A free slot first; failing that, the one used longest ago.
    int slot = m_messageWindows.indexOf(nullptr);
    if (slot < 0) {
        slot = m_messageWindowUse.isEmpty() ? 0 : m_messageWindowUse.last();
        closeMessageWindow(m_messageWindows.at(slot));
    }

    auto *window = new ConversationSession(this);
    window->chatId = chatId;
    window->anchor = anchor;
    window->source = new CollectionViewModel(this);
    // The transcript is the one view held newest-first. Its live edge is the
    // bottom of the screen, and a BottomToTop ListView draws row 0 there, so
    // pinning the newest message at row 0 makes the end the reader is looking
    // at the end the view measures from. Everything upward of it is then
    // estimated history, which is where an estimate belongs.
    window->source->setReverseOrder(true);
    window->presentation = new ProtocolMessageModel(window->source, this);
    window->presentation->setTransfersSource(m_transfersModel);
    connectMessageWindow(window);

    m_messageWindows[slot] = window;
    m_messageWindowUse.removeAll(slot);
    m_messageWindowUse.prepend(slot);
    return window;
}

void ProtocolController::closeMessageWindow(ConversationSession *window)
{
    if (!window) {
        return;
    }
    const int slot = m_messageWindows.indexOf(window);
    if (slot >= 0) {
        m_messageWindows[slot] = nullptr;
        m_messageWindowUse.removeAll(slot);
    }
    if (window == m_session) {
        // Back to the empty stand-in rather than to nullptr: everything that
        // reads the active transcript is entitled to find one there.
        m_session = m_idleSession;
        m_messagesModel = m_idleMessagesModel;
        m_messagePresentationModel = m_idleMessagePresentation;
        m_messagesSub = nullptr;
    }
    delete window->sub;
    delete window->presentation;
    delete window->source;
    delete window;
}

void ProtocolController::closeAllMessageWindows()
{
    const QList<ConversationSession *> windows = m_messageWindows;
    for (ConversationSession *window : windows) {
        closeMessageWindow(window);
    }
    m_messageWindows.fill(nullptr, kWarmChatWindows);
    m_messageWindowUse.clear();
    Q_EMIT warmWindowsChanged();
}

void ProtocolController::activateMessageWindow(ConversationSession *window)
{
    if (!window) {
        return;
    }
    m_session = window;
    m_messagesModel = window->source;
    m_messagePresentationModel = window->presentation;
    m_messagesSub = window->sub;

    const int slot = m_messageWindows.indexOf(window);
    if (slot >= 0) {
        m_messageWindowUse.removeAll(slot);
        m_messageWindowUse.prepend(slot);
    }
    Q_EMIT warmWindowsChanged();
}

// Leaving the conversation: no chat is on screen, but every chat that was warm
// stays warm. The transcript models are deliberately not reset here, which is
// the whole point of the pool; the panes hide themselves because none of their
// chat ids matches the (now empty) selection, and the rows they hold are still
// there when one of them does again.
void ProtocolController::detachVisibleMessageWindow()
{
    // The session keeps everything it holds; only the pointer that says which
    // one is on screen moves. Coming back to this chat is then taking its own
    // state back, rather than rebuilding it from a copy.
    m_session = m_idleSession;
    m_messagesModel = m_idleMessagesModel;
    m_messagePresentationModel = m_idleMessagePresentation;
    m_messagesSub = nullptr;
}

void ProtocolController::subscribeMessages(const QString &anchor, const QString &jumpMessageId)
{
    // An explicit anchor (including a same-chat jump) supersedes a pending
    // default unread/latest choice from the selected `chat` row.
    m_waitingForSelectedChatItem = false;
    if (m_session->phoneHistoryRequesting) {
        m_session->phoneHistoryRequesting = false;
        m_phoneHistoryTimer->stop();
        m_phoneHistorySettleTimer->stop();
    }
    if (m_readTimer->isActive() && !m_session->pendingReadWatermark.isEmpty()) {
        m_readTimer->stop();
        const QString watermark = std::exchange(m_session->pendingReadWatermark, {});
        m_session->lastReadWatermark = watermark;
        m_client->request(QStringLiteral("chat.mark_read"),
                          {{QStringLiteral("chat_id"), m_selectedChatId},
                           {QStringLiteral("up_to_message_id"), watermark}});
    }
    if (perfLogging()) {
        m_openClock.start();
        m_openPhaseRows = 0;
        qInfo("[perf] open %-10s %6.1f ms  chat=%s anchor=%s", "subscribe", 0.0,
              qPrintable(m_selectedChatId), qPrintable(anchor));
    }

    // Already warm *at the anchor being asked for*: its pane still holds its
    // rows and its subscription has been keeping them right the whole time it
    // was off screen. Switching to it is taking back the state it was left in,
    // and nothing else at all. No round trip, no reset, no rows rebuilt.
    //
    // A different anchor over the same chat is a different window (the unread
    // divider and the live edge put the reader in different places), so it
    // falls through and is rebuilt into this chat's one slot rather than
    // claiming a second.
    if (ConversationSession *warm = warmWindowFor(m_selectedChatId, anchor)) {
        activateMessageWindow(warm);
        m_session->pendingJumpMessageId = jumpMessageId;
        m_session->reloading = false;
        m_session->displayedChatId = m_selectedChatId;
        Q_EMIT unreadAnchorChanged();
        Q_EMIT messagesChanged();
        markChatOpenPhase(QStringLiteral("warm"));
        if (!jumpMessageId.isEmpty()) {
            // The rows are already here, so the jump can be answered now rather
            // than waiting for a fill that is not coming.
            const QString jumpId = std::exchange(m_session->pendingJumpMessageId, {});
            if (m_messagePresentationModel->indexOf(jumpId) >= 0) {
                Q_EMIT messageJumpReady(jumpId);
            } else {
                Q_EMIT messageJumpUnavailable(jumpId);
            }
        }
        return;
    }

    // Cold: a slot has to hold this chat, and the daemon has to be asked.
    ConversationSession *window = openMessageWindow(m_selectedChatId, anchor);
    activateMessageWindow(window);
    ++m_session->generation;

    m_session->requestedAnchor = anchor;
    m_session->effectiveAnchor = anchor;
    m_session->pendingJumpMessageId = jumpMessageId;
    m_session->pendingExtendDirection.clear();
    // A re-subscribe within the chat already on screen is a reload, not an open.
    // The rows go away for a round trip, but the conversation does not, and the
    // pane must not collapse to its empty state and back in between.
    m_session->reloading = m_session->displayedChatId == m_selectedChatId && !m_selectedChatId.isEmpty();
    m_session->displayedChatId.clear();
    m_session->errorText.clear();
    m_session->waitingInitialMessages = true;
    m_session->refillingAfterReset = false;
    m_session->olderLoading = false;
    m_session->newerLoading = false;
    m_session->canLoadOlder = false;
    m_session->canLoadNewer = false;
    m_session->olderFailed = false;
    m_session->newerFailed = false;
    m_session->atLiveEdge = anchor == QLatin1String("latest");
    m_session->unreadAnchorMessageId.clear();
    m_session->unreadAnchorCount = anchor == QLatin1String("unread") ? selectedChatUnreadCount() : 0;
    m_session->unreadAnchorResolving = anchor == QLatin1String("unread");
    m_messagesModel->onReset();
    Q_EMIT unreadAnchorChanged();
    Q_EMIT messagesChanged();
    Q_EMIT warmWindowsChanged();

    m_messagesSub = m_client->subscribe(
        QStringLiteral("messages"),
        {{QStringLiteral("chat_id"), m_selectedChatId},
         {QStringLiteral("limit"), kMessagePageSize},
         {QStringLiteral("anchor"), anchor}},
        m_messagesModel);
    window->sub = m_messagesSub;
    // A reconnect re-issues every warm subscription at once, so these fire for
    // chats that are not on screen. Each one writes its own session, which is
    // what used to need a guard: a background window's subscribe result was
    // landing on the visible conversation's unread anchor and error text.
    connect(m_messagesSub, &Subscription::subscribed, this, [this, window](const QVariantMap &meta) {
        onMessagesSubscribed(window, meta);
    });
    connect(m_messagesSub, &Subscription::failed, this,
            [this, window](const QString &code, const QString &message) {
                onMessagesFailed(window, code, message);
            });
    connect(m_messagesSub, &Subscription::extendFailed, this,
            [this, window](const QString &code, const QString &message) {
                Q_UNUSED(code)
                Q_UNUSED(message)
                const QString direction = std::exchange(window->pendingExtendDirection, {});
                if (direction == QLatin1String("older")) {
                    window->olderLoading = false;
                    window->olderFailed = true;
                } else if (direction == QLatin1String("newer")) {
                    window->newerLoading = false;
                    window->newerFailed = true;
                }
                publishSession(window);
            });
}

void ProtocolController::onMessagesSubscribed(ConversationSession *session, const QVariantMap &meta)
{
    if (session == m_session) {
        markChatOpenPhase(QStringLiteral("subscribed"));
    }
    session->failed = false;
    session->errorText.clear();
    if (session->requestedAnchor == QLatin1String("unread")) {
        session->unreadAnchorMessageId = meta.value(QStringLiteral("anchor_id")).toString();
        session->unreadAnchorResolving = false;
        if (session->unreadAnchorMessageId.isEmpty()) {
            // No unread anchor means the daemon deliberately degraded to the
            // live edge; there is no divider to render.
            session->unreadAnchorCount = 0;
            noteReachedLiveEdge(session);
        }
        publishUnreadAnchor(session);
    }
}

void ProtocolController::onMessagesReady(ConversationSession *session, bool exhausted)
{
    session->errorText.clear();
    if (!session->pendingExtendDirection.isEmpty()) {
        const QString direction = std::exchange(session->pendingExtendDirection, {});
        if (direction == QLatin1String("older")) {
            session->olderLoading = false;
            session->olderFailed = false;
            session->canLoadOlder = !exhausted;
        } else {
            session->newerLoading = false;
            session->newerFailed = false;
            session->canLoadNewer = !exhausted;
            if (exhausted) {
                noteReachedLiveEdge(session);
            }
        }
        if (session->refillingAfterReset) {
            session->refillingAfterReset = false;
            session->waitingInitialMessages = false;
            session->displayedChatId = session->chatId;
            session->reloading = false;
        }
        publishSession(session);
        return;
    }

    if (!session->waitingInitialMessages) {
        return;
    }
    if (session == m_session) {
        markChatOpenPhase(QStringLiteral("ready"));
    }
    session->waitingInitialMessages = false;
    session->displayedChatId = session->chatId;
    session->reloading = false;
    if (session->refillingAfterReset) {
        session->refillingAfterReset = false;
        session->olderFailed = false;
        session->newerFailed = false;
        publishSession(session);
        return;
    }
    if (session->effectiveAnchor == QLatin1String("latest")) {
        session->canLoadOlder = !exhausted;
        session->canLoadNewer = false;
        noteReachedLiveEdge(session);
    } else {
        // Initial anchored exhaustion describes both frontiers together. When
        // false, probe each independently as the viewport approaches it.
        session->atLiveEdge = exhausted;
        session->canLoadOlder = !exhausted;
        session->canLoadNewer = !exhausted;
        if (exhausted) {
            noteReachedLiveEdge(session);
        }
    }

    publishSession(session);
    const QString jumpId = std::exchange(session->pendingJumpMessageId, {});
    if (jumpId.isEmpty()) {
        return;
    }
    if (session->presentation->indexOf(jumpId) >= 0) {
        session->jumpFallbackAnchor.clear();
        if (session == m_session) {
            Q_EMIT messageJumpReady(jumpId);
        }
        return;
    }
    if (session == m_session) {
        Q_EMIT messageJumpUnavailable(jumpId);
    }
    retryJumpAtFallbackAnchor(session);
}

// The jump landed in a window that does not hold it. Come back on the next
// turn of the loop and ask for the chat at the fallback anchor, unless the
// reader has moved on in the meantime.
void ProtocolController::retryJumpAtFallbackAnchor(ConversationSession *session)
{
    const QString fallback = session->jumpFallbackAnchor.isEmpty()
        ? QStringLiteral("latest")
        : std::exchange(session->jumpFallbackAnchor, {});
    const int generation = session->generation;
    QTimer::singleShot(0, this, [this, session, fallback, generation] {
        if (m_session == session && session->generation == generation && m_conversationVisible) {
            subscribeMessages(fallback);
        }
    });
}

void ProtocolController::onMessagesFailed(ConversationSession *session, const QString &code,
                                          const QString &message)
{
    if (code == QLatin1String("io") && session->sub) {
        return; // live subscriptions auto-resubscribe after reconnect
    }
    // Whatever happens next, this window will never fill: the daemon refused
    // it. Say so on the session so a re-open, and the Retry button in
    // particular, builds a new subscription rather than finding this one warm.
    session->failed = true;
    session->waitingInitialMessages = false;
    session->unreadAnchorResolving = false;
    const QString jumpId = std::exchange(session->pendingJumpMessageId, {});
    if (!jumpId.isEmpty()) {
        if (session == m_session) {
            Q_EMIT messageJumpUnavailable(jumpId);
        }
        retryJumpAtFallbackAnchor(session);
    } else {
        session->errorText = message;
        session->reloading = false;
    }
    publishUnreadAnchor(session);
    publishSession(session);
}

void ProtocolController::onMessagesReset(ConversationSession *session)
{
    if (session->chatId.isEmpty()) {
        return;
    }
    // Reached both from a daemon-side `reset` (queue overflow) and from
    // subscribeMessages() resetting the model itself. Either way the chat is
    // not going anywhere, so the rows come back rather than the pane falling
    // through to its empty state.
    session->reloading = session->reloading || session->displayedChatId == session->chatId;
    session->displayedChatId.clear();
    // Off screen there is nothing to hold a spinner for; the refill lands on
    // its own and the session is correct again by the time it comes back.
    if (session != m_session || !m_conversationVisible) {
        session->reloading = false;
        session->waitingInitialMessages = session != m_session;
        publishSession(session);
        return;
    }
    const bool wasWaitingInitial = session->waitingInitialMessages;
    session->waitingInitialMessages = true;
    const bool inConnectionReset = m_clientReady && session->sub && session->sub->isActive();
    session->refillingAfterReset = inConnectionReset && !wasWaitingInitial;
    if (!inConnectionReset) {
        session->pendingExtendDirection.clear();
    }
    if (!inConnectionReset || session->pendingExtendDirection.isEmpty()) {
        session->olderLoading = false;
        session->newerLoading = false;
    }
    if (!inConnectionReset) {
        session->canLoadOlder = false;
        session->canLoadNewer = false;
    }
    if (!inConnectionReset && session->requestedAnchor == QLatin1String("unread")) {
        session->unreadAnchorMessageId.clear();
        session->unreadAnchorResolving = true;
        publishUnreadAnchor(session);
    }
    publishSession(session);
}

void ProtocolController::extendMessages(const QString &direction, bool force)
{
    if (!m_messagesSub || !m_session->pendingExtendDirection.isEmpty()) {
        return;
    }
    if (!force && direction == QLatin1String("older") && !m_session->canLoadOlder) {
        return;
    }
    if (!force && direction == QLatin1String("newer") && !m_session->canLoadNewer) {
        return;
    }
    m_session->pendingExtendDirection = direction;
    if (direction == QLatin1String("older")) {
        m_session->olderLoading = true;
    } else {
        m_session->newerLoading = true;
    }
    Q_EMIT messagesChanged();
    m_messagesSub->extend(kMessagePageSize, direction);
}

void ProtocolController::loadOlderMessages()
{
    if (m_session->olderFailed) {
        m_session->olderFailed = false;
        Q_EMIT messagesChanged();
    }
    extendMessages(QStringLiteral("older"));
}

void ProtocolController::loadNewerMessages()
{
    if (m_session->newerFailed) {
        m_session->newerFailed = false;
        Q_EMIT messagesChanged();
    }
    extendMessages(QStringLiteral("newer"));
}

void ProtocolController::requestOlderMessagesFromPhone()
{
    if (m_selectedChatId.isEmpty() || m_session->phoneHistoryRequesting || selectedChatHistoryExhausted()) {
        return;
    }
    const QString chatId = m_selectedChatId;
    const int generation = m_session->generation;
    m_session->phoneHistoryRequesting = true;
    m_session->phoneHistoryOldestId = m_messagePresentationModel->oldestMessageId();
    m_session->phoneHistoryGeneration = generation;
    m_phoneHistoryTimer->start();
    Q_EMIT messagesChanged();
    m_client->request(QStringLiteral("chat.request_older"),
                      {{QStringLiteral("chat_id"), chatId}},
                      [this, chatId, generation](const QJsonObject &result, const ProtocolError &error) {
                          if (chatId != m_selectedChatId || generation != m_session->generation) {
                              return;
                          }
                          if (error.isError() || !result.value(QStringLiteral("requested")).toBool()) {
                              m_session->phoneHistoryRequesting = false;
                              m_phoneHistoryTimer->stop();
                              Q_EMIT messagesChanged();
                              return;
                          }
                          // Grow the local window now; later backfilled rows then
                          // enter it through ordinary messages-view upserts.
                          extendMessages(QStringLiteral("older"), true);
                      });
}

void ProtocolController::jumpToMessage(const QString &messageId)
{
    if (messageId.isEmpty() || m_selectedChatId.isEmpty()) {
        Q_EMIT messageJumpUnavailable(messageId);
        return;
    }
    if (m_messagePresentationModel->indexOf(messageId) >= 0) {
        QTimer::singleShot(0, this, [this, messageId] { Q_EMIT messageJumpReady(messageId); });
        return;
    }
    m_session->jumpFallbackAnchor = m_session->effectiveAnchor.isEmpty() ? QStringLiteral("latest") : m_session->effectiveAnchor;
    subscribeMessages(messageId, messageId);
}

// noteReachedLiveEdge records that the window now contains the newest message.
//
// The anchor is where the window was *opened*, which is not where it is: a chat
// opened on its unread divider keeps `m_session->effectiveAnchor == "unread"` for the
// rest of the session even after extending forward to the newest message. Every
// caller that treats the anchor as "where we are" was therefore wrong about a
// chat with unread messages, and jumpToBottom() below re-subscribed on every
// single send because of it.
void ProtocolController::publishSession(ConversationSession *session)
{
    session->notifyChanged();
    if (session == m_session) {
        Q_EMIT messagesChanged();
    }
}

void ProtocolController::publishUnreadAnchor(ConversationSession *session)
{
    session->notifyUnreadAnchorChanged();
    if (session == m_session) {
        Q_EMIT unreadAnchorChanged();
    }
}

void ProtocolController::noteReachedLiveEdge(ConversationSession *session)
{
    session->atLiveEdge = true;
    session->effectiveAnchor = QStringLiteral("latest");
}

void ProtocolController::jumpToBottom()
{
    // Re-subscribing resets the model, which tears down and rebuilds every
    // delegate and blanks the conversation column for a socket round trip. Only
    // worth it when the window genuinely does not reach the newest message:
    // while it does, the caller's own scroll is the whole job.
    if (m_selectedChatId.isEmpty() || m_session->effectiveAnchor == QLatin1String("latest")
        || (m_session->atLiveEdge && !m_session->canLoadNewer)) {
        return;
    }
    subscribeMessages(QStringLiteral("latest"));
}

void ProtocolController::showMessageInChat(const QString &chatId, const QString &messageId)
{
    if (chatId.isEmpty() || messageId.isEmpty()) {
        Q_EMIT messageJumpUnavailable(messageId);
        return;
    }
    m_session->jumpFallbackAnchor = QStringLiteral("latest");
    setSelectedChat(chatId, messageId, messageId);
}

void ProtocolController::retryMessages()
{
    if (m_selectedChatId.isEmpty()) {
        return;
    }
    const QString anchor = m_session->requestedAnchor.isEmpty() ? QStringLiteral("latest") : m_session->requestedAnchor;
    subscribeMessages(anchor, m_session->pendingJumpMessageId);
}

void ProtocolController::markSelectedChatViewed(const QString &upToMessageId)
{
    if (m_selectedChatId.isEmpty() || upToMessageId.isEmpty()) {
        return;
    }
    const int candidate = m_messagePresentationModel->indexOf(upToMessageId);
    const int pending = m_messagePresentationModel->indexOf(m_session->pendingReadWatermark);
    const int sent = m_messagePresentationModel->indexOf(m_session->lastReadWatermark);
    if (!m_session->pendingReadWatermark.isEmpty() && pending < 0) {
        return;
    }
    // A watermark only ever moves forward in time. The rows are held
    // newest-first, so "older than what we already marked" is a *larger* index,
    // not a smaller one.
    //
    // Each side is guarded on the watermark existing at all, rather than
    // leaning on -1 comparing the way we want. Under the old ordering a missing
    // watermark's -1 was smaller than every real row and so was harmless; with
    // the comparison the other way round it would read as newer than
    // everything, and the very first watermark of a chat would be refused.
    const bool wouldRegress = (pending >= 0 && candidate > pending)
        || (sent >= 0 && candidate > sent);
    if (candidate >= 0 && wouldRegress) {
        return;
    }
    m_session->pendingReadWatermark = upToMessageId;
    m_readTimer->start();
}

void ProtocolController::markAllChatsRead()
{
    // Ack-then-lifecycle like its siblings: badges clear through the `chats`
    // view, failures surface as a transient message action error.
    m_client->request(QStringLiteral("chat.mark_all_read"), {},
                      [this](const QJsonObject &, const ProtocolError &error) {
                          if (error.isError()) {
                              Q_EMIT messageActionFailed(error.message.isEmpty() ? i18nc("@info", "Unable to mark all chats read")
                                                                                 : error.message);
                          }
                      });
}

void ProtocolController::setConversationVisible(bool visible)
{
    if (visible == m_conversationVisible) {
        return;
    }
    m_conversationVisible = visible;
    // Presence is subscribed for exactly what the conversation is showing: a
    // hidden conversation drops it (and with it the upstream WhatsApp presence
    // demand), a shown one re-establishes it alongside the messages window.
    updatePresenceSubscription();
    // The pinned banner and the composer's mention roster are part of that same
    // conversation view.
    updatePinnedSubscription();
    updateLiveLocationsSubscription();
    updateChatMembersSubscription();
    if (!visible) {
        m_session->phoneHistoryRequesting = false;
        m_phoneHistoryTimer->stop();
        m_phoneHistorySettleTimer->stop();
        detachVisibleMessageWindow();
        sendSessionUpdate();
        Q_EMIT messagesChanged();
        return;
    }
    sendSessionUpdate();
    if (!m_selectedChatId.isEmpty()) {
        if (m_waitingForSelectedChatItem) {
            return;
        }
        const QString fresh = selectedChatUnreadCount() > 0 ? QStringLiteral("unread") : QStringLiteral("latest");
        subscribeMessages(anchorForOpening(m_selectedChatId, fresh));
    }
}

void ProtocolController::sendSessionUpdate()
{
    if (!m_clientReady) {
        return;
    }
    const auto *app = qobject_cast<QGuiApplication *>(QCoreApplication::instance());
    const bool focused = app && app->applicationState() == Qt::ApplicationActive;
    m_client->request(QStringLiteral("session.update"),
                      {{QStringLiteral("focused"), focused},
                       {QStringLiteral("active_chat_id"), m_conversationVisible ? m_selectedChatId : QString()}});
}

// --- conversation header presence (D3c) ------------------------------------

void ProtocolController::updatePresenceSubscription()
{
    const QString target = m_conversationVisible ? m_selectedChatId : QString();
    if (target == m_presenceChatId) {
        return;
    }
    m_presenceChatId = target;
    delete m_presenceSub;
    m_presenceSub = nullptr;
    m_presenceModel->onReset();
    if (!target.isEmpty()) {
        m_presenceSub = m_client->subscribe(QStringLiteral("presence"),
                                            {{QStringLiteral("chat_id"), target}}, m_presenceModel);
    }
    Q_EMIT presenceChanged();
}

QString ProtocolController::selectedChatPresenceText() const
{
    if (m_selectedChatId.isEmpty()) {
        return {};
    }
    // Typing wins over availability, exactly as the gRPC header did. Composing
    // arrives unsolicited on the global `typing` view; availability only on the
    // per-chat `presence` view we subscribed for this chat.
    if (chatTyping(m_selectedChatId)) {
        return i18nc("@info chat presence", "typing...");
    }
    const QVariantMap item = m_presenceModel->itemById(m_selectedChatId);
    if (item.isEmpty()) {
        return {};
    }
    if (item.value(QStringLiteral("availability")).toString() == QLatin1String("online")) {
        return i18nc("@info chat presence", "online");
    }
    return formatLastSeen(item.value(QStringLiteral("last_seen_unix")).toLongLong());
}

// --- message info receipts (D3c) -------------------------------------------

void ProtocolController::openMessageReceipts(const QString &messageId)
{
    delete m_receiptsSub;
    m_receiptsSub = nullptr;
    m_receiptsModel->onReset();
    m_receiptsMessageId = messageId;
    m_receiptsError.clear();
    Q_EMIT messageReceiptsChanged();
    if (messageId.isEmpty()) {
        return;
    }

    m_receiptsSub = m_client->subscribe(QStringLiteral("receipts"),
                                        {{QStringLiteral("message_id"), messageId}}, m_receiptsModel);
    connect(m_receiptsSub, &Subscription::failed, this,
            [this, messageId](const QString &code, const QString &message) {
                if (m_receiptsMessageId != messageId) {
                    return; // a later dialog owns the view now
                }
                if (code == QLatin1String("io") && m_receiptsSub) {
                    return; // live subscriptions auto-resubscribe after reconnect
                }
                m_receiptsError = message;
                Q_EMIT messageReceiptsChanged();
            });
}

void ProtocolController::closeMessageReceipts()
{
    if (m_receiptsMessageId.isEmpty() && !m_receiptsSub) {
        return;
    }
    delete m_receiptsSub;
    m_receiptsSub = nullptr;
    m_receiptsMessageId.clear();
    m_receiptsError.clear();
    m_receiptsModel->onReset();
    Q_EMIT messageReceiptsChanged();
}

bool ProtocolController::messageReceiptsLoading() const
{
    return !m_receiptsMessageId.isEmpty() && m_receiptsError.isEmpty() && !m_receiptsModel->isReady();
}

bool ProtocolController::messageReceiptsIsGroup() const
{
    // Group-ness is the daemon's `chats` row flag; the dialog only ever opens on
    // a message of the selected chat.
    return selectedChatItem().value(QStringLiteral("is_group")).toBool();
}

qint64 ProtocolController::messageReceiptsSentTimestamp() const
{
    // The send time belongs to the message, not to a receipt; read it live off
    // the timeline row rather than copying it into dialog state.
    return m_messagesModel->itemById(m_receiptsMessageId).value(QStringLiteral("timestamp")).toLongLong();
}

QVariantList ProtocolController::messageReceipts() const
{
    QVariantList rows;
    rows.reserve(m_receiptsModel->count());
    for (int row = 0; row < m_receiptsModel->count(); ++row) {
        rows.append(m_receiptsModel->data(m_receiptsModel->index(row, 0), CollectionViewModel::ItemRole));
    }
    return rows;
}

QVariantMap ProtocolController::directMessageReceipt() const
{
    // The daemon keys a direct chat's single aggregate row under this sentinel
    // (GetMessageInfo carries no jid for a 1:1 recipient).
    return m_receiptsModel->itemById(QStringLiteral("peer"));
}

// --- composer + send paths (D4a) -------------------------------------------

bool ProtocolController::composerEnabled() const
{
    return hasSelectedChat() && m_clientReady && selectedChatCanSend();
}

bool ProtocolController::selectedChatCanSend() const
{
    if (!m_selectedChatId.endsWith(QStringLiteral("@g.us"))) {
        return true;
    }
    const QVariantMap policy = m_groupPolicyModel->value();
    const QString role = policy.value(QStringLiteral("my_role")).toString();
    return !policy.value(QStringLiteral("announce")).toBool()
           || role == QLatin1String("admin") || role == QLatin1String("superadmin");
}

void ProtocolController::dismissUnreadAnchor()
{
    const bool changed = !m_session->unreadAnchorMessageId.isEmpty() || m_session->unreadAnchorCount != 0 || m_session->unreadAnchorResolving;
    m_session->unreadAnchorMessageId.clear();
    m_session->unreadAnchorCount = 0;
    m_session->unreadAnchorResolving = false;
    if (changed) {
        Q_EMIT unreadAnchorChanged();
    }
}

void ProtocolController::sendText(const QString &text, const QString &replyToMessageId, const QStringList &mentionedJids)
{
    const QString trimmed = plainTextFromQtRichText(text).trimmed();
    if (m_selectedChatId.isEmpty() || trimmed.isEmpty() || m_sendInFlight || !selectedChatCanSend()) {
        return;
    }

    setSelectedChatComposing(false);
    dismissUnreadAnchor();
    Q_EMIT messageSent();

    QJsonObject params{{QStringLiteral("chat_id"), m_selectedChatId}, {QStringLiteral("text"), trimmed}};
    if (const QString reply = replyToMessageId.trimmed(); !reply.isEmpty()) {
        params.insert(QStringLiteral("reply_to"), reply);
    }
    if (!mentionedJids.isEmpty()) {
        params.insert(QStringLiteral("mentions"), QJsonArray::fromStringList(mentionedJids));
    }

    m_sendInFlight = true;
    m_composerErrorText.clear();
    Q_EMIT composerChanged();

    m_client->request(QStringLiteral("send.text"), params, [this](const QJsonObject &, const ProtocolError &error) {
        m_sendInFlight = false;
        m_composerErrorText = error.isError()
            ? (error.message.isEmpty() ? i18nc("@info", "Unable to send message") : error.message)
            : QString();
        Q_EMIT composerChanged();
    });
}

void ProtocolController::scheduleText(const QString &text, qint64 sendAt)
{
    const QString trimmed = plainTextFromQtRichText(text).trimmed();
    if (m_selectedChatId.isEmpty() || trimmed.isEmpty() || sendAt <= QDateTime::currentSecsSinceEpoch()) {
        return;
    }
    m_client->request(QStringLiteral("schedule.text"),
                      {{QStringLiteral("chat_id"), m_selectedChatId},
                       {QStringLiteral("text"), trimmed},
                       {QStringLiteral("send_at"), sendAt}},
                      [this](const QJsonObject &, const ProtocolError &error) {
                          if (error.isError()) {
                              Q_EMIT messageActionFailed(error.message.isEmpty()
                                                             ? i18nc("@info", "Unable to schedule message")
                                                             : error.message);
                          }
                      });
}

QVariantList ProtocolController::scheduledMessages() const
{
    return m_scheduledMessages;
}

void ProtocolController::refreshScheduledMessages(const QString &chatId)
{
    m_client->request(QStringLiteral("schedule.list"),
                      {{QStringLiteral("chat_id"), chatId}},
                      [this](const QJsonObject &result, const ProtocolError &error) {
                          if (error.isError()) {
                              Q_EMIT messageActionFailed(error.message.isEmpty()
                                                             ? i18nc("@info", "Unable to load scheduled messages")
                                                             : error.message);
                              return;
                          }
                          m_scheduledMessages.clear();
                          for (const QJsonValue &value : result.value(QStringLiteral("messages")).toArray()) {
                              m_scheduledMessages.append(value.toObject().toVariantMap());
                          }
                          Q_EMIT scheduledMessagesChanged();
                      });
}

void ProtocolController::cancelScheduledMessage(qlonglong id, const QString &chatId)
{
    if (id <= 0) {
        return;
    }
    m_client->request(QStringLiteral("schedule.cancel"),
                      {{QStringLiteral("id"), id}},
                      [this, chatId](const QJsonObject &, const ProtocolError &error) {
                          if (error.isError()) {
                              Q_EMIT messageActionFailed(error.message.isEmpty()
                                                             ? i18nc("@info", "Unable to cancel scheduled message")
                                                             : error.message);
                              return;
                          }
                          refreshScheduledMessages(chatId);
                      });
}

void ProtocolController::sendMedia(const QString &fileUrl, const QString &caption, const QString &replyToMessageId, const QString &kind, bool viewOnce)
{
    if (m_selectedChatId.isEmpty() || fileUrl.isEmpty() || m_sendInFlight || !selectedChatCanSend()) {
        return;
    }

    const QUrl url(fileUrl);
    const QString filePath = url.isLocalFile() ? url.toLocalFile() : fileUrl;
    if (filePath.isEmpty()) {
        return;
    }

    setSelectedChatComposing(false);
    dismissUnreadAnchor();
    Q_EMIT messageSent();

    QJsonObject params{{QStringLiteral("chat_id"), m_selectedChatId},
                       {QStringLiteral("path"), filePath},
                       {QStringLiteral("caption"), plainTextFromQtRichText(caption).trimmed()}};
    if (const QString reply = replyToMessageId.trimmed(); !reply.isEmpty()) {
        params.insert(QStringLiteral("reply_to"), reply);
    }
    if (!kind.trimmed().isEmpty()) {
        params.insert(QStringLiteral("kind"), kind.trimmed());
    }
    if (viewOnce) {
        params.insert(QStringLiteral("view_once"), true);
    }

    m_sendInFlight = true;
    m_composerErrorText.clear();
    Q_EMIT composerChanged();

    m_client->request(QStringLiteral("send.media"), params, [this](const QJsonObject &, const ProtocolError &error) {
        m_sendInFlight = false;
        m_composerErrorText = error.isError()
            ? (error.message.isEmpty() ? i18nc("@info", "Unable to send media") : error.message)
            : QString();
        Q_EMIT composerChanged();
    });
}

// Batch twin of sendMedia: the daemon serializes the files, so a multi-pick
// (or a multi-file drop) goes out as one request instead of N racing ones
// that the single in-flight guard would drop after the first.
void ProtocolController::sendMediaBatch(const QVariantList &fileUrls, const QString &caption, const QString &replyToMessageId, const QString &kind, bool viewOnce)
{
    if (m_selectedChatId.isEmpty() || fileUrls.isEmpty() || m_sendInFlight || !selectedChatCanSend()) {
        return;
    }
    QJsonArray files;
    bool first = true;
    for (const QVariant &entry : fileUrls) {
        const QUrl url(entry.toString());
        const QString filePath = url.isLocalFile() ? url.toLocalFile() : entry.toString();
        if (filePath.isEmpty()) {
            continue;
        }
        QJsonObject file;
        file.insert(QStringLiteral("path"), filePath);
        // The shared caption rides on the first file only.
        file.insert(QStringLiteral("caption"), first ? plainTextFromQtRichText(caption).trimmed() : QString());
        files.append(file);
        first = false;
    }
    if (files.isEmpty()) {
        return;
    }

    setSelectedChatComposing(false);
    dismissUnreadAnchor();
    Q_EMIT messageSent();

    QJsonObject params{{QStringLiteral("chat_id"), m_selectedChatId},
                       {QStringLiteral("files"), files}};
    if (const QString reply = replyToMessageId.trimmed(); !reply.isEmpty()) {
        params.insert(QStringLiteral("reply_to"), reply);
    }
    if (!kind.trimmed().isEmpty()) {
        params.insert(QStringLiteral("kind"), kind.trimmed());
    }
    if (viewOnce) {
        params.insert(QStringLiteral("view_once"), true);
    }

    m_sendInFlight = true;
    m_composerErrorText.clear();
    Q_EMIT composerChanged();

    m_client->request(QStringLiteral("send.media_batch"), params, [this](const QJsonObject &result, const ProtocolError &error) {
        m_sendInFlight = false;
        if (error.isError()) {
            m_composerErrorText = error.message.isEmpty() ? i18nc("@info", "Unable to send media") : error.message;
        } else {
            const QVariantList failures = result.value(QStringLiteral("errors")).toArray().toVariantList();
            if (!failures.isEmpty()) {
                m_composerErrorText = i18nc("@info", "Some files could not be sent");
            }
        }
        Q_EMIT composerChanged();
    });
}

bool ProtocolController::sendClipboardImage(const QString &caption, const QString &replyToMessageId)
{
    if (m_selectedChatId.isEmpty() || m_sendInFlight) {
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

        sendMedia(filePath, caption, replyToMessageId);
        return true;
    }

    if (mimeData->hasUrls()) {
        for (const QUrl &url : mimeData->urls()) {
            if (!url.isLocalFile()) {
                continue;
            }
            const QString filePath = url.toLocalFile();
            const QString suffix = QFileInfo(filePath).suffix().toLower();
            if (suffix == QLatin1String("png") || suffix == QLatin1String("jpg")
                || suffix == QLatin1String("jpeg") || suffix == QLatin1String("webp")) {
                sendMedia(filePath, caption, replyToMessageId);
                return true;
            }
            if (suffix == QLatin1String("gif")) {
                m_composerErrorText = i18nc("@info", "GIFs can't be sent yet — WhatsApp treats them as short videos");
                Q_EMIT composerChanged();
                return true;
            }
        }
    }

    return false;
}

void ProtocolController::sendPoll(const QString &question, const QStringList &options, bool multiSelect, const QString &replyToMessageId)
{
    const QString trimmed = question.trimmed();
    if (m_selectedChatId.isEmpty() || trimmed.isEmpty() || options.isEmpty() || m_sendInFlight || !selectedChatCanSend()) {
        return;
    }

    setSelectedChatComposing(false);
    dismissUnreadAnchor();
    Q_EMIT messageSent();

    QJsonArray opts;
    for (const QString &opt : options) {
        if (!opt.trimmed().isEmpty()) {
            opts.append(opt.trimmed());
        }
    }

    QJsonObject params{{QStringLiteral("chat_id"), m_selectedChatId},
                       {QStringLiteral("question"), trimmed},
                       {QStringLiteral("options"), opts},
                       {QStringLiteral("multi"), multiSelect}};
    if (const QString reply = replyToMessageId.trimmed(); !reply.isEmpty()) {
        params.insert(QStringLiteral("reply_to"), reply);
    }

    m_sendInFlight = true;
    m_composerErrorText.clear();
    Q_EMIT composerChanged();

    m_client->request(QStringLiteral("send.poll"), params, [this](const QJsonObject &, const ProtocolError &error) {
        m_sendInFlight = false;
        m_composerErrorText = error.isError()
            ? (error.message.isEmpty() ? i18nc("@info", "Unable to send poll") : error.message)
            : QString();
        Q_EMIT composerChanged();
    });
}

void ProtocolController::sendContact(const QString &name, const QString &phone, const QString &replyToMessageId)
{
    if (m_selectedChatId.isEmpty() || name.trimmed().isEmpty() || phone.trimmed().isEmpty() || m_sendInFlight || !selectedChatCanSend()) {
        return;
    }

    setSelectedChatComposing(false);
    dismissUnreadAnchor();
    Q_EMIT messageSent();

    QJsonObject params{{QStringLiteral("chat_id"), m_selectedChatId},
                       {QStringLiteral("name"), name.trimmed()},
                       {QStringLiteral("phone"), phone.trimmed()}};
    if (const QString reply = replyToMessageId.trimmed(); !reply.isEmpty()) {
        params.insert(QStringLiteral("reply_to"), reply);
    }

    m_sendInFlight = true;
    m_composerErrorText.clear();
    Q_EMIT composerChanged();

    m_client->request(QStringLiteral("send.contact"), params, [this](const QJsonObject &, const ProtocolError &error) {
        m_sendInFlight = false;
        m_composerErrorText = error.isError()
            ? (error.message.isEmpty() ? i18nc("@info", "Unable to send contact") : error.message)
            : QString();
        Q_EMIT composerChanged();
    });
}

void ProtocolController::sendLocation(double latitude, double longitude, const QString &name, const QString &address, const QString &replyToMessageId)
{
    if (m_selectedChatId.isEmpty() || (latitude == 0.0 && longitude == 0.0) || m_sendInFlight || !selectedChatCanSend()) {
        return;
    }

    setSelectedChatComposing(false);
    dismissUnreadAnchor();
    Q_EMIT messageSent();

    QJsonObject params{{QStringLiteral("chat_id"), m_selectedChatId},
                       {QStringLiteral("lat"), latitude},
                       {QStringLiteral("long"), longitude},
                       {QStringLiteral("name"), name},
                       {QStringLiteral("address"), address}};
    if (const QString reply = replyToMessageId.trimmed(); !reply.isEmpty()) {
        params.insert(QStringLiteral("reply_to"), reply);
    }

    m_sendInFlight = true;
    m_composerErrorText.clear();
    Q_EMIT composerChanged();

    m_client->request(QStringLiteral("send.location"), params, [this](const QJsonObject &, const ProtocolError &error) {
        m_sendInFlight = false;
        m_composerErrorText = error.isError()
            ? (error.message.isEmpty() ? i18nc("@info", "Unable to send location") : error.message)
            : QString();
        Q_EMIT composerChanged();
    });
}

void ProtocolController::setSelectedChatComposing(bool composing)
{
    if (m_selectedChatId.isEmpty()) {
        return;
    }
    if (!composing && m_localComposingChatId != m_selectedChatId) {
        return;
    }
    m_localComposingChatId = composing ? m_selectedChatId : QString();
    m_client->request(QStringLiteral("chat.typing"),
                      {{QStringLiteral("chat_id"), m_selectedChatId}, {QStringLiteral("composing"), composing}});
}

// --- message actions (D4b) --------------------------------------------------

void ProtocolController::sendMessageCommand(const QString &method, const QJsonObject &params, const QString &failureText)
{
    m_client->request(method, params, [this, failureText](const QJsonObject &, const ProtocolError &error) {
        if (!error.isError()) {
            return;
        }
        Q_EMIT messageActionFailed(error.message.isEmpty() ? failureText : error.message);
    });
}

void ProtocolController::sendReaction(const QString &messageId, const QString &emoji)
{
    if (messageId.isEmpty()) {
        return;
    }
    // Reacting means the user has seen the message, so the unread divider goes
    // (mirrors AppController::sendReaction).
    dismissUnreadAnchor();
    sendMessageCommand(QStringLiteral("message.react"),
                       {{QStringLiteral("message_id"), messageId}, {QStringLiteral("emoji"), emoji}},
                       i18nc("@info", "Unable to react to the message"));
}

void ProtocolController::editMessage(const QString &messageId, const QString &newText)
{
    const QString trimmed = newText.trimmed();
    if (messageId.isEmpty() || trimmed.isEmpty()) {
        return;
    }
    sendMessageCommand(QStringLiteral("message.edit"),
                       {{QStringLiteral("message_id"), messageId}, {QStringLiteral("text"), trimmed}},
                       i18nc("@info", "Unable to edit the message"));
}

void ProtocolController::revokeMessage(const QString &messageId)
{
    if (messageId.isEmpty()) {
        return;
    }
    sendMessageCommand(QStringLiteral("message.revoke"), {{QStringLiteral("message_id"), messageId}},
                       i18nc("@info", "Unable to delete the message for everyone"));
}

void ProtocolController::deleteMessageForMe(const QString &messageId)
{
    if (messageId.isEmpty()) {
        return;
    }
    sendMessageCommand(QStringLiteral("message.delete"), {{QStringLiteral("message_id"), messageId}},
                       i18nc("@info", "Unable to delete the message"));
}

void ProtocolController::setMessageStarred(const QString &messageId, bool starred)
{
    if (messageId.isEmpty()) {
        return;
    }
    sendMessageCommand(QStringLiteral("message.star"),
                       {{QStringLiteral("message_id"), messageId}, {QStringLiteral("starred"), starred}},
                       i18nc("@info", "Unable to star the message"));
}

void ProtocolController::pinMessage(const QString &messageId, int durationSecs)
{
    if (messageId.isEmpty() || durationSecs <= 0) {
        return;
    }
    sendMessageCommand(QStringLiteral("message.pin"),
                       {{QStringLiteral("message_id"), messageId},
                        {QStringLiteral("pinned"), true},
                        {QStringLiteral("duration_secs"), durationSecs}},
                       i18nc("@info", "Unable to pin the message"));
}

void ProtocolController::unpinMessage(const QString &messageId)
{
    if (messageId.isEmpty()) {
        return;
    }
    sendMessageCommand(QStringLiteral("message.pin"),
                       {{QStringLiteral("message_id"), messageId}, {QStringLiteral("pinned"), false}},
                       i18nc("@info", "Unable to unpin the message"));
}

// --- media download (D4c) ---------------------------------------------------

void ProtocolController::downloadMessageMedia(const QString &messageId)
{
    if (messageId.isEmpty()) {
        return;
    }
    // The command's own reply only reports that the daemon could not *start*
    // the download; anything that goes wrong during it lands on the message row
    // as `media.download_error`, which the bubble already renders, so this must
    // not also raise a transient error for the same failure.
    sendMessageCommand(QStringLiteral("media.download"),
                       {{QStringLiteral("message_id"), messageId}},
                       i18nc("@info", "Unable to download the attachment"));
}

void ProtocolController::cancelMessageMediaDownload(const QString &messageId)
{
    if (messageId.isEmpty()) {
        return;
    }
    // Fire and forget: the transfers view reports the row closing, and a
    // rejection just means the fetch had already finished on its own.
    m_client->request(QStringLiteral("media.cancel_download"),
                      {{QStringLiteral("message_id"), messageId}},
                      [](const QJsonObject &, const ProtocolError &) {});
}

void ProtocolController::streamMessageMedia(const QString &messageId)
{
    if (messageId.isEmpty()) {
        return;
    }
    m_client->request(QStringLiteral("media.stream"),
                      {{QStringLiteral("message_id"), messageId}},
                      [this, messageId](const QJsonObject &result, const ProtocolError &error) {
                          if (error.isError()) {
                              // Not every message can be streamed (no length, no
                              // hash, a CDN that ignores ranges). The bubble
                              // falls back to an ordinary download rather than
                              // showing an error for something the user can
                              // still watch a moment later.
                              Q_EMIT mediaStreamFailed(messageId, error.message);
                              return;
                          }
                          const QString url = result.value(QStringLiteral("url")).toString();
                          const QString streamId = result.value(QStringLiteral("stream_id")).toString();
                          if (url.isEmpty() || streamId.isEmpty()) {
                              Q_EMIT mediaStreamFailed(messageId, QString());
                              return;
                          }
                          m_mediaStreamMessages.insert(streamId, messageId);
                          Q_EMIT mediaStreamReady(messageId, streamId, QUrl(url));
                      });
}

void ProtocolController::markMessagePlayed(const QString &messageId)
{
    if (messageId.isEmpty()) {
        return;
    }
    // A played receipt is a courtesy to the sender; failing to send one is not
    // worth interrupting the listener over.
    m_client->request(QStringLiteral("message.mark_played"),
                      {{QStringLiteral("message_id"), messageId}},
                      [](const QJsonObject &, const ProtocolError &) {});
}

void ProtocolController::requestMessageFromPhone(const QString &messageId)
{
    if (messageId.isEmpty()) {
        return;
    }
    // The daemon answers by upserting the waiting row with the request on it,
    // so the button's effect arrives through the view like everything else. A
    // refusal is worth saying out loud: this is a button somebody pressed on
    // purpose, having already waited.
    sendMessageCommand(QStringLiteral("message.request_from_phone"),
                       {{QStringLiteral("message_id"), messageId}},
                       i18nc("@info:status", "Could not ask your phone for this message"));
}

void ProtocolController::votePoll(const QString &messageId, const QVariantList &optionIndexes)
{
    if (messageId.isEmpty()) {
        return;
    }

    QJsonArray ids;
    for (const QVariant &index : optionIndexes) {
        bool ok = false;
        const int value = index.toInt(&ok);
        if (ok && value >= 0) {
            ids.append(value);
        }
    }

    // The tap is answered here rather than one round trip later. The daemon
    // publishes its own version of the tally within a few milliseconds, but the
    // send behind it takes a couple of hundred, and holding the row still for
    // either of those makes the poll feel like it did not hear the tap.
    m_pendingPollVotes.insert(messageId, optionIndexes);
    Q_EMIT pollVotesChanged();

    m_client->request(QStringLiteral("poll.vote"),
                      {{QStringLiteral("message_id"), messageId}, {QStringLiteral("option_ids"), ids}},
                      [this, messageId](const QJsonObject &, const ProtocolError &error) {
                          // Whether it worked or not, the daemon's tally is now
                          // the truth: on success it already carries this vote,
                          // and on failure it has put the previous one back.
                          m_pendingPollVotes.remove(messageId);
                          Q_EMIT pollVotesChanged();
                          if (error.isError()) {
                              Q_EMIT messageActionFailed(error.message.isEmpty()
                                                             ? i18nc("@info", "Unable to vote in the poll")
                                                             : error.message);
                          }
                      });
}

QVariant ProtocolController::pendingPollSelection(const QString &messageId) const
{
    return m_pendingPollVotes.value(messageId);
}

void ProtocolController::respondToEvent(const QString &messageId, const QString &response, int extraGuests)
{
    if (messageId.trimmed().isEmpty() || response.trimmed().isEmpty()) {
        return;
    }
    if (extraGuests < 0) {
        extraGuests = 0;
    }

    // Answered here rather than one round trip later, for the reason the poll
    // rows are: the daemon's own echo lands in a few milliseconds, but the send
    // behind it takes a couple of hundred, and a chip that does not light on
    // the tap reads as the card not having heard it.
    m_pendingEventRSVPs.insert(messageId,
                               QVariantMap{{QStringLiteral("response"), response},
                                           {QStringLiteral("extra_guests"), extraGuests}});
    Q_EMIT eventRSVPsChanged();

    m_client->request(QStringLiteral("event.rsvp"),
                      {{QStringLiteral("message_id"), messageId},
                       {QStringLiteral("response"), response},
                       {QStringLiteral("extra_guests"), extraGuests}},
                      [this, messageId](const QJsonObject &, const ProtocolError &error) {
                          // Either way the daemon's copy is now the truth: on
                          // success it carries this answer, and on failure it
                          // has put the previous one back.
                          m_pendingEventRSVPs.remove(messageId);
                          Q_EMIT eventRSVPsChanged();
                          if (error.isError()) {
                              Q_EMIT messageActionFailed(error.message.isEmpty()
                                                             ? i18nc("@info", "Unable to answer the event")
                                                             : error.message);
                          }
                      });
}

namespace
{
/// Escapes one iCalendar text value: RFC 5545 §3.3.11 gives backslash,
/// semicolon and comma special meaning inside one, and a literal newline ends
/// the property outright.
QString icsText(const QString &value)
{
    QString out = value;
    out.replace(QLatin1Char('\\'), QStringLiteral("\\\\"));
    out.replace(QLatin1Char(';'), QStringLiteral("\\;"));
    out.replace(QLatin1Char(','), QStringLiteral("\\,"));
    out.replace(QStringLiteral("\r\n"), QStringLiteral("\\n"));
    out.replace(QLatin1Char('\n'), QStringLiteral("\\n"));
    out.replace(QLatin1Char('\r'), QStringLiteral("\\n"));
    return out;
}

/// Folds a content line to 75 octets, continuing with a leading space, as
/// §3.1 requires. Long descriptions are exactly the case where a calendar that
/// enforces this rejects the whole file.
QString icsFold(const QString &line)
{
    const QByteArray utf8 = line.toUtf8();
    if (utf8.size() <= 75) {
        return line;
    }
    QString folded;
    int consumed = 0;
    while (consumed < utf8.size()) {
        int take = qMin(consumed == 0 ? 75 : 74, utf8.size() - consumed);
        // Never split a UTF-8 sequence: if the next byte is a continuation, the
        // cut lands mid-character, so back off to the lead byte before it. The
        // bounds check is the whole point of the first condition: on the last
        // chunk there is no next byte to look at, and reading it anyway is an
        // out-of-bounds access that aborts the app rather than folding badly.
        while (consumed + take < utf8.size() && take > 1
               && (utf8.at(consumed + take) & 0xC0) == 0x80) {
            --take;
        }
        if (!folded.isEmpty()) {
            folded += QStringLiteral("\r\n ");
        }
        folded += QString::fromUtf8(utf8.mid(consumed, take));
        consumed += take;
    }
    return folded;
}

QString icsStamp(qint64 unixSeconds)
{
    return QDateTime::fromSecsSinceEpoch(unixSeconds, QTimeZone::UTC)
        .toString(QStringLiteral("yyyyMMdd'T'HHmmss'Z'"));
}
} // namespace

QString ProtocolController::eventCalendarEntry(const QString &messageId, const QVariantMap &event)
{
    const qint64 startsAt = event.value(QStringLiteral("starts_at")).toLongLong();
    if (startsAt <= 0) {
        return {};
    }

    QString name = event.value(QStringLiteral("name")).toString().simplified();
    if (name.isEmpty()) {
        name = i18nc("@title fallback name for an event with none", "Event");
    }

    // The venue as one line: the place's name and its address say different
    // things and a calendar has one field for both.
    const QVariantMap location = event.value(QStringLiteral("location")).toMap();
    QStringList placeParts;
    for (const auto &key : {QStringLiteral("name"), QStringLiteral("address")}) {
        const QString part = location.value(key).toString().simplified();
        if (!part.isEmpty() && !placeParts.contains(part)) {
            placeParts.append(part);
        }
    }

    QStringList lines{
        QStringLiteral("BEGIN:VCALENDAR"),
        QStringLiteral("VERSION:2.0"),
        QStringLiteral("PRODID:-//whatevr//whatkevr//EN"),
        QStringLiteral("CALSCALE:GREGORIAN"),
        QStringLiteral("BEGIN:VEVENT"),
        // The message id is already unique and stable, so re-importing the same
        // event updates the entry the user already has instead of adding a
        // second copy of it.
        QStringLiteral("UID:") + icsText(messageId) + QStringLiteral("@whatevr"),
        QStringLiteral("DTSTAMP:") + icsStamp(QDateTime::currentSecsSinceEpoch()),
        QStringLiteral("DTSTART:") + icsStamp(startsAt),
        QStringLiteral("SUMMARY:") + icsText(name),
    };

    const qint64 endsAt = event.value(QStringLiteral("ends_at")).toLongLong();
    if (endsAt > startsAt) {
        lines.append(QStringLiteral("DTEND:") + icsStamp(endsAt));
    }
    const QString description = event.value(QStringLiteral("description")).toString();
    if (!description.isEmpty()) {
        lines.append(QStringLiteral("DESCRIPTION:") + icsText(description));
    }
    if (!placeParts.isEmpty()) {
        lines.append(QStringLiteral("LOCATION:") + icsText(placeParts.join(QStringLiteral(", "))));
    }
    const QString joinLink = event.value(QStringLiteral("join_link")).toString();
    if (!joinLink.isEmpty()) {
        lines.append(QStringLiteral("URL:") + icsText(joinLink));
    }
    // A cancelled event is still worth importing: it updates the entry the user
    // already accepted rather than leaving it sitting in their week.
    if (event.value(QStringLiteral("canceled")).toBool()) {
        lines.append(QStringLiteral("STATUS:CANCELLED"));
    }
    lines.append(QStringLiteral("END:VEVENT"));
    lines.append(QStringLiteral("END:VCALENDAR"));

    QString body;
    for (const QString &line : std::as_const(lines)) {
        body += icsFold(line) + QStringLiteral("\r\n");
    }
    return body;
}

bool ProtocolController::saveEventToCalendar(const QString &messageId, const QVariantMap &event)
{
    const QString body = eventCalendarEntry(messageId, event);
    if (body.isEmpty()) {
        Q_EMIT messageActionFailed(i18nc("@info", "This event has no start time"));
        return false;
    }

    const QString directory =
        QStandardPaths::writableLocation(QStandardPaths::CacheLocation) + QStringLiteral("/events");
    if (!QDir().mkpath(directory)) {
        Q_EMIT messageActionFailed(i18nc("@info", "Unable to write the calendar entry"));
        return false;
    }

    // The filename comes from the message id rather than the event's name: the
    // name is attacker-controlled text arriving over the network and must never
    // be able to steer where this writes.
    QString base = messageId;
    base.replace(QRegularExpression(QStringLiteral("[^\\w.-]")), QStringLiteral("_"));
    base.truncate(96);
    if (base.isEmpty()) {
        base = QStringLiteral("event");
    }

    const QString path = directory + QLatin1Char('/') + base + QStringLiteral(".ics");
    QFile file(path);
    if (!file.open(QIODevice::WriteOnly | QIODevice::Truncate)
        || file.write(body.toUtf8()) < 0) {
        Q_EMIT messageActionFailed(i18nc("@info", "Unable to write the calendar entry"));
        return false;
    }
    file.close();

    if (!QDesktopServices::openUrl(QUrl::fromLocalFile(path))) {
        Q_EMIT messageActionFailed(i18nc("@info", "No application is set up to open calendar entries"));
        return false;
    }
    return true;
}

void ProtocolController::joinGroupInvite(const QString &messageId)
{
    if (messageId.trimmed().isEmpty()) {
        return;
    }
    m_client->request(QStringLiteral("group.join_invite"), {{QStringLiteral("message_id"), messageId}},
                      [this](const QJsonObject &result, const ProtocolError &error) {
                          if (error.isError()) {
                              Q_EMIT messageActionFailed(error.message.isEmpty()
                                                             ? i18nc("@info", "Unable to join the group")
                                                             : error.message);
                              return;
                          }
                          const QString chatId = result.value(QStringLiteral("chat_id")).toString();
                          if (chatId.isEmpty()) {
                              Q_EMIT messageActionFailed(i18nc("@info", "Unable to join the group"));
                              return;
                          }
                          // Joining and then leaving the reader in the chat they
                          // were already in is half the action. The row itself
                          // arrives through the `chats` view; this only selects
                          // it and drives the column navigation, exactly as
                          // opening a contact's chat does.
                          clearSearch();
                          selectChat(chatId);
                          Q_EMIT openChatRequested(chatId);
                      });
}

bool ProtocolController::openLocalFile(const QString &localPath)
{
    if (localPath.isEmpty() || !QFileInfo::exists(localPath)) {
        return false;
    }
    return QDesktopServices::openUrl(QUrl::fromLocalFile(localPath));
}

bool ProtocolController::openLogDirectory()
{
    const QString dir = QStandardPaths::writableLocation(QStandardPaths::GenericCacheLocation)
        + QStringLiteral("/whatevrd");
    if (!QFileInfo::exists(dir)) {
        return false;
    }
    return QDesktopServices::openUrl(QUrl::fromLocalFile(dir));
}

QUrl ProtocolController::localFileUrl(const QString &localPath) const
{
    return localPath.isEmpty() ? QUrl() : QUrl::fromLocalFile(localPath);
}

bool ProtocolController::openLocation(double latitude, double longitude, const QString &label)
{
    if (latitude == 0.0 && longitude == 0.0) {
        return false;
    }

    // RFC 5870 with the `q=` extension every map application understands: the
    // coordinates place the view, the query drops a labelled pin on it.
    const QString coordinates = QStringLiteral("%1,%2")
                                    .arg(latitude, 0, 'f', 6)
                                    .arg(longitude, 0, 'f', 6);
    QString geo = QStringLiteral("geo:") + coordinates;
    if (label.isEmpty()) {
        geo += QStringLiteral("?q=") + coordinates;
    } else {
        geo += QStringLiteral("?q=") + coordinates + QStringLiteral("(")
            + QString::fromUtf8(QUrl::toPercentEncoding(label)) + QStringLiteral(")");
    }
    if (QDesktopServices::openUrl(QUrl(geo))) {
        return true;
    }

    // Nothing registered for geo:. OpenStreetMap in a browser is the honest
    // fallback: it is the same data the daemon drew the map from.
    const QUrl web(QStringLiteral("https://www.openstreetmap.org/?mlat=%1&mlon=%2#map=17/%1/%2")
                       .arg(latitude, 0, 'f', 6)
                       .arg(longitude, 0, 'f', 6));
    return QDesktopServices::openUrl(web);
}

void ProtocolController::forwardMessage(const QString &messageId, const QStringList &chatIds)
{
    if (messageId.isEmpty() || chatIds.isEmpty()) {
        return;
    }

    // The picker forwards every selected message in one synchronous loop, so an
    // idle in-flight count marks the start of a batch.
    if (m_forwardInFlight == 0) {
        m_forwardBatchChatCount = static_cast<int>(chatIds.size());
        m_forwardBatchFailed = false;
    }
    ++m_forwardInFlight;

    m_client->request(QStringLiteral("message.forward"),
                      {{QStringLiteral("message_id"), messageId},
                       {QStringLiteral("chat_ids"), QJsonArray::fromStringList(chatIds)}},
                      [this](const QJsonObject &, const ProtocolError &error) {
        m_forwardInFlight = qMax(0, m_forwardInFlight - 1);
        if (error.isError()) {
            if (!m_forwardBatchFailed) {
                m_forwardBatchFailed = true;
                Q_EMIT messageActionFailed(error.message.isEmpty()
                                               ? i18nc("@info", "Unable to forward the message")
                                               : error.message);
            }
        }
        // Report success once, after the last message in the batch settles.
        if (m_forwardInFlight == 0 && !m_forwardBatchFailed) {
            Q_EMIT messageForwarded(m_forwardBatchChatCount);
        }
    });
}


bool ProtocolController::canEditAt(qint64 timestampUnix) const
{
    // Mirrors whatsmeow.EditWindow (20 minutes); the daemon is authoritative and
    // answers `expired` if this is optimistic.
    static constexpr qint64 kEditWindowSeconds = 20 * 60;
    if (timestampUnix <= 0) {
        return false;
    }
    return QDateTime::currentSecsSinceEpoch() - timestampUnix <= kEditWindowSeconds;
}

// --- pinned banner (D4b) ----------------------------------------------------

void ProtocolController::updatePinnedSubscription()
{
    const QString target = m_conversationVisible ? m_selectedChatId : QString();
    if (target == m_pinnedChatId) {
        return;
    }
    m_pinnedChatId = target;
    delete m_pinnedSub;
    m_pinnedSub = nullptr;
    m_pinnedModel->onReset();
    if (!target.isEmpty()) {
        m_pinnedSub = m_client->subscribe(QStringLiteral("pinned"),
                                          {{QStringLiteral("chat_id"), target}}, m_pinnedModel);
    }
    Q_EMIT pinnedMessagesChanged();
}

// --- live-location strip ----------------------------------------------------

void ProtocolController::updateLiveLocationsSubscription()
{
    const QString target = m_conversationVisible ? m_selectedChatId : QString();
    if (target == m_liveLocationsChatId) {
        return;
    }
    m_liveLocationsChatId = target;
    delete m_liveLocationsSub;
    m_liveLocationsSub = nullptr;
    m_liveLocationsModel->onReset();
    if (!target.isEmpty()) {
        m_liveLocationsSub = m_client->subscribe(QStringLiteral("live_locations"),
                                                 {{QStringLiteral("chat_id"), target}}, m_liveLocationsModel);
    }
    Q_EMIT liveLocationsChanged();
}

int ProtocolController::liveLocationsCount() const
{
    return m_liveLocationsModel->count();
}

QVariantMap ProtocolController::liveLocationAt(int index) const
{
    if (index < 0 || index >= m_liveLocationsModel->count()) {
        return {};
    }
    const QVariantMap item =
        m_liveLocationsModel->data(m_liveLocationsModel->index(index, 0), CollectionViewModel::ItemRole)
            .toMap();
    const QVariantMap sender = item.value(QStringLiteral("sender")).toMap();
    return {
        {QStringLiteral("messageId"), item.value(QStringLiteral("id")).toString()},
        {QStringLiteral("senderName"), sender.value(QStringLiteral("name")).toString()},
        {QStringLiteral("senderAvatarLocalPath"), sender.value(QStringLiteral("avatar_path")).toString()},
        {QStringLiteral("expiresAt"), item.value(QStringLiteral("expires_at")).toLongLong()},
        {QStringLiteral("updatedAt"), item.value(QStringLiteral("updated_at")).toLongLong()},
    };
}

bool ProtocolController::pinnedMessagesReady() const
{
    // Nothing subscribed means nothing to wait for, so the conversation can
    // collapse the banner slot instead of reserving space for it.
    return m_pinnedSub == nullptr || m_pinnedModel->isReady();
}

int ProtocolController::pinnedMessagesCount() const
{
    return m_pinnedModel->count();
}

QVariantMap ProtocolController::pinnedMessageAt(int index) const
{
    if (index < 0 || index >= m_pinnedModel->count()) {
        return {};
    }
    const QVariantMap item = m_pinnedModel
                                 ->data(m_pinnedModel->index(index, 0), CollectionViewModel::ItemRole)
                                 .toMap();
    return {
        {QStringLiteral("messageId"), item.value(QStringLiteral("id")).toString()},
        {QStringLiteral("senderName"), messageRowSenderName(item)},
        {QStringLiteral("preview"), whatevr::util::messageRowPreview(item)},
    };
}

// --- forward picker (D4b) ---------------------------------------------------

void ProtocolController::openForwardTargets()
{
    if (m_forwardTargetsSub) {
        return;
    }
    // Its own subscription rather than the sidebar's: the picker offers every
    // chat, not whatever the chat-list filter happens to be showing.
    m_forwardTargetsSub = m_client->subscribe(
        QStringLiteral("chats"),
        {{QStringLiteral("filter"), QStringLiteral("all")}, {QStringLiteral("archived"), false}},
        m_forwardTargetsModel);
}

void ProtocolController::closeForwardTargets()
{
    delete m_forwardTargetsSub;
    m_forwardTargetsSub = nullptr;
    m_forwardTargetsModel->onReset();
}

QVariantList ProtocolController::forwardChatTargets(const QString &query) const
{
    const QString needle = query.trimmed();
    QVariantList rows;
    rows.reserve(m_forwardTargetsModel->count());
    for (int row = 0; row < m_forwardTargetsModel->count(); ++row) {
        const QVariantMap item = m_forwardTargetsModel
                                     ->data(m_forwardTargetsModel->index(row, 0), CollectionViewModel::ItemRole)
                                     .toMap();
        if (!needle.isEmpty()
            && !item.value(QStringLiteral("name")).toString().contains(needle, Qt::CaseInsensitive)) {
            continue;
        }
        rows.append(item);
    }
    return rows;
}

// --- unified search (D5) ----------------------------------------------------

QAbstractItemModel *ProtocolController::searchResultsModel() const
{
    return m_searchResultsModel;
}

void ProtocolController::setSearchQuery(const QString &query)
{
    if (m_searchQuery == query) {
        return;
    }
    m_searchQuery = query;
    Q_EMIT searchChanged();

    if (query.trimmed().isEmpty()) {
        // Abandon whatever is in flight: bumping the generation makes every
        // pending reply a no-op, since the protocol client always answers.
        ++m_searchGeneration;
        m_searchPending = 0;
        m_searchDebounceTimer->stop();
        m_searchResultsModel->clear();
        if (m_searchBusy) {
            m_searchBusy = false;
            Q_EMIT searchChanged();
        }
        return;
    }
    m_searchDebounceTimer->start();
}

void ProtocolController::clearSearch()
{
    setSearchQuery(QString());
}

void ProtocolController::runSearch()
{
    const QString query = m_searchQuery.trimmed();
    if (query.isEmpty()) {
        m_searchResultsModel->clear();
        return;
    }

    const int generation = ++m_searchGeneration;
    m_searchPending = 2;
    m_searchBusy = true;
    Q_EMIT searchChanged();

    // Chat-name and message-text matches are two independent queries; each
    // keeps the daemon's own order in its own section (no merging).
    const auto halfLanded = [this, generation] {
        if (generation != m_searchGeneration) {
            return;
        }
        m_searchPending = qMax(0, m_searchPending - 1);
        if (m_searchPending == 0 && m_searchBusy) {
            m_searchBusy = false;
            Q_EMIT searchChanged();
        }
    };

    m_client->request(QStringLiteral("search.chats"), {{QStringLiteral("query"), query}},
                      [this, generation, halfLanded](const QJsonObject &result, const ProtocolError &error) {
        if (generation != m_searchGeneration) {
            return; // a newer query owns the model
        }
        if (!error.isError()) {
            m_searchResultsModel->setChats(result.value(QStringLiteral("chats")).toArray());
        }
        halfLanded();
    });

    m_client->request(QStringLiteral("search.messages"), {{QStringLiteral("query"), query}},
                      [this, generation, halfLanded](const QJsonObject &result, const ProtocolError &error) {
        if (generation != m_searchGeneration) {
            return;
        }
        if (!error.isError()) {
            m_searchResultsModel->setMessages(result.value(QStringLiteral("messages")).toArray());
        }
        halfLanded();
    });

    // The phone lookup is a fast secondary query and does not gate the spinner.
    if (!looksLikePhoneNumber(query)) {
        m_searchResultsModel->clearNumber();
        return;
    }
    m_client->request(QStringLiteral("contacts.check_phone"), {{QStringLiteral("phone"), query}},
                      [this, generation](const QJsonObject &result, const ProtocolError &error) {
        if (generation != m_searchGeneration) {
            return;
        }
        if (error.isError()) {
            m_searchResultsModel->clearNumber();
            return;
        }
        m_searchResultsModel->setNumber(result);
    });
}

// --- in-chat search (D5) ----------------------------------------------------

QString ProtocolController::chatSearchActiveMessageId() const
{
    if (m_chatSearchIndex < 0 || m_chatSearchIndex >= m_chatSearchMatchIds.size()) {
        return {};
    }
    return m_chatSearchMatchIds.at(m_chatSearchIndex);
}

void ProtocolController::openChatSearch()
{
    if (m_selectedChatId.isEmpty() || m_chatSearchActive) {
        return;
    }
    m_chatSearchActive = true;
    Q_EMIT chatSearchChanged();
}

void ProtocolController::closeChatSearch()
{
    if (!m_chatSearchActive && m_chatSearchQuery.isEmpty() && m_chatSearchMatchIds.isEmpty()) {
        return;
    }
    resetChatSearch();
    m_chatSearchActive = false;
    Q_EMIT chatSearchChanged();
}

void ProtocolController::resetChatSearch()
{
    ++m_chatSearchGeneration;
    m_chatSearchDebounceTimer->stop();
    m_chatSearchQuery.clear();
    m_chatSearchMatchIds.clear();
    m_chatSearchIndex = -1;
}

void ProtocolController::setChatSearchQuery(const QString &query)
{
    if (m_chatSearchQuery == query) {
        return;
    }
    m_chatSearchQuery = query;
    Q_EMIT chatSearchChanged();

    if (query.trimmed().isEmpty()) {
        ++m_chatSearchGeneration;
        m_chatSearchDebounceTimer->stop();
        m_chatSearchMatchIds.clear();
        m_chatSearchIndex = -1;
        Q_EMIT chatSearchChanged();
        return;
    }
    m_chatSearchDebounceTimer->start();
}

void ProtocolController::runChatSearch()
{
    const QString query = m_chatSearchQuery.trimmed();
    const QString chatId = m_selectedChatId;
    if (query.isEmpty() || chatId.isEmpty()) {
        m_chatSearchMatchIds.clear();
        m_chatSearchIndex = -1;
        Q_EMIT chatSearchChanged();
        return;
    }

    const int generation = ++m_chatSearchGeneration;
    m_client->request(QStringLiteral("search.messages"),
                      {{QStringLiteral("query"), query},
                       {QStringLiteral("chat_id"), chatId},
                       {QStringLiteral("limit"), kChatSearchLimit}},
                      [this, generation, chatId](const QJsonObject &result, const ProtocolError &error) {
        // Drop a reply to a superseded query, or one for a chat the user has
        // already left.
        if (generation != m_chatSearchGeneration || error.isError() || chatId != m_selectedChatId) {
            return;
        }
        m_chatSearchMatchIds.clear();
        const QJsonArray messages = result.value(QStringLiteral("messages")).toArray();
        for (const auto &value : messages) {
            m_chatSearchMatchIds.append(value.toObject().value(QStringLiteral("id")).toString());
        }
        m_chatSearchIndex = m_chatSearchMatchIds.isEmpty() ? -1 : 0;
        // The conversation scrolls to chatSearchActiveMessageId; the bubble
        // highlights off chatSearchQuery.
        Q_EMIT chatSearchChanged();
    });
}

void ProtocolController::chatSearchNext()
{
    if (m_chatSearchMatchIds.isEmpty()) {
        return;
    }
    m_chatSearchIndex = (m_chatSearchIndex + 1) % m_chatSearchMatchIds.size();
    Q_EMIT chatSearchChanged();
}

void ProtocolController::chatSearchPrevious()
{
    if (m_chatSearchMatchIds.isEmpty()) {
        return;
    }
    m_chatSearchIndex =
        (m_chatSearchIndex - 1 + m_chatSearchMatchIds.size()) % m_chatSearchMatchIds.size();
    Q_EMIT chatSearchChanged();
}

// --- starred page (D5) ------------------------------------------------------

QAbstractItemModel *ProtocolController::starredMessagesModel() const
{
    return m_starredModel;
}

bool ProtocolController::starredMessagesLoading() const
{
    return m_starredSub != nullptr && !m_starredModel->isReady();
}

bool ProtocolController::starredMessagesExhausted() const
{
    return m_starredSub == nullptr || m_starredModel->isExhausted();
}

void ProtocolController::openStarredMessages(const QString &chatId)
{
    delete m_starredSub;
    m_starredSub = nullptr;
    m_starredModel->onReset();
    m_starredChatId = chatId;

    QJsonObject params{{QStringLiteral("limit"), kStarredPageSize}};
    if (!chatId.isEmpty()) {
        params.insert(QStringLiteral("chat_id"), chatId);
    }
    m_starredSub = m_client->subscribe(QStringLiteral("starred"), params, m_starredModel);
    Q_EMIT starredMessagesChanged();
}

void ProtocolController::closeStarredMessages()
{
    if (!m_starredSub) {
        return;
    }
    delete m_starredSub;
    m_starredSub = nullptr;
    m_starredChatId.clear();
    m_starredModel->onReset();
    Q_EMIT starredMessagesChanged();
}

void ProtocolController::loadMoreStarredMessages()
{
    // A live-edge window only ever grows away from the edge (PROTOCOL.md
    // "Windows"), so the page's scroll-extend is always `older`.
    if (!m_starredSub || m_starredModel->isExhausted() || !m_starredModel->isReady()) {
        return;
    }
    m_starredSub->extend(kStarredPageSize, QStringLiteral("older"));
}

// --- status tab -------------------------------------------------------------

QAbstractItemModel *ProtocolController::statusModel() const
{
    return m_statusModel;
}

bool ProtocolController::statusLoading() const
{
    // The page groups over three views; a rebuild with any of them unready
    // flashes contacts in the wrong section before settling.
    return m_statusSub != nullptr
        && (!m_statusModel->isReady() || !m_keptStatusModel->isReady() || !m_mutedStatusModel->isReady());
}

bool ProtocolController::statusExhausted() const
{
    return m_statusSub == nullptr || m_statusModel->isExhausted();
}

void ProtocolController::openStatus()
{
    delete m_statusSub;
    m_statusSub = nullptr;
    m_statusModel->onReset();
    delete m_keptStatusSub;
    m_keptStatusSub = nullptr;
    m_keptStatusModel->onReset();
    delete m_mutedStatusSub;
    m_mutedStatusSub = nullptr;
    m_mutedStatusModel->onReset();

    QJsonObject params{{QStringLiteral("limit"), kStatusPageSize}};
    m_statusSub = m_client->subscribe(QStringLiteral("status"), params, m_statusModel);
    m_keptStatusSub = m_client->subscribe(QStringLiteral("status.kept"), {}, m_keptStatusModel);
    m_mutedStatusSub = m_client->subscribe(QStringLiteral("status.muted"), {}, m_mutedStatusModel);
    Q_EMIT statusChanged();
}

QAbstractItemModel *ProtocolController::keptStatusModel() const
{
    return m_keptStatusModel;
}

void ProtocolController::setStatusKeepSender(const QString &senderId, bool kept)
{
    if (senderId.isEmpty()) {
        return;
    }
    // Ack-then-lifecycle like markStatusViewed: the `status.kept` view (and
    // the `status` view) refresh off the StatusChanged event the command
    // publishes.
    sendMessageCommand(QStringLiteral("status.keep_sender"),
                       {{QStringLiteral("sender_id"), senderId}, {QStringLiteral("kept"), kept}},
                       i18nc("@info", "Unable to update the keep setting"));
}

void ProtocolController::requestEditHistory(const QString &messageId)
{
    if (messageId.isEmpty()) {
        return;
    }
    m_client->request(QStringLiteral("message.edit_history"), {{QStringLiteral("message_id"), messageId}},
                      [this, messageId](const QJsonObject &result, const ProtocolError &error) {
                          if (error.isError()) {
                              Q_EMIT messageActionFailed(error.message.isEmpty() ? i18nc("@info", "Unable to load edit history")
                                                                                 : error.message);
                              return;
                          }
                          Q_EMIT editHistoryReady(messageId, result.value(QStringLiteral("edits")).toArray().toVariantList());
                      });
}

QAbstractItemModel *ProtocolController::mutedStatusModel() const
{
    return m_mutedStatusModel;
}

void ProtocolController::setStatusMuteSender(const QString &senderId, bool muted)
{
    if (senderId.isEmpty()) {
        return;
    }
    // Ack-then-lifecycle like setStatusKeepSender: the `status.muted` view
    // (and the `status` view) refresh off the StatusChanged event the
    // command publishes.
    sendMessageCommand(QStringLiteral("status.mute_sender"),
                       {{QStringLiteral("sender_id"), senderId}, {QStringLiteral("muted"), muted}},
                       i18nc("@info", "Unable to update the mute setting"));
}

void ProtocolController::closeStatus()
{
    if (!m_statusSub && !m_keptStatusSub && !m_mutedStatusSub) {
        return;
    }
    delete m_statusSub;
    m_statusSub = nullptr;
    m_statusModel->onReset();
    delete m_keptStatusSub;
    m_keptStatusSub = nullptr;
    m_keptStatusModel->onReset();
    delete m_mutedStatusSub;
    m_mutedStatusSub = nullptr;
    m_mutedStatusModel->onReset();
    Q_EMIT statusChanged();
}

void ProtocolController::loadMoreStatus()
{
    // Live-edge window: only ever grows `older`.
    if (!m_statusSub || m_statusModel->isExhausted() || !m_statusModel->isReady()) {
        return;
    }
    m_statusSub->extend(kStatusPageSize, QStringLiteral("older"));
}

void ProtocolController::markStatusViewed(const QString &statusId)
{
    if (statusId.isEmpty()) {
        return;
    }
    sendMessageCommand(QStringLiteral("status.mark_viewed"), {{QStringLiteral("status_id"), statusId}},
                       i18nc("@info", "Unable to mark the status viewed"));
}

void ProtocolController::postStatusText(const QString &text, int background, int font)
{
    if (text.trimmed().isEmpty()) {
        return;
    }
    m_client->request(QStringLiteral("status.post"), {{QStringLiteral("text"), text},
                                                       {QStringLiteral("background"), background},
                                                       {QStringLiteral("font"), font}},
                      [this](const QJsonObject &, const ProtocolError &error) {
                          if (error.isError()) {
                              Q_EMIT messageActionFailed(error.message.isEmpty() ? i18nc("@info", "Unable to post the status")
                                                                                 : error.message);
                          }
                      });
}

void ProtocolController::postStatusMedia(const QString &fileUrl, const QString &caption, int background, int font)
{
    const QUrl url(fileUrl);
    const QString filePath = url.isLocalFile() ? url.toLocalFile() : fileUrl;
    if (filePath.isEmpty()) {
        return;
    }
    m_client->request(QStringLiteral("status.post"),
                      {{QStringLiteral("path"), filePath}, {QStringLiteral("caption"), caption.trimmed()},
                       {QStringLiteral("background"), background}, {QStringLiteral("font"), font}},
                      [this](const QJsonObject &, const ProtocolError &error) {
                          if (error.isError()) {
                              Q_EMIT messageActionFailed(error.message.isEmpty() ? i18nc("@info", "Unable to post the status")
                                                                                 : error.message);
                          }
                      });
}

void ProtocolController::replyToStatus(const QString &statusId, const QString &text)
{
    if (statusId.trimmed().isEmpty() || text.trimmed().isEmpty()) {
        return;
    }
    m_client->request(QStringLiteral("status.reply"),
                      {{QStringLiteral("status_id"), statusId}, {QStringLiteral("text"), text.trimmed()}},
                      [this](const QJsonObject &, const ProtocolError &error) {
                          if (error.isError()) {
                              Q_EMIT messageActionFailed(error.message.isEmpty()
                                                             ? i18nc("@info", "Unable to reply to the status")
                                                             : error.message);
                          }
                      });
}

void ProtocolController::downloadStatus(const QString &statusId)
{
    if (statusId.isEmpty()) {
        return;
    }
    // Ack-then-lifecycle like media.download: the row upserts with media.path,
    // failures surface only as a log line daemon-side, same as its twin.
    m_client->request(QStringLiteral("status.download"), {{QStringLiteral("status_id"), statusId}});
}

void ProtocolController::saveRemoteMedia(const QString &messageId, const QString &statusId, const QString &jid, const QUrl &destUrl){
    if (!destUrl.isLocalFile()) {
        return;
    }
    const QString destination = destUrl.toLocalFile();
    if (destination.isEmpty()) {
        return;
    }
    // The dialog already confirmed overwriting; QFile::copy refuses to.
    if (QFile::exists(destination) && !QFile::remove(destination)) {
        Q_EMIT messageActionFailed(i18nc("@info", "Unable to overwrite the existing file"));
        return;
    }
    QJsonObject params{{QStringLiteral("path"), destination}};
    if (!messageId.isEmpty()) {
        params.insert(QStringLiteral("message_id"), messageId);
    }
    if (!statusId.isEmpty()) {
        params.insert(QStringLiteral("status_id"), statusId);
    }
    if (!jid.isEmpty()) {
        params.insert(QStringLiteral("jid"), jid);
    }
    m_client->request(QStringLiteral("media.save"), params,
                      [this](const QJsonObject &result, const ProtocolError &error) {
                          if (error.isError()) {
                              Q_EMIT messageActionFailed(
                                  error.message.isEmpty() ? i18nc("@info", "Unable to save the file") : error.message);
                              return;
                          }
                          Q_EMIT remoteMediaSaved(result.value(QStringLiteral("path")).toString());
                      });
}

void ProtocolController::exportChat(const QString &chatId, const QUrl &destUrl)
{
    if (chatId.isEmpty() || !destUrl.isLocalFile()) {
        return;
    }
    const QString destination = destUrl.toLocalFile();
    if (destination.isEmpty()) {
        return;
    }
    // The dialog already confirmed overwriting; QFile::copy refuses to.
    if (QFile::exists(destination) && !QFile::remove(destination)) {
        Q_EMIT messageActionFailed(i18nc("@info", "Unable to overwrite the existing file"));
        return;
    }
    QJsonObject params{{QStringLiteral("chat_id"), chatId},
                       {QStringLiteral("path"), destination}};
    m_client->request(QStringLiteral("chat.export"), params,
                      [this](const QJsonObject &result, const ProtocolError &error) {
                          if (error.isError()) {
                              Q_EMIT messageActionFailed(
                                  error.message.isEmpty() ? i18nc("@info", "Unable to export the chat") : error.message);
                              return;
                          }
                          Q_EMIT chatExported(result.value(QStringLiteral("path")).toString());
                      });
}

void ProtocolController::exportBackup(const QUrl &destUrl, const QString &passphrase, bool useKeyring)
{
    if (!destUrl.isLocalFile()) {
        Q_EMIT messageActionFailed(i18nc("@info", "Choose a local backup file"));
        return;
    }
    const QString path = destUrl.toLocalFile();
    if (path.isEmpty()) {
        Q_EMIT messageActionFailed(i18nc("@info", "Choose a backup destination"));
        return;
    }
    m_client->request(QStringLiteral("daemon.backup_export"),
                      {{QStringLiteral("path"), path},
                       {QStringLiteral("passphrase"), passphrase},
                       {QStringLiteral("use_keyring"), useKeyring}},
                      [this](const QJsonObject &result, const ProtocolError &error) {
                          if (error.isError()) {
                              Q_EMIT messageActionFailed(error.message.isEmpty()
                                                             ? i18nc("@info", "Unable to export backup")
                                                             : error.message);
                              return;
                          }
                          Q_EMIT backupExported(result.value(QStringLiteral("path")).toString());
                      });
}

void ProtocolController::setBackupPassphrase(const QString &passphrase)
{
    if (passphrase.trimmed().isEmpty()) {
        Q_EMIT messageActionFailed(i18nc("@info", "Enter a backup passphrase"));
        return;
    }
    m_client->request(QStringLiteral("daemon.backup_set_passphrase"),
                      {{QStringLiteral("passphrase"), passphrase}},
                      [this](const QJsonObject &, const ProtocolError &error) {
                          if (error.isError()) {
                              Q_EMIT messageActionFailed(error.message.isEmpty()
                                                             ? i18nc("@info", "Unable to store backup passphrase")
                                                             : error.message);
                          }
                      });
}

// --- calls tab --------------------------------------------------------------

QAbstractItemModel *ProtocolController::callsModel() const
{
    return m_callsModel;
}

int ProtocolController::callsRingingCount() const
{
    return m_callsModel ? m_callsModel->count() : 0;
}

QAbstractItemModel *ProtocolController::logsModel() const
{
    return m_logsModel;
}

void ProtocolController::openCalls()
{
    delete m_callsSub;
    m_callsSub = nullptr;
    m_callsModel->onReset();

    m_callsSub = m_client->subscribe(QStringLiteral("calls"), {}, m_callsModel);
    Q_EMIT callsChanged();
}

void ProtocolController::closeCalls()
{
    if (!m_callsSub) {
        return;
    }
    delete m_callsSub;
    m_callsSub = nullptr;
    m_callsModel->onReset();
    Q_EMIT callsChanged();
}

void ProtocolController::openLogs()
{
    if (m_logsSub) {
        return;
    }
    m_logsLoading = true;
    m_logsErrorText.clear();
    Q_EMIT logsLoadingChanged();

    delete m_logsSub;
    m_logsSub = nullptr;
    m_logsModel->onReset();

    m_logsSub = m_client->subscribe(
        QStringLiteral("daemon.logs"),
        // Full ring for debug: the daemon caps at its 1000-line ring anyway.
        {{QStringLiteral("limit"), 1000}},
        m_logsModel);
    connect(m_logsSub, &Subscription::failed, this,
            [this](const QString &code, const QString &message) {
                m_logsLoading = false;
                m_logsErrorText = message.isEmpty()
                    ? i18nc("@info", "Could not load daemon logs (%1)", code)
                    : message;
                Q_EMIT logsLoadingChanged();
            });
    connect(m_logsModel, &CollectionViewModel::readyChanged, this, [this] {
        if (m_logsLoading && m_logsModel->isReady()) {
            m_logsLoading = false;
            Q_EMIT logsLoadingChanged();
        }
    });
}

void ProtocolController::closeLogs()
{
    if (!m_logsSub) {
        return;
    }
    delete m_logsSub;
    m_logsSub = nullptr;
    m_logsModel->onReset();
    if (m_logsLoading) {
        m_logsLoading = false;
        Q_EMIT logsLoadingChanged();
    }
}

void ProtocolController::rejectCall(const QString &chatId)
{
    if (chatId.isEmpty()) {
        return;
    }
    sendMessageCommand(QStringLiteral("call.reject"), {{QStringLiteral("chat_id"), chatId}},
                       i18nc("@info", "Unable to reject the call"));
}

// --- channels tab -----------------------------------------------------------

QAbstractItemModel *ProtocolController::channelsModel() const
{
    return m_channelsModel;
}

bool ProtocolController::channelsLoading() const
{
    return m_channelsSub != nullptr && !m_channelsModel->isReady();
}

QAbstractItemModel *ProtocolController::channelMessagesModel() const
{
    return m_channelMessagesModel;
}

bool ProtocolController::channelMessagesLoading() const
{
    return m_channelMessagesSub != nullptr && !m_channelMessagesModel->isReady();
}

QString ProtocolController::selectedChannelJid() const
{
    return m_selectedChannelJid;
}

QString ProtocolController::selectedChannelName() const
{
    return m_selectedChannelName;
}

void ProtocolController::openChannels()
{
    delete m_channelsSub;
    m_channelsSub = nullptr;
    m_channelsModel->onReset();

    m_channelsSub = m_client->subscribe(QStringLiteral("channels"), {}, m_channelsModel);
    // The directory is server state: refresh on every open so follows made on
    // the phone (or in another window) appear. The view refreshes off the
    // ChannelsChanged event the command publishes.
    m_client->request(QStringLiteral("channels.refresh"), {});
    Q_EMIT channelsChanged();
}

void ProtocolController::closeChannels()
{
    if (!m_channelsSub) {
        return;
    }
    delete m_channelsSub;
    m_channelsSub = nullptr;
    m_channelsModel->onReset();
    Q_EMIT channelsChanged();
}

void ProtocolController::openChannelMessages(const QString &jid, const QString &name)
{
    closeChannelMessages();
    m_selectedChannelJid = jid;
    m_selectedChannelName = name;
    Q_EMIT channelMessagesChanged();

    m_channelMessagesSub = m_client->subscribe(
        QStringLiteral("channel_messages"),
        {{QStringLiteral("channel"), jid}},
        m_channelMessagesModel);
}

void ProtocolController::closeChannelMessages()
{
    if (!m_channelMessagesSub) {
        return;
    }
    delete m_channelMessagesSub;
    m_channelMessagesSub = nullptr;
    m_channelMessagesModel->onReset();
    m_selectedChannelJid.clear();
    m_selectedChannelName.clear();
    Q_EMIT channelMessagesChanged();
}

void ProtocolController::followChannel(const QString &jidOrLink)
{
    if (jidOrLink.trimmed().isEmpty()) {
        return;
    }
    QJsonObject params;
    if (jidOrLink.contains(QLatin1Char('/')) || jidOrLink.startsWith(QStringLiteral("http")))
        params[QStringLiteral("invite")] = jidOrLink;
    else
        params[QStringLiteral("jid")] = jidOrLink;
    m_client->request(QStringLiteral("channel.follow"), params);
}

void ProtocolController::unfollowChannel(const QString &jid)
{
    if (jid.isEmpty()) {
        return;
    }
    m_client->request(QStringLiteral("channel.unfollow"), {{QStringLiteral("jid"), jid}});
}

void ProtocolController::muteChannel(const QString &jid, bool muted)
{
    if (jid.isEmpty()) {
        return;
    }
    m_client->request(QStringLiteral("channel.mute"),
                      {{QStringLiteral("jid"), jid}, {QStringLiteral("muted"), muted}});
}

void ProtocolController::reactToChannelMessage(const QString &channelId, qint64 serverId, const QString &emoji)
{
    if (channelId.trimmed().isEmpty() || serverId <= 0) {
        return;
    }
    m_client->request(QStringLiteral("channel.react"),
                      {{QStringLiteral("channel_id"), channelId},
                       {QStringLiteral("server_id"), serverId},
                       {QStringLiteral("emoji"), emoji}},
                      [this](const QJsonObject &, const ProtocolError &error) {
                          if (error.isError()) {
                              Q_EMIT messageActionFailed(error.message.isEmpty()
                                                             ? i18nc("@info", "Unable to react to channel post")
                                                             : error.message);
                          }
                      });
}

void ProtocolController::markChannelViewed(const QString &channelId, const QVariantList &serverIds)
{
    if (channelId.trimmed().isEmpty() || serverIds.isEmpty()) {
        return;
    }
    QJsonArray ids;
    for (const QVariant &value : serverIds) {
        const qint64 id = value.toLongLong();
        if (id > 0) {
            ids.append(id);
        }
    }
    if (ids.isEmpty()) {
        return;
    }
    m_client->request(QStringLiteral("channel.mark_viewed"),
                      {{QStringLiteral("channel_id"), channelId},
                       {QStringLiteral("server_ids"), ids}},
                      [this](const QJsonObject &, const ProtocolError &error) {
                          if (error.isError()) {
                              Q_EMIT messageActionFailed(error.message.isEmpty()
                                                             ? i18nc("@info", "Unable to mark channel posts viewed")
                                                             : error.message);
                          }
                      });
}

void ProtocolController::leaveGroup(const QString &chatId)
{
    if (chatId.isEmpty()) {
        return;
    }
    sendMessageCommand(QStringLiteral("group.leave"), {{QStringLiteral("chat_id"), chatId}},
                       i18nc("@info", "Unable to leave the group"));
}

void ProtocolController::copyGroupInviteLink(const QString &chatId)
{
    if (chatId.isEmpty()) {
        return;
    }
    m_client->request(QStringLiteral("group.invite_link"), {{QStringLiteral("chat_id"), chatId}},
                      [this](const QJsonObject &result, const ProtocolError &error) {
                          if (error.isError()) {
                              Q_EMIT messageActionFailed(
                                  error.message.isEmpty() ? i18nc("@info", "Unable to get the invite link") : error.message);
                              return;
                          }
                          copyToClipboard(result.value(QStringLiteral("link")).toString());
                      });
}

// --- per-chat media gallery -------------------------------------------------

QAbstractItemModel *ProtocolController::chatMediaModel() const
{
    return m_chatMediaModel;
}

bool ProtocolController::chatMediaLoading() const
{
    return m_chatMediaSub != nullptr && !m_chatMediaModel->isReady();
}

bool ProtocolController::chatMediaExhausted() const
{
    return m_chatMediaSub == nullptr || m_chatMediaModel->isExhausted();
}

void ProtocolController::openChatMedia(const QString &chatId, const QString &kind)
{
    delete m_chatMediaSub;
    m_chatMediaSub = nullptr;
    m_chatMediaModel->onReset();
    if (chatId.isEmpty()) {
        Q_EMIT chatMediaChanged();
        return;
    }

    QJsonObject params{
        {QStringLiteral("chat_id"), chatId},
        {QStringLiteral("limit"), kChatMediaPageSize}};
    if (!kind.isEmpty())
        params[QStringLiteral("kinds")] = QJsonArray{kind};

    m_chatMediaSub = m_client->subscribe(QStringLiteral("chat_media"), params, m_chatMediaModel);
    Q_EMIT chatMediaChanged();
}

void ProtocolController::closeChatMedia()
{
    if (!m_chatMediaSub) {
        return;
    }
    delete m_chatMediaSub;
    m_chatMediaSub = nullptr;
    m_chatMediaModel->onReset();
    Q_EMIT chatMediaChanged();
}

void ProtocolController::extendChatMedia(int count)
{
    // A newest-first live-edge window only grows into older rows.
    if (!m_chatMediaSub || m_chatMediaModel->isExhausted() || !m_chatMediaModel->isReady()) {
        return;
    }
    m_chatMediaSub->extend(count > 0 ? count : kChatMediaPageSize, QStringLiteral("older"));
}

QVariantMap ProtocolController::messageRowDisplay(const QVariantMap &item) const
{
    if (item.isEmpty()) {
        return {};
    }
    const qint64 timestamp = item.value(QStringLiteral("timestamp")).toLongLong();
    return {
        {QStringLiteral("messageId"), item.value(QStringLiteral("id")).toString()},
        {QStringLiteral("chatId"), item.value(QStringLiteral("chat_id")).toString()},
        {QStringLiteral("chatName"), item.value(QStringLiteral("chat_name")).toString()},
        {QStringLiteral("senderName"), messageRowSenderName(item)},
        {QStringLiteral("preview"), whatevr::util::messageRowPreview(item)},
        {QStringLiteral("timeText"), timestamp > 0
             ? QLocale().toString(QDateTime::fromSecsSinceEpoch(timestamp), QLocale::ShortFormat)
             : QString()},
        {QStringLiteral("isOutgoing"),
         item.value(QStringLiteral("direction")).toString() == QLatin1String("outgoing")},
    };
}

// --- contact / group info card (D5) -----------------------------------------

QVariantMap ProtocolController::infoCard() const
{
    return m_infoCardModel->value();
}

bool ProtocolController::infoCardLoading() const
{
    return !m_infoCardKind.isEmpty() && m_infoCardError.isEmpty() && !m_infoCardModel->isPresent();
}

bool ProtocolController::infoCardBlocked() const
{
    if (m_infoCardKind != QLatin1String("contact")) {
        return false;
    }
    // The blocklist is keyed by jid; a contact card whose subject is in the view
    // is blocked. Membership, not a copied flag.
    return !m_blocklistModel->itemById(m_infoCardSubject).isEmpty();
}

int ProtocolController::groupMemberCount() const
{
    return m_groupMembersModel->count();
}

QVariantList ProtocolController::groupMembers(const QString &query) const
{
    return filterMemberRows(m_groupMembersModel, query);
}

QVariantList ProtocolController::chatMembers(const QString &query) const
{
    return filterMemberRows(m_chatMembersModel, query);
}

QVariantList ProtocolController::filterMemberRows(const CollectionViewModel *model, const QString &query)
{
    const QString needle = query.trimmed();
    QVariantList rows;
    rows.reserve(model->count());
    for (int row = 0; row < model->count(); ++row) {
        const QVariantMap item =
            model->data(model->index(row, 0), CollectionViewModel::ItemRole).toMap();
        if (!needle.isEmpty()
            && !item.value(QStringLiteral("display_name")).toString().contains(needle, Qt::CaseInsensitive)
            && !item.value(QStringLiteral("phone")).toString().contains(needle, Qt::CaseInsensitive)) {
            continue;
        }
        rows.append(item);
    }
    return rows;
}

void ProtocolController::updateChatMembersSubscription()
{
    // Only a group conversation has a roster, and only a visible one needs it.
    const bool eligible = m_conversationVisible && m_selectedChatId.endsWith(QLatin1String("@g.us"));
    const QString target = eligible ? m_selectedChatId : QString();
    const bool retarget = target != m_chatMembersChatId;
    if (retarget) {
        // A different conversation: whatever the last one asked for does not
        // carry over, and its rows must not linger behind the new chat.
        m_chatMembersChatId = target;
        m_chatMembersWanted = false;
        delete m_chatMembersSub;
        m_chatMembersSub = nullptr;
        m_chatMembersModel->onReset();
    }
    // Deliberately not subscribed on open. Resolving a group's roster is by far
    // the most expensive thing the daemon does for a conversation — a large
    // group costs hundreds of milliseconds and ships a row per member — and the
    // only consumer is the mention picker, which is not on screen yet.
    // ensureChatMembers() turns it on the moment an `@` token opens.
    // The model's own reset/count/data signals already bump the revision, so a
    // retarget has published itself by here and arriving rows will publish
    // themselves as they land.
    if (m_chatMembersWanted && !target.isEmpty() && !m_chatMembersSub) {
        m_chatMembersSub = m_client->subscribe(QStringLiteral("group_members"),
                                               {{QStringLiteral("chat_id"), target}}, m_chatMembersModel);
    }
}

void ProtocolController::ensureChatMembers()
{
    if (m_chatMembersWanted) {
        return;
    }
    m_chatMembersWanted = true;
    updateChatMembersSubscription();
}

void ProtocolController::openContactCard(const QString &jid)
{
    closeInfoCard();
    if (jid.trimmed().isEmpty()) {
        return;
    }
    m_infoCardKind = QStringLiteral("contact");
    m_infoCardSubject = jid;
    m_infoCardSub = m_client->subscribe(QStringLiteral("contact"),
                                        {{QStringLiteral("jid"), jid}}, m_infoCardModel);
    connect(m_infoCardSub, &Subscription::failed, this,
            [this, jid](const QString &code, const QString &message) {
                if (m_infoCardSubject != jid) {
                    return; // a later card owns the view now
                }
                if (code == QLatin1String("io") && m_infoCardSub) {
                    return; // live subscriptions auto-resubscribe after reconnect
                }
                m_infoCardError = message.isEmpty()
                    ? i18nc("@info", "Unable to load contact info")
                    : message;
                Q_EMIT infoCardChanged();
            });
    // Block state is shared with the settings page; one subscription stays live
    // while either consumer is visible.
    updateBlocklistSubscription();
    Q_EMIT infoCardChanged();
    Q_EMIT groupMembersChanged();
}

void ProtocolController::openGroupCard(const QString &chatId)
{
    closeInfoCard();
    if (chatId.trimmed().isEmpty()) {
        return;
    }
    m_infoCardKind = QStringLiteral("group");
    m_infoCardSubject = chatId;
    // Two views, one dialog: PROTOCOL.md splits the group card from its roster
    // so a member join is one upsert instead of a whole card rewrite.
    m_infoCardSub = m_client->subscribe(QStringLiteral("group"),
                                        {{QStringLiteral("chat_id"), chatId}}, m_infoCardModel);
    connect(m_infoCardSub, &Subscription::failed, this,
            [this, chatId](const QString &code, const QString &message) {
                if (m_infoCardSubject != chatId) {
                    return;
                }
                if (code == QLatin1String("io") && m_infoCardSub) {
                    return;
                }
                m_infoCardError = message.isEmpty()
                    ? i18nc("@info", "Unable to load group info")
                    : message;
                Q_EMIT infoCardChanged();
            });
    m_groupMembersSub = m_client->subscribe(QStringLiteral("group_members"),
                                            {{QStringLiteral("chat_id"), chatId}}, m_groupMembersModel);
    Q_EMIT infoCardChanged();
    Q_EMIT groupMembersChanged();
}

void ProtocolController::closeInfoCard()
{
    if (m_infoCardKind.isEmpty() && !m_infoCardSub) {
        return;
    }
    delete m_infoCardSub;
    delete m_groupMembersSub;
    m_infoCardSub = nullptr;
    m_groupMembersSub = nullptr;
    m_infoCardKind.clear();
    m_infoCardSubject.clear();
    m_infoCardError.clear();
    m_infoCardModel->onReset();
    m_groupMembersModel->onReset();
    updateBlocklistSubscription();
    Q_EMIT infoCardChanged();
    Q_EMIT groupMembersChanged();
}

void ProtocolController::setContactBlocked(const QString &jid, bool blocked)
{
    if (jid.trimmed().isEmpty()) {
        return;
    }
    // Ack only: the new block state arrives back through the `blocklist` view.
    m_client->request(QStringLiteral("contact.block"),
                      {{QStringLiteral("jid"), jid}, {QStringLiteral("blocked"), blocked}},
                      [this, jid](const QJsonObject &, const ProtocolError &error) {
        if (!error.isError()) {
            return;
        }
        const QString message = error.message.isEmpty() ? i18nc("@info", "Updating the blocklist failed")
                                                        : error.message;
        Q_EMIT settingsActionFailed(message);
        if (m_infoCardSubject == jid) {
            m_infoCardError = message;
            Q_EMIT infoCardChanged();
        }
    });
}

void ProtocolController::viewProfilePicture(const QString &jid)
{
    if (jid.trimmed().isEmpty()) {
        return;
    }
    m_client->request(QStringLiteral("media.fetch_profile_picture"), {{QStringLiteral("jid"), jid}},
                      [this, jid](const QJsonObject &result, const ProtocolError &error) {
        if (error.isError()) {
            Q_EMIT profilePictureFailed(jid, error.message.isEmpty()
                                                 ? i18nc("@info", "Unable to load profile picture")
                                                 : error.message);
            return;
        }
        const QString path = result.value(QStringLiteral("path")).toString();
        if (path.isEmpty()) {
            Q_EMIT profilePictureFailed(jid, i18nc("@info", "No profile picture available"));
            return;
        }
        Q_EMIT profilePictureReady(jid, path);
    });
}

void ProtocolController::startDirectChat(const QString &jid)
{
    if (jid.trimmed().isEmpty()) {
        return;
    }
    m_client->request(QStringLiteral("chat.ensure_direct"), {{QStringLiteral("jid"), jid}},
                      [this](const QJsonObject &result, const ProtocolError &error) {
        if (error.isError()) {
            Q_EMIT messageActionFailed(error.message.isEmpty() ? i18nc("@info", "Unable to start chat")
                                                               : error.message);
            return;
        }
        const QString chatId = result.value(QStringLiteral("chat_id")).toString();
        if (chatId.isEmpty()) {
            Q_EMIT messageActionFailed(i18nc("@info", "Unable to start chat"));
            return;
        }
        // The row itself arrives through the `chats` view; all this does is
        // select it and drive column navigation the way a deep link does.
        clearSearch();
        selectChat(chatId);
        Q_EMIT openChatRequested(chatId);
    });
}

// --- settings / profile / emoji / stickers (D6) ---------------------------

QVariantMap ProtocolController::privacySettings() const
{
    return m_privacyModel->value();
}

QVariantMap ProtocolController::appPreferences() const
{
    return m_preferencesModel->value();
}

QAbstractItemModel *ProtocolController::blockedContactsModel() const
{
    return m_blocklistModel;
}

QVariantMap ProtocolController::selfProfile() const
{
    return m_selfModel->value();
}

QString ProtocolController::currentUserName() const
{
    const QVariantMap profile = selfProfile();
    const QString name = profile.value(QStringLiteral("push_name")).toString().trimmed();
    if (!name.isEmpty()) {
        return name;
    }
    const QString phone = profile.value(QStringLiteral("phone")).toString();
    return phone.isEmpty() ? profile.value(QStringLiteral("jid")).toString().section(QLatin1Char('@'), 0, 0)
                           : phone;
}

QString ProtocolController::currentUserAvatarPath() const
{
    return selfProfile().value(QStringLiteral("avatar_path")).toString();
}

QString ProtocolController::currentUserStatusText() const
{
    return selfProfile().value(QStringLiteral("about")).toString();
}

QString ProtocolController::currentUserJid() const
{
    return selfProfile().value(QStringLiteral("jid")).toString();
}

QAbstractItemModel *ProtocolController::emojiModel() const
{
    if (!m_emojiModel) {
        m_emojiModel = new EmojiModel(const_cast<ProtocolController *>(this));
    }
    return m_emojiModel;
}

QObject *ProtocolController::stickers() const
{
    return m_stickerController;
}

void ProtocolController::openPrivacySettings()
{
    if (m_privacyPageOpen) {
        return;
    }
    m_privacyPageOpen = true;
    m_privacySub = m_client->subscribe(QStringLiteral("privacy"), {}, m_privacyModel);
    connect(m_privacySub, &Subscription::failed, this, [this](const QString &code, const QString &message) {
        if (code == QLatin1String("io") && m_privacySub) {
            return;
        }
        Q_EMIT settingsActionFailed(message.isEmpty() ? i18nc("@info", "Unable to load privacy settings")
                                                       : message);
    });
}

void ProtocolController::closePrivacySettings()
{
    if (!m_privacyPageOpen) {
        return;
    }
    m_privacyPageOpen = false;
    delete m_privacySub;
    m_privacySub = nullptr;
    m_privacyModel->onReset();
}

void ProtocolController::openBlockedContacts()
{
    m_blocklistPageOpen = true;
    updateBlocklistSubscription();
}

void ProtocolController::closeBlockedContacts()
{
    m_blocklistPageOpen = false;
    updateBlocklistSubscription();
}

void ProtocolController::updateBlocklistSubscription()
{
    const bool wanted = m_blocklistPageOpen || m_infoCardKind == QLatin1String("contact");
    if (wanted == (m_blocklistSub != nullptr)) {
        return;
    }
    delete m_blocklistSub;
    m_blocklistSub = nullptr;
    m_blocklistModel->onReset();
    if (wanted) {
        m_blocklistSub = m_client->subscribe(QStringLiteral("blocklist"), {}, m_blocklistModel);
    }
    Q_EMIT blocklistChanged();
}

void ProtocolController::sendSettingsCommand(const QString &method, const QJsonObject &params,
                                             const QString &failureText)
{
    m_client->request(method, params, [this, failureText](const QJsonObject &, const ProtocolError &error) {
        if (error.isError()) {
            Q_EMIT settingsActionFailed(error.message.isEmpty() ? failureText : error.message);
        }
    });
}

void ProtocolController::setPrivacyAudience(const QString &category, const QString &value)
{
    if (category.isEmpty() || value.isEmpty()) {
        return;
    }
    sendSettingsCommand(QStringLiteral("privacy.set"),
                        {{QStringLiteral("category"), category}, {QStringLiteral("value"), value}},
                        i18nc("@info", "Updating privacy settings failed"));
}

void ProtocolController::setReadReceipts(bool enabled)
{
    sendSettingsCommand(QStringLiteral("privacy.set"),
                        {{QStringLiteral("category"), QStringLiteral("read_receipts")},
                         {QStringLiteral("value"), enabled}},
                        i18nc("@info", "Updating privacy settings failed"));
}

void ProtocolController::setAppPreference(const QString &key, bool value)
{
    static const QSet<QString> keys{
        QStringLiteral("notifications_enabled"), QStringLiteral("notification_sound"),
        QStringLiteral("notification_preview"), QStringLiteral("auto_download_photos"),
        QStringLiteral("auto_download_videos"), QStringLiteral("auto_download_audio"),
        QStringLiteral("auto_download_documents"), QStringLiteral("auto_download_stickers"),
        QStringLiteral("anti_delete"), QStringLiteral("send_typing_indicators")};
    if (!keys.contains(key)) {
        return;
    }
    sendSettingsCommand(QStringLiteral("preferences.set"), {{key, value}},
                        i18nc("@info", "Updating preferences failed"));
}

void ProtocolController::setAutoDownloadLimit(qint64 maxBytes)
{
    sendSettingsCommand(QStringLiteral("preferences.set"),
                        {{QStringLiteral("auto_download_max_bytes"), qMax(qint64(0), maxBytes)}},
                        i18nc("@info", "Updating preferences failed"));
}

void ProtocolController::setProfileStatus(const QString &text)
{
    sendSettingsCommand(QStringLiteral("self.set_about"), {{QStringLiteral("text"), text}},
                        i18nc("@info", "Changing the profile failed"));
}

void ProtocolController::logout()
{
    sendSettingsCommand(QStringLiteral("account.logout"), {}, i18nc("@info", "Logout failed"));
}

void ProtocolController::shutdownDaemon()
{
    // Fire-and-forget: the daemon acks, then exits on its own timer. The
    // frontend quits regardless so a dead/absent daemon never blocks exit.
    if (!m_client) {
        return;
    }
    m_client->request(QStringLiteral("daemon.shutdown"), {}, [](const QJsonObject &, const ProtocolError &) {});
}

// --- history-sync strip (D2b2) --------------------------------------------

void ProtocolController::recomputeHistorySync()
{
    const QVariantMap item = m_syncModel->value();
    const QString type = item.value(QStringLiteral("type")).toString();
    const QString phase = item.value(QStringLiteral("phase")).toString();
    const bool isComplete = item.value(QStringLiteral("is_complete")).toBool();
    const int percent = qBound(0, item.value(QStringLiteral("progress_percent")).toInt(), 100);

    // Hidden when there is no active sync (absent/complete) or for on-demand
    // (per-chat) history, which the conversation view surfaces on its own. This
    // is a simpler policy than AppController's cross-event cursor: the `sync`
    // object view already delivers a single current state, so the strip renders
    // it directly (see the D2b2 note on the dropped type-dedup).
    const bool visible = m_syncModel->isPresent() && !isComplete && !type.isEmpty()
        && type != QLatin1String("on_demand");

    const bool wasVisible = m_historySyncVisible;
    QString title;
    QString detail;
    int shownPercent = 0;
    if (visible) {
        title = syncTypeLabel(type);
        // Never let the bar jump backwards within one visible session (a new
        // chunk restarts low); take the max, seed from the incoming value when
        // the strip first appears.
        shownPercent = wasVisible ? qMax(m_historySyncPercent, percent) : percent;

        const auto count = [&item](const char *key) {
            return item.value(QLatin1String(key)).toInt();
        };
        const int msgs = count("processed_messages");
        const int msgsIn = count("messages_in_chunk");
        const int convs = count("processed_conversations");
        const int convsIn = count("conversations_in_chunk");
        const int chunk = count("chunk_order");

        if (phase == QLatin1String("stalled")) {
            detail = i18nc("@info", "Sync paused — open WhatsApp on your phone to continue");
        } else if (type == QLatin1String("offline_catchup")) {
            const QString messagesText = msgsIn > 0
                ? i18nc("@info", "%1/%2 messages", msgs, msgsIn)
                : i18ncp("@info", "%1 message", "%1 messages", msgs);
            const QString eventsText = convsIn > 0
                ? i18nc("@info", "%1/%2 events", convs, convsIn)
                : i18ncp("@info", "%1 event", "%1 events", convs);
            detail = i18nc("@info", "%1 · %2", messagesText, eventsText);
        } else {
            const QString chunkText = chunk > 0 ? i18nc("@info", "Chunk %1", chunk)
                                                : i18nc("@info", "Processing chunk");
            if (phase == QLatin1String("queued")) {
                detail = i18nc("@info", "%1 · Queued", chunkText);
            } else if (phase == QLatin1String("downloading")) {
                detail = i18nc("@info", "%1 · Downloading", chunkText);
            } else {
                QStringList details;
                details << chunkText;
                if (convsIn > 0) {
                    details << i18nc("@info", "%1/%2 conversations", convs, convsIn);
                }
                if (msgsIn > 0) {
                    details << i18nc("@info", "%1/%2 messages", msgs, msgsIn);
                }
                if (details.size() == 1) {
                    details << i18nc("@info", "Processing");
                }
                detail = details.join(i18nc("@info list separator", " · "));
            }
        }
    }

    if (visible == m_historySyncVisible && shownPercent == m_historySyncPercent
        && title == m_historySyncTitle && detail == m_historySyncDetail) {
        return;
    }
    m_historySyncVisible = visible;
    m_historySyncPercent = shownPercent;
    m_historySyncTitle = title;
    m_historySyncDetail = detail;
    Q_EMIT historySyncChanged();
}

// --- transport phase ------------------------------------------------------

ProtocolController::Phase ProtocolController::phase() const
{
    if (m_clientReady) {
        return Phase::Connected;
    }
    if (m_startupGrace) {
        return Phase::Connecting;
    }
    // Socket present but hello not yet done: the daemon is up, keep trying.
    if (daemonSocketExists()) {
        return Phase::Connecting;
    }
    return Phase::NotRunning;
}

QString ProtocolController::daemonState() const
{
    return m_connectionModel->value().value(QStringLiteral("state")).toString();
}

bool ProtocolController::canReconnect() const
{
    return m_connectionModel->value().value(QStringLiteral("can_reconnect")).toBool();
}

// --- routing gate ---------------------------------------------------------

bool ProtocolController::starting() const
{
    // The initial window between launch and the first connection-view item,
    // routed to a neutral splash so a sub-second connect never flashes the
    // daemon-status page.
    return m_startupGrace && !m_connectionModel->isPresent();
}

bool ProtocolController::loginRequired() const
{
    return daemonState() == QLatin1String("need_login");
}

bool ProtocolController::shellVisible() const
{
    return phase() == Phase::Connected && !loginRequired() && m_connectionModel->isPresent();
}

// --- status page ----------------------------------------------------------

QString ProtocolController::connectionPhase() const
{
    switch (phase()) {
    case Phase::Connecting:
        return QStringLiteral("connecting");
    case Phase::Connected:
        return QStringLiteral("connected");
    case Phase::NotRunning:
        return QStringLiteral("not-running");
    }
    return QStringLiteral("connecting");
}

bool ProtocolController::daemonRunning() const
{
    return phase() != Phase::NotRunning;
}

bool ProtocolController::loading() const
{
    return phase() == Phase::Connecting;
}

QString ProtocolController::statusTitle() const
{
    if (loginRequired()) {
        return i18nc("@title", "Scan to sign in");
    }
    switch (phase()) {
    case Phase::NotRunning:
        return i18nc("@title", "whatevrd isn't running");
    case Phase::Connecting:
        return i18nc("@title", "Connecting to whatevrd");
    case Phase::Connected:
        return shellVisible() ? i18nc("@title", "Daemon session ready")
                              : i18nc("@title", "Waiting for whatevrd");
    }
    return i18nc("@title", "Connecting to whatevrd");
}

QString ProtocolController::statusText() const
{
    if (loginRequired()) {
        return i18nc("@info", "Use WhatsApp on your phone to scan the QR code below.");
    }
    switch (phase()) {
    case Phase::NotRunning:
        return i18nc("@info", "The background daemon isn't running. Start it and Whatevr will connect automatically.");
    case Phase::Connecting:
        return i18nc("@info", "Preparing the local daemon connection and reading the current session state.");
    case Phase::Connected:
        return shellVisible()
            ? i18nc("@info", "The daemon is reachable. Chat list and timeline work land next on top of this shell.")
            : i18nc("@info", "Connected to the daemon; waiting for it to come online.");
    }
    return i18nc("@info", "Preparing the local daemon connection and reading the current session state.");
}

QString ProtocolController::detailText() const
{
    QStringList lines;
    // The daemon-reported state/detail is only meaningful while connected; once
    // the link drops it's stale, so don't show it.
    if (phase() == Phase::Connected) {
        const QString state = daemonState();
        if (!state.isEmpty()) {
            lines << i18nc("@info", "State: %1", state);
        }
        const QString detail = m_connectionModel->value().value(QStringLiteral("detail")).toString();
        if (!detail.isEmpty()) {
            lines << detail;
        }
    }
    if (!m_socketPath.isEmpty()) {
        lines << i18nc("@info", "Socket: %1", m_socketPath);
    }
    return lines.join(QLatin1Char('\n'));
}

QString ProtocolController::bannerText() const
{
    return m_bannerText;
}

QString ProtocolController::actionError() const
{
    return m_actionError;
}

QString ProtocolController::primaryActionText() const
{
    // Only offer the daemon-side Reconnect command when there's a live
    // connection to send it on; otherwise the button just retries the socket.
    if (phase() == Phase::Connected && canReconnect() && !loginRequired()) {
        return i18nc("@action:button", "Reconnect");
    }
    return i18nc("@action:button", "Retry");
}

bool ProtocolController::primaryActionEnabled() const
{
    return !m_reconnectInFlight;
}

QString ProtocolController::daemonServiceCommand() const
{
    return QStringLiteral("systemctl --user start whatevrd.service");
}

QString ProtocolController::daemonBinaryCommand() const
{
    return QStringLiteral("whatevrd");
}

QString ProtocolController::daemonInstructions() const
{
    return i18nc("@info",
                 "Start it with systemd:\n"
                 "    systemctl --user start whatevrd.service\n"
                 "or run it directly:\n"
                 "    whatevrd");
}

// --- login page -----------------------------------------------------------

QString ProtocolController::qrCode() const
{
    return m_loginModel->value().value(QStringLiteral("qr")).toMap().value(QStringLiteral("code")).toString();
}

bool ProtocolController::qrAvailable() const
{
    return !qrCode().isEmpty();
}

QString ProtocolController::qrExpiryText() const
{
    const QVariantMap qr = m_loginModel->value().value(QStringLiteral("qr")).toMap();
    if (qr.isEmpty()) {
        return {};
    }
    // The daemon marshals expires_at as an RFC3339 timestamp.
    const QDateTime expiresAt = QDateTime::fromString(qr.value(QStringLiteral("expires_at")).toString(), Qt::ISODateWithMs);
    if (!expiresAt.isValid()) {
        return {};
    }
    return formatQrExpiry(expiresAt.toSecsSinceEpoch());
}

// --- actions --------------------------------------------------------------

void ProtocolController::triggerPrimaryAction()
{
    if (phase() == Phase::Connected && canReconnect() && !loginRequired()) {
        requestReconnect();
        return;
    }
    // Retry: kick an immediate reconnect attempt instead of waiting on the
    // client's backoff tick.
    m_bannerText.clear();
    m_client->start();
    Q_EMIT stateChanged();
}

void ProtocolController::requestReconnect()
{
    if (!m_clientReady || m_reconnectInFlight) {
        return;
    }
    m_reconnectInFlight = true;
    m_bannerText.clear();
    Q_EMIT stateChanged();

    QPointer<ProtocolController> self(this);
    m_client->request(QStringLiteral("daemon.reconnect"), {},
                      [self](const QJsonObject &, const ProtocolError &error) {
                          if (!self) {
                              return;
                          }
                          self->m_reconnectInFlight = false;
                          self->m_bannerText = error.isError()
                              ? i18nc("@info", "Reconnect request failed: %1", error.message)
                              : i18nc("@info", "Reconnect requested. Waiting for daemon updates.");
                          Q_EMIT self->stateChanged();
                      });
}

void ProtocolController::startDaemon()
{
    // Prefer the systemd user unit; fall back to launching the binary directly
    // when systemctl is missing or the unit isn't installed. Either way the
    // client's reconnect loop picks up the socket once it appears.
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
    // Nudge the client to attempt a connection sooner than its backoff tick.
    m_client->start();
    Q_EMIT stateChanged();
}

void ProtocolController::launchDaemonBinary()
{
    if (QProcess::startDetached(QStringLiteral("whatevrd"), {})) {
        return;
    }
    // Neither the systemd unit nor the binary on PATH could be started, so the
    // user's click produced nothing visible. Surface a sticky error; phase()
    // reports NotRunning again (no socket), with the manual instructions shown.
    m_actionError = i18nc("@info",
                          "Couldn't start whatevrd automatically — the systemd service isn't "
                          "installed and the whatevrd binary wasn't found in PATH. Start it "
                          "manually using the commands below.");
    Q_EMIT stateChanged();
}

void ProtocolController::copyToClipboard(const QString &text)
{
    if (QClipboard *clipboard = QGuiApplication::clipboard()) {
        clipboard->setText(text);
    }
}

// --- frontend-only helpers (D7) -------------------------------------------

bool ProtocolController::perfLogging()
{
    static const bool enabled = qEnvironmentVariableIsSet("WHATKEVR_PERF");
    return enabled;
}

void ProtocolController::markChatOpenPhase(const QString &phase)
{
    if (!perfLogging() || !m_openClock.isValid()) {
        return;
    }
    const int rows = m_messagePresentationModel ? m_messagePresentationModel->rowCount() : 0;
    // The view reaches its settled state once before the rows do: a switch
    // clears the window, and for a turn or two the pane is a laid-out, painted,
    // empty list. Stamping that would report a four-millisecond open and stop
    // the clock before the work happened, so an empty window is not an open
    // yet and the clock keeps running.
    if (rows == 0 && (phase == QLatin1String("settled") || phase == QLatin1String("painted"))) {
        return;
    }
    const int delta = rows - m_openPhaseRows;
    m_openPhaseRows = rows;
    qInfo("[perf] open %-10s %6.1f ms  rows=%d (+%d)",
          qPrintable(phase), m_openClock.nsecsElapsed() / 1e6, rows, delta);
    // `painted` is the last phase there is: the transcript is on screen, and
    // anything stamped after it belongs to the next open rather than this one.
    if (phase == QLatin1String("painted")) {
        m_openClock.invalidate();
    }
}

void ProtocolController::setChatDraft(const QString &chatId, const QString &text)
{
    if (chatId.isEmpty()) {
        return;
    }
    const bool had = m_drafts.contains(chatId);
    if (text.trimmed().isEmpty()) {
        if (!had) {
            return;
        }
        m_drafts.remove(chatId);
    } else {
        if (had && m_drafts.value(chatId) == text) {
            return;
        }
        m_drafts.insert(chatId, text);
    }
    savePersistedDrafts();
}

QString ProtocolController::chatDraft(const QString &chatId) const
{
    return m_drafts.value(chatId);
}

void ProtocolController::loadPersistedDrafts()
{
    if (!QSettings().value(QLatin1String(kPersistDraftsKey), true).toBool()) {
        return;
    }
    const QVariantMap stored = QSettings().value(QLatin1String(kDraftsKey)).toMap();
    for (auto it = stored.constBegin(); it != stored.constEnd(); ++it) {
        const QString text = it.value().toString();
        if (!text.isEmpty()) {
            m_drafts.insert(it.key(), text);
        }
    }
}

void ProtocolController::savePersistedDrafts() const
{
    if (!QSettings().value(QLatin1String(kPersistDraftsKey), true).toBool()) {
        return;
    }
    QVariantMap map;
    for (auto it = m_drafts.constBegin(); it != m_drafts.constEnd(); ++it) {
        map.insert(it.key(), it.value());
    }
    QSettings().setValue(QLatin1String(kDraftsKey), map);
}

void ProtocolController::copyImageToClipboard(const QString &localPath)
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

void ProtocolController::copyFileToClipboard(const QString &localPath)
{
    if (localPath.isEmpty() || !QFile::exists(localPath)) {
        return;
    }
    QClipboard *clipboard = QGuiApplication::clipboard();
    if (!clipboard) {
        return;
    }
    // A file URL rather than the decoded bytes: this is what a file manager or
    // another chat app expects to receive when something is pasted into it.
    auto *mime = new QMimeData;
    const QUrl url = QUrl::fromLocalFile(localPath);
    mime->setUrls({url});
    mime->setText(localPath);
    clipboard->setMimeData(mime);
}

bool ProtocolController::saveContactCard(const QString &displayName, const QString &vcard)
{
    if (vcard.isEmpty()) {
        return false;
    }

    const QString directory =
        QStandardPaths::writableLocation(QStandardPaths::CacheLocation) + QStringLiteral("/contacts");
    if (!QDir().mkpath(directory)) {
        Q_EMIT messageActionFailed(i18nc("@info", "Unable to write the contact card"));
        return false;
    }

    // Anything that is not plainly a filename character becomes an underscore:
    // a contact's name is attacker-controlled text arriving over the network,
    // and it must never be able to steer where this writes.
    QString base = displayName.simplified();
    base.replace(QRegularExpression(QStringLiteral("[^\\w .-]"), QRegularExpression::UseUnicodePropertiesOption),
                 QStringLiteral("_"));
    base = base.trimmed();
    if (base.isEmpty()) {
        base = QStringLiteral("contact");
    }
    base.truncate(64);

    const QString path = directory + QLatin1Char('/') + base + QStringLiteral(".vcf");
    QFile file(path);
    if (!file.open(QIODevice::WriteOnly | QIODevice::Truncate)) {
        Q_EMIT messageActionFailed(i18nc("@info", "Unable to write the contact card"));
        return false;
    }
    // vCard is a CRLF format, and some contact managers are strict about it.
    QString normalized = vcard;
    normalized.replace(QStringLiteral("\r\n"), QStringLiteral("\n"));
    normalized.replace(QLatin1Char('\n'), QStringLiteral("\r\n"));
    if (!normalized.endsWith(QStringLiteral("\r\n"))) {
        normalized += QStringLiteral("\r\n");
    }
    if (file.write(normalized.toUtf8()) < 0) {
        file.close();
        Q_EMIT messageActionFailed(i18nc("@info", "Unable to write the contact card"));
        return false;
    }
    file.close();

    if (!QDesktopServices::openUrl(QUrl::fromLocalFile(path))) {
        Q_EMIT messageActionFailed(i18nc("@info", "No application is set up to open contact cards"));
        return false;
    }
    return true;
}

bool ProtocolController::saveMediaAs(const QString &localPath, const QUrl &destUrl)
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

QString ProtocolController::toCommonMark(const QString &text) const
{
    return whatevr::util::whatsAppToCommonMark(text);
}

int ProtocolController::previousGraphemeBoundary(const QString &text, int cursorPosition) const
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

void ProtocolController::handleCommandLine(const QStringList &arguments)
{    QString uri;
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

void ProtocolController::openChatInNewWindow(const QString &chatId)
{
    if (chatId.isEmpty()) {
        return;
    }
    // A second process with its own controller and daemon connection; the
    // deep link selects the chat once its shell is up (same path as a
    // notification cold-start).
    const QString link = QStringLiteral("whatevr://chat/")
        + QString::fromUtf8(QUrl::toPercentEncoding(chatId));
    QProcess::startDetached(QCoreApplication::applicationFilePath(),
                            {QStringLiteral("--new-window"), link});
}

void ProtocolController::openChatFromUri(const QString &uri)
{
    // Expected form: whatevr://chat/<percent-encoded-chat-id> (emitted by the
    // daemon's notification handler). A malformed link still raises the window.
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

void ProtocolController::tryApplyPendingDeepLink()
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

    const QString chatId = std::exchange(m_pendingDeepLinkChatId, {});
    selectChat(chatId);
    Q_EMIT openChatRequested(chatId);
}

// --- reactions to client / view changes -----------------------------------

void ProtocolController::onClientReady()
{
    m_clientReady = true;
    // A fresh successful connection clears any stale start/reconnect banner and
    // the sticky launch error.
    m_bannerText.clear();
    m_actionError.clear();
    sendSessionUpdate();
    Q_EMIT stateChanged();
    // The composer is gated on the connection too — see setSelectedChat.
    Q_EMIT composerChanged();
}

void ProtocolController::onClientDisconnected()
{
    m_clientReady = false;
    m_mediaStreamMessages.clear();
    // The client reset the object-view sinks on drop; their valueChanged already
    // fired. Recompute the gate from the new phase.
    Q_EMIT stateChanged();
    Q_EMIT composerChanged();
}

void ProtocolController::onConnectionValueChanged()
{
    Q_EMIT stateChanged();
}

void ProtocolController::onLoginValueChanged()
{
    if (qrAvailable()) {
        if (!m_qrTimer->isActive()) {
            m_qrTimer->start();
        }
    } else {
        m_qrTimer->stop();
    }
    Q_EMIT stateChanged();
}

void ProtocolController::refreshQrExpiry()
{
    if (!qrAvailable()) {
        m_qrTimer->stop();
        return;
    }
    // qrExpiryText() recomputes from expires_at on read; re-notify so the bound
    // countdown updates each second.
    Q_EMIT stateChanged();
}
