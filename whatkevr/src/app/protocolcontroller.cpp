#include "protocolcontroller.h"

#include <QClipboard>
#include <QDateTime>
#include <QDir>
#include <QFileInfo>
#include <QGuiApplication>
#include <QPointer>
#include <QProcess>
#include <QQmlEngine>
#include <QStandardPaths>
#include <QStringList>
#include <QTimer>

#include <KLocalizedString>

#include <utility>

#include "collectionviewmodel.h"
#include "objectviewmodel.h"
#include "protocolclient.h"

using whatevr::proto::CollectionViewModel;
using whatevr::proto::ObjectViewModel;
using whatevr::proto::ProtocolClient;
using whatevr::proto::ProtocolError;
using whatevr::proto::Subscription;

namespace
{
ProtocolController *s_instance = nullptr;

// Cold-start grace: hold the neutral splash rather than flashing the
// "not running" page while the daemon socket may still be appearing right
// after launch. Matches AppController's 1s window.
constexpr int kStartupGraceMs = 1000;

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
    // Every failed connect attempt also lands here (the client funnels connect
    // errors through disconnected()); recomputing phase is idempotent.

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

    // Typing overlay (D2b2): the global `typing` collection. Any change (a chat
    // starting/stopping, or a reset) bumps typingRevision so per-row isTyping
    // bindings re-evaluate.
    m_typingModel = new CollectionViewModel(this);
    const auto bumpTyping = [this] {
        ++m_typingRevision;
        Q_EMIT typingChanged();
    };
    connect(m_typingModel, &CollectionViewModel::countChanged, this, bumpTyping);
    connect(m_typingModel, &CollectionViewModel::modelReset, this, bumpTyping);
    connect(m_typingModel, &CollectionViewModel::dataChanged, this, bumpTyping);

    // History-sync strip (D2b2): the `sync` object view; the strip state is
    // derived from its single item.
    m_syncModel = new ObjectViewModel(this);
    connect(m_syncModel, &ObjectViewModel::valueChanged, this, &ProtocolController::recomputeHistorySync);

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
    m_connectionSub = nullptr;
    m_loginSub = nullptr;
    m_chatsSub = nullptr;
    m_archivedSub = nullptr;
    m_typingSub = nullptr;
    m_syncSub = nullptr;
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
    // The typing and sync views are global (unfiltered) and observed for the
    // whole session, like connection/login.
    m_typingSub = m_client->subscribe(QStringLiteral("typing"), {}, m_typingModel);
    m_syncSub = m_client->subscribe(QStringLiteral("sync"), {}, m_syncModel);
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

// --- reactions to client / view changes -----------------------------------

void ProtocolController::onClientReady()
{
    m_clientReady = true;
    // A fresh successful connection clears any stale start/reconnect banner and
    // the sticky launch error.
    m_bannerText.clear();
    m_actionError.clear();
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
