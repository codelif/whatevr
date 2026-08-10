#include "protocolcontroller.h"

#include <QClipboard>
#include <QCoreApplication>
#include <QDateTime>
#include <QDir>
#include <QFile>
#include <QFileInfo>
#include <QGuiApplication>
#include <QImage>
#include <QJsonArray>
#include <QLocale>
#include <QMimeData>
#include <QPointer>
#include <QProcess>
#include <QQmlEngine>
#include <QSet>
#include <QSettings>
#include <QStandardPaths>
#include <QStringList>
#include <QTextBoundaryFinder>
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
// Starred rows are a windowed view like any other collection; the page extends
// as it scrolls instead of asking the daemon for every star at once.
constexpr int kStarredPageSize = 50;
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

    const auto selectionSourceChanged = [this] {
        if (!m_selectedChatId.isEmpty()) {
            if (m_phoneHistoryRequesting && selectedChatHistoryExhausted()) {
                m_phoneHistoryRequesting = false;
                m_phoneHistoryTimer->stop();
                m_phoneHistorySettleTimer->stop();
                Q_EMIT messagesChanged();
            }
            Q_EMIT selectionChanged();
        }
    };
    connect(m_chatsModel, &QAbstractItemModel::dataChanged, this, selectionSourceChanged);
    connect(m_chatsModel, &QAbstractItemModel::rowsInserted, this, selectionSourceChanged);
    connect(m_chatsModel, &QAbstractItemModel::rowsRemoved, this, selectionSourceChanged);
    connect(m_chatsModel, &QAbstractItemModel::modelReset, this, selectionSourceChanged);
    connect(m_archivedModel, &QAbstractItemModel::dataChanged, this, selectionSourceChanged);
    connect(m_archivedModel, &QAbstractItemModel::rowsInserted, this, selectionSourceChanged);
    connect(m_archivedModel, &QAbstractItemModel::rowsRemoved, this, selectionSourceChanged);
    connect(m_archivedModel, &QAbstractItemModel::modelReset, this, selectionSourceChanged);

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

    m_messagesModel = new CollectionViewModel(this);
    m_messagePresentationModel = new ProtocolMessageModel(m_messagesModel, this);
    // Media transfers (D4c): the global `transfers` view is what makes a
    // downloading bubble show progress. The timeline model reads it through by
    // message id at render time — the two views are never merged into one row.
    m_transfersModel = new CollectionViewModel(this);
    m_messagePresentationModel->setTransfersSource(m_transfersModel);
    connect(m_messagesModel, &CollectionViewModel::readyReceived, this, &ProtocolController::onMessagesReady);
    connect(m_messagesModel, &QAbstractItemModel::modelReset, this, &ProtocolController::onMessagesReset);
    connect(m_messagesModel, &CollectionViewModel::countChanged, this, [this] {
        Q_EMIT messagesChanged();
    });
    connect(m_messagesModel, &QAbstractItemModel::rowsInserted, this,
            [this](const QModelIndex &, int first, int) {
        Q_UNUSED(first)
        if (m_phoneHistoryRequesting && m_phoneHistoryGeneration == m_messagesGeneration
            && m_messagePresentationModel->messageIdAt(0) != m_phoneHistoryOldestId) {
            // Backfills arrive as a burst of individual upserts. Restore the
            // viewport only after that burst settles, not after its first row.
            m_phoneHistorySettleTimer->start();
        }
    });

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
    connect(m_starredModel, &CollectionViewModel::countChanged, this, &ProtocolController::starredMessagesChanged);
    connect(m_starredModel, &CollectionViewModel::readyChanged, this, &ProtocolController::starredMessagesChanged);
    connect(m_starredModel, &CollectionViewModel::modelReset, this, &ProtocolController::starredMessagesChanged);

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
        if (m_selectedChatId.isEmpty() || m_pendingReadWatermark.isEmpty()) {
            return;
        }
        const QString chatId = m_selectedChatId;
        const QString watermark = std::exchange(m_pendingReadWatermark, {});
        m_lastReadWatermark = watermark;
        m_client->request(QStringLiteral("chat.mark_read"),
                          {{QStringLiteral("chat_id"), chatId},
                           {QStringLiteral("up_to_message_id"), watermark}});
    });

    m_phoneHistoryTimer = new QTimer(this);
    m_phoneHistoryTimer->setSingleShot(true);
    m_phoneHistoryTimer->setInterval(kPhoneHistoryTimeoutMs);
    connect(m_phoneHistoryTimer, &QTimer::timeout, this, [this] {
        if (m_phoneHistoryRequesting) {
            m_phoneHistoryRequesting = false;
            Q_EMIT messagesChanged();
        }
    });

    m_phoneHistorySettleTimer = new QTimer(this);
    m_phoneHistorySettleTimer->setSingleShot(true);
    m_phoneHistorySettleTimer->setInterval(50);
    connect(m_phoneHistorySettleTimer, &QTimer::timeout, this, [this] {
        if (m_phoneHistoryRequesting && m_phoneHistoryGeneration == m_messagesGeneration) {
            m_phoneHistoryRequesting = false;
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
    delete m_typingSub;
    delete m_syncSub;
    delete m_messagesSub;
    delete m_presenceSub;
    delete m_receiptsSub;
    delete m_pinnedSub;
    delete m_forwardTargetsSub;
    delete m_transfersSub;
    delete m_starredSub;
    delete m_infoCardSub;
    delete m_groupMembersSub;
    delete m_chatMembersSub;
    delete m_blocklistSub;
    delete m_privacySub;
    delete m_preferencesSub;
    delete m_selfSub;
    m_chatMembersSub = nullptr;
    m_starredSub = nullptr;
    m_infoCardSub = nullptr;
    m_groupMembersSub = nullptr;
    m_blocklistSub = nullptr;
    m_privacySub = nullptr;
    m_preferencesSub = nullptr;
    m_selfSub = nullptr;
    m_connectionSub = nullptr;
    m_loginSub = nullptr;
    m_chatsSub = nullptr;
    m_archivedSub = nullptr;
    m_typingSub = nullptr;
    m_syncSub = nullptr;
    m_messagesSub = nullptr;
    m_presenceSub = nullptr;
    m_receiptsSub = nullptr;
    m_pinnedSub = nullptr;
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
    // The whatevr protocol socket lives under whatevr/, next to (not the same as)
    // the gRPC socket under whatevrd/ — see whatevrd/internal/app/paths.go.
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
    m_chatsModel->onReset();
    m_archivedModel->onReset();

    // Active and archived are two disjoint `chats` subscriptions; both honour the
    // selected filter so the archived section narrows with the sidebar the same
    // way the active list does.
    m_chatsSub = m_client->subscribe(
        QStringLiteral("chats"),
        {{QStringLiteral("filter"), chatFilterName()}, {QStringLiteral("archived"), false}},
        m_chatsModel);
    m_archivedSub = m_client->subscribe(
        QStringLiteral("chats"),
        {{QStringLiteral("filter"), chatFilterName()}, {QStringLiteral("archived"), true}},
        m_archivedModel);
}

QAbstractItemModel *ProtocolController::chatsModel() const
{
    return m_chatsModel;
}

QAbstractItemModel *ProtocolController::archivedChatsModel() const
{
    return m_archivedModel;
}

int ProtocolController::archivedCount() const
{
    return m_archivedModel->count();
}

bool ProtocolController::chatTyping(const QString &chatId) const
{
    // The typing view is keyed by chat_id; a present row means someone is
    // composing in that chat.
    return !chatId.isEmpty() && m_typingModel->indexOfId(chatId) >= 0;
}

void ProtocolController::setChatFilter(int filter)
{
    if (filter < 0 || filter > 2) {
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

void ProtocolController::setChatPinned(const QString &chatId, bool pinned)
{
    if (chatId.isEmpty()) {
        return;
    }
    m_client->request(QStringLiteral("chat.pin"),
                      {{QStringLiteral("chat_id"), chatId}, {QStringLiteral("pinned"), pinned}});
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
    QVariantMap item = m_chatsModel->itemById(m_selectedChatId);
    if (item.isEmpty()) {
        item = m_archivedModel->itemById(m_selectedChatId);
    }
    return item;
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

bool ProtocolController::selectedChatHistoryExhausted() const
{
    return selectedChatItem().value(QStringLiteral("history_exhausted")).toBool();
}

bool ProtocolController::messagesLoading() const
{
    return hasSelectedChat() && (m_waitingInitialMessages || !m_messagesModel->isReady());
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

    m_selectedChatId = chatId;
    const QString anchor = selectedChatUnreadCount() > 0 ? QStringLiteral("unread") : QStringLiteral("latest");
    m_selectedChatId.clear();
    setSelectedChat(chatId, anchor, {});
}

void ProtocolController::setSelectedChat(const QString &chatId, const QString &anchor, const QString &jumpMessageId)
{
    const bool selectionChanged = chatId != m_selectedChatId;
    m_selectedChatId = chatId;
    m_stickerController->setChatId(chatId);
    if (selectionChanged) {
        m_pendingReadWatermark.clear();
        m_lastReadWatermark.clear();
        m_readTimer->stop();
        // The in-chat search is scoped to one conversation; switching ends it.
        closeChatSearch();
    }
    m_phoneHistoryRequesting = false;
    m_phoneHistoryTimer->stop();
    m_phoneHistorySettleTimer->stop();

    if (selectionChanged) {
        Q_EMIT this->selectionChanged();
    }
    sendSessionUpdate();
    updatePresenceSubscription();
    updatePinnedSubscription();
    updateChatMembersSubscription();

    if (chatId.isEmpty()) {
        delete m_messagesSub;
        m_messagesSub = nullptr;
        m_requestedAnchor.clear();
        m_effectiveAnchor.clear();
        m_pendingJumpMessageId.clear();
        m_displayedMessagesChatId.clear();
        m_waitingInitialMessages = false;
        m_unreadAnchorMessageId.clear();
        m_unreadAnchorCount = 0;
        m_unreadAnchorResolving = false;
        m_messagesModel->onReset();
        Q_EMIT unreadAnchorChanged();
        Q_EMIT messagesChanged();
        return;
    }

    if (!m_conversationVisible) {
        delete m_messagesSub;
        m_messagesSub = nullptr;
        m_requestedAnchor = anchor;
        m_effectiveAnchor = anchor;
        m_pendingJumpMessageId = jumpMessageId;
        m_displayedMessagesChatId.clear();
        m_waitingInitialMessages = false;
        m_messagesModel->onReset();
        Q_EMIT messagesChanged();
        return;
    }

    subscribeMessages(anchor, jumpMessageId);
}

void ProtocolController::subscribeMessages(const QString &anchor, const QString &jumpMessageId)
{
    if (m_phoneHistoryRequesting) {
        m_phoneHistoryRequesting = false;
        m_phoneHistoryTimer->stop();
        m_phoneHistorySettleTimer->stop();
    }
    if (m_readTimer->isActive() && !m_pendingReadWatermark.isEmpty()) {
        m_readTimer->stop();
        const QString watermark = std::exchange(m_pendingReadWatermark, {});
        m_lastReadWatermark = watermark;
        m_client->request(QStringLiteral("chat.mark_read"),
                          {{QStringLiteral("chat_id"), m_selectedChatId},
                           {QStringLiteral("up_to_message_id"), watermark}});
    }
    delete m_messagesSub;
    m_messagesSub = nullptr;
    ++m_messagesGeneration;

    m_requestedAnchor = anchor;
    m_effectiveAnchor = anchor;
    m_pendingJumpMessageId = jumpMessageId;
    m_pendingExtendDirection.clear();
    m_displayedMessagesChatId.clear();
    m_messageErrorText.clear();
    m_waitingInitialMessages = true;
    m_refillingAfterReset = false;
    m_olderMessagesLoading = false;
    m_newerMessagesLoading = false;
    m_canLoadOlderMessages = false;
    m_canLoadNewerMessages = false;
    m_olderMessagesFailed = false;
    m_newerMessagesFailed = false;
    m_messagesAtLiveEdge = anchor == QLatin1String("latest");
    m_unreadAnchorMessageId.clear();
    m_unreadAnchorCount = anchor == QLatin1String("unread") ? selectedChatUnreadCount() : 0;
    m_unreadAnchorResolving = anchor == QLatin1String("unread");
    m_messagesModel->onReset();
    Q_EMIT unreadAnchorChanged();
    Q_EMIT messagesChanged();

    m_messagesSub = m_client->subscribe(
        QStringLiteral("messages"),
        {{QStringLiteral("chat_id"), m_selectedChatId},
         {QStringLiteral("limit"), kMessagePageSize},
         {QStringLiteral("anchor"), anchor}},
        m_messagesModel);
    connect(m_messagesSub, &Subscription::subscribed, this, &ProtocolController::onMessagesSubscribed);
    connect(m_messagesSub, &Subscription::failed, this, &ProtocolController::onMessagesFailed);
    connect(m_messagesSub, &Subscription::extendFailed, this,
            [this](const QString &code, const QString &message) {
                const QString direction = std::exchange(m_pendingExtendDirection, {});
                if (direction == QLatin1String("older")) {
                    m_olderMessagesLoading = false;
                    m_olderMessagesFailed = true;
                } else if (direction == QLatin1String("newer")) {
                    m_newerMessagesLoading = false;
                    m_newerMessagesFailed = true;
                }
                Q_UNUSED(code)
                Q_UNUSED(message)
                Q_EMIT messagesChanged();
            });
}

void ProtocolController::onMessagesSubscribed(const QVariantMap &meta)
{
    m_messageErrorText.clear();
    if (m_requestedAnchor == QLatin1String("unread")) {
        m_unreadAnchorMessageId = meta.value(QStringLiteral("anchor_id")).toString();
        m_unreadAnchorResolving = false;
        if (m_unreadAnchorMessageId.isEmpty()) {
            // No unread anchor means the daemon deliberately degraded to the
            // live edge; there is no divider to render.
            m_effectiveAnchor = QStringLiteral("latest");
            m_unreadAnchorCount = 0;
            m_messagesAtLiveEdge = true;
        }
        Q_EMIT unreadAnchorChanged();
    }
}

void ProtocolController::onMessagesReady(bool exhausted)
{
    m_messageErrorText.clear();
    if (!m_pendingExtendDirection.isEmpty()) {
        const QString direction = std::exchange(m_pendingExtendDirection, {});
        if (direction == QLatin1String("older")) {
            m_olderMessagesLoading = false;
            m_olderMessagesFailed = false;
            m_canLoadOlderMessages = !exhausted;
        } else {
            m_newerMessagesLoading = false;
            m_newerMessagesFailed = false;
            m_canLoadNewerMessages = !exhausted;
            if (exhausted) {
                m_messagesAtLiveEdge = true;
            }
        }
        if (m_refillingAfterReset) {
            m_refillingAfterReset = false;
            m_waitingInitialMessages = false;
            m_displayedMessagesChatId = m_selectedChatId;
        }
        Q_EMIT messagesChanged();
        return;
    }

    if (!m_waitingInitialMessages) {
        return;
    }
    m_waitingInitialMessages = false;
    m_displayedMessagesChatId = m_selectedChatId;
    if (m_refillingAfterReset) {
        m_refillingAfterReset = false;
        m_olderMessagesFailed = false;
        m_newerMessagesFailed = false;
        Q_EMIT messagesChanged();
        return;
    }
    if (m_effectiveAnchor == QLatin1String("latest")) {
        m_messagesAtLiveEdge = true;
        m_canLoadOlderMessages = !exhausted;
        m_canLoadNewerMessages = false;
    } else {
        // Initial anchored exhaustion describes both frontiers together. When
        // false, probe each independently as the viewport approaches it.
        m_messagesAtLiveEdge = exhausted;
        m_canLoadOlderMessages = !exhausted;
        m_canLoadNewerMessages = !exhausted;
    }

    Q_EMIT messagesChanged();
    const QString jumpId = std::exchange(m_pendingJumpMessageId, {});
    if (!jumpId.isEmpty()) {
        if (m_messagePresentationModel->indexOf(jumpId) >= 0) {
            m_jumpFallbackAnchor.clear();
            Q_EMIT messageJumpReady(jumpId);
        } else {
            Q_EMIT messageJumpUnavailable(jumpId);
            const QString fallback = m_jumpFallbackAnchor.isEmpty() ? QStringLiteral("latest")
                                                                     : std::exchange(m_jumpFallbackAnchor, {});
            const QString chatId = m_selectedChatId;
            const int generation = m_messagesGeneration;
            QTimer::singleShot(0, this, [this, fallback, chatId, generation] {
                if (m_selectedChatId == chatId && m_messagesGeneration == generation
                    && m_conversationVisible) {
                    subscribeMessages(fallback);
                }
            });
        }
    }
}

void ProtocolController::onMessagesFailed(const QString &code, const QString &message)
{
    if (code == QLatin1String("io") && m_messagesSub) {
        return; // live subscriptions auto-resubscribe after reconnect
    }
    m_waitingInitialMessages = false;
    m_unreadAnchorResolving = false;
    const QString jumpId = std::exchange(m_pendingJumpMessageId, {});
    if (!jumpId.isEmpty()) {
        Q_EMIT messageJumpUnavailable(jumpId);
        const QString fallback = m_jumpFallbackAnchor.isEmpty() ? QStringLiteral("latest")
                                                                 : std::exchange(m_jumpFallbackAnchor, {});
        const QString chatId = m_selectedChatId;
        const int generation = m_messagesGeneration;
        QTimer::singleShot(0, this, [this, fallback, chatId, generation] {
            if (m_selectedChatId == chatId && m_messagesGeneration == generation
                && m_conversationVisible) {
                subscribeMessages(fallback);
            }
        });
    } else {
        m_messageErrorText = message;
    }
    Q_EMIT unreadAnchorChanged();
    Q_EMIT messagesChanged();
}

void ProtocolController::onMessagesReset()
{
    if (m_selectedChatId.isEmpty()) {
        return;
    }
    m_displayedMessagesChatId.clear();
    if (!m_conversationVisible) {
        m_waitingInitialMessages = false;
        Q_EMIT messagesChanged();
        return;
    }
    const bool wasWaitingInitial = m_waitingInitialMessages;
    m_waitingInitialMessages = true;
    const bool inConnectionReset = m_clientReady && m_messagesSub && m_messagesSub->isActive();
    m_refillingAfterReset = inConnectionReset && !wasWaitingInitial;
    if (!inConnectionReset) {
        m_pendingExtendDirection.clear();
    }
    if (!inConnectionReset || m_pendingExtendDirection.isEmpty()) {
        m_olderMessagesLoading = false;
        m_newerMessagesLoading = false;
    }
    if (!inConnectionReset) {
        m_canLoadOlderMessages = false;
        m_canLoadNewerMessages = false;
    }
    if (!inConnectionReset && m_requestedAnchor == QLatin1String("unread")) {
        m_unreadAnchorMessageId.clear();
        m_unreadAnchorResolving = true;
        Q_EMIT unreadAnchorChanged();
    }
    Q_EMIT messagesChanged();
}

void ProtocolController::extendMessages(const QString &direction, bool force)
{
    if (!m_messagesSub || !m_pendingExtendDirection.isEmpty()) {
        return;
    }
    if (!force && direction == QLatin1String("older") && !m_canLoadOlderMessages) {
        return;
    }
    if (!force && direction == QLatin1String("newer") && !m_canLoadNewerMessages) {
        return;
    }
    m_pendingExtendDirection = direction;
    if (direction == QLatin1String("older")) {
        m_olderMessagesLoading = true;
    } else {
        m_newerMessagesLoading = true;
    }
    Q_EMIT messagesChanged();
    m_messagesSub->extend(kMessagePageSize, direction);
}

void ProtocolController::loadOlderMessages()
{
    if (m_olderMessagesFailed) {
        m_olderMessagesFailed = false;
        Q_EMIT messagesChanged();
    }
    extendMessages(QStringLiteral("older"));
}

void ProtocolController::loadNewerMessages()
{
    if (m_newerMessagesFailed) {
        m_newerMessagesFailed = false;
        Q_EMIT messagesChanged();
    }
    extendMessages(QStringLiteral("newer"));
}

void ProtocolController::requestOlderMessagesFromPhone()
{
    if (m_selectedChatId.isEmpty() || m_phoneHistoryRequesting || selectedChatHistoryExhausted()) {
        return;
    }
    const QString chatId = m_selectedChatId;
    const int generation = m_messagesGeneration;
    m_phoneHistoryRequesting = true;
    m_phoneHistoryOldestId = m_messagePresentationModel->messageIdAt(0);
    m_phoneHistoryGeneration = generation;
    m_phoneHistoryTimer->start();
    Q_EMIT messagesChanged();
    m_client->request(QStringLiteral("chat.request_older"),
                      {{QStringLiteral("chat_id"), chatId}},
                      [this, chatId, generation](const QJsonObject &result, const ProtocolError &error) {
                          if (chatId != m_selectedChatId || generation != m_messagesGeneration) {
                              return;
                          }
                          if (error.isError() || !result.value(QStringLiteral("requested")).toBool()) {
                              m_phoneHistoryRequesting = false;
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
    m_jumpFallbackAnchor = m_effectiveAnchor.isEmpty() ? QStringLiteral("latest") : m_effectiveAnchor;
    subscribeMessages(messageId, messageId);
}

void ProtocolController::jumpToBottom()
{
    if (m_selectedChatId.isEmpty() || m_effectiveAnchor == QLatin1String("latest")) {
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
    m_jumpFallbackAnchor = QStringLiteral("latest");
    setSelectedChat(chatId, messageId, messageId);
}

void ProtocolController::retryMessages()
{
    if (m_selectedChatId.isEmpty()) {
        return;
    }
    const QString anchor = m_requestedAnchor.isEmpty() ? QStringLiteral("latest") : m_requestedAnchor;
    subscribeMessages(anchor, m_pendingJumpMessageId);
}

void ProtocolController::markSelectedChatViewed(const QString &upToMessageId)
{
    if (m_selectedChatId.isEmpty() || upToMessageId.isEmpty()) {
        return;
    }
    const int candidate = m_messagePresentationModel->indexOf(upToMessageId);
    const int pending = m_messagePresentationModel->indexOf(m_pendingReadWatermark);
    const int sent = m_messagePresentationModel->indexOf(m_lastReadWatermark);
    if (!m_pendingReadWatermark.isEmpty() && pending < 0) {
        return;
    }
    if (candidate >= 0 && (candidate < pending || candidate < sent)) {
        return;
    }
    m_pendingReadWatermark = upToMessageId;
    m_readTimer->start();
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
    updateChatMembersSubscription();
    if (!visible) {
        m_phoneHistoryRequesting = false;
        m_phoneHistoryTimer->stop();
        m_phoneHistorySettleTimer->stop();
        delete m_messagesSub;
        m_messagesSub = nullptr;
        m_displayedMessagesChatId.clear();
        m_waitingInitialMessages = false;
        m_messagesModel->onReset();
        sendSessionUpdate();
        Q_EMIT messagesChanged();
        return;
    }
    sendSessionUpdate();
    if (!m_selectedChatId.isEmpty()) {
        const QString anchor = selectedChatUnreadCount() > 0 ? QStringLiteral("unread") : QStringLiteral("latest");
        subscribeMessages(anchor);
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
    return hasSelectedChat() && m_clientReady;
}

void ProtocolController::dismissUnreadAnchor()
{
    const bool changed = !m_unreadAnchorMessageId.isEmpty() || m_unreadAnchorCount != 0 || m_unreadAnchorResolving;
    m_unreadAnchorMessageId.clear();
    m_unreadAnchorCount = 0;
    m_unreadAnchorResolving = false;
    if (changed) {
        Q_EMIT unreadAnchorChanged();
    }
}

void ProtocolController::sendText(const QString &text, const QString &replyToMessageId, const QStringList &mentionedJids)
{
    const QString trimmed = plainTextFromQtRichText(text).trimmed();
    if (m_selectedChatId.isEmpty() || trimmed.isEmpty() || m_sendInFlight) {
        return;
    }

    setSelectedChatComposing(false);
    dismissUnreadAnchor();

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

void ProtocolController::sendMedia(const QString &fileUrl, const QString &caption, const QString &replyToMessageId)
{
    if (m_selectedChatId.isEmpty() || fileUrl.isEmpty() || m_sendInFlight) {
        return;
    }

    const QUrl url(fileUrl);
    const QString filePath = url.isLocalFile() ? url.toLocalFile() : fileUrl;
    if (filePath.isEmpty()) {
        return;
    }

    setSelectedChatComposing(false);
    dismissUnreadAnchor();

    QJsonObject params{{QStringLiteral("chat_id"), m_selectedChatId},
                       {QStringLiteral("path"), filePath},
                       {QStringLiteral("caption"), plainTextFromQtRichText(caption).trimmed()}};
    if (const QString reply = replyToMessageId.trimmed(); !reply.isEmpty()) {
        params.insert(QStringLiteral("reply_to"), reply);
    }

    m_sendInFlight = true;
    m_composerErrorText.clear();
    Q_EMIT composerChanged();

    m_client->request(QStringLiteral("send.media"), params, [this](const QJsonObject &, const ProtocolError &error) {
        m_sendInFlight = false;
        m_composerErrorText = error.isError()
            ? (error.message.isEmpty() ? i18nc("@info", "Unable to send image") : error.message)
            : QString();
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
    const bool wanted = m_conversationVisible && m_selectedChatId.endsWith(QLatin1String("@g.us"));
    const QString target = wanted ? m_selectedChatId : QString();
    if (target == m_chatMembersChatId) {
        return;
    }
    m_chatMembersChatId = target;
    delete m_chatMembersSub;
    m_chatMembersSub = nullptr;
    m_chatMembersModel->onReset();
    if (!target.isEmpty()) {
        m_chatMembersSub = m_client->subscribe(QStringLiteral("group_members"),
                                               {{QStringLiteral("chat_id"), target}}, m_chatMembersModel);
    }
    ++m_chatMembersRevision;
    Q_EMIT chatMembersChanged();
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
        QStringLiteral("auto_download_documents"), QStringLiteral("auto_download_stickers")};
    if (!keys.contains(key)) {
        return;
    }
    sendSettingsCommand(QStringLiteral("preferences.set"), {{key, value}},
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
}

void ProtocolController::onClientDisconnected()
{
    m_clientReady = false;
    // The client reset the object-view sinks on drop; their valueChanged already
    // fired. Recompute the gate from the new phase.
    Q_EMIT stateChanged();
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
