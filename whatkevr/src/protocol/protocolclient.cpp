#include "protocolclient.h"

#include <QJsonArray>
#include <QJsonDocument>
#include <QJsonValue>
#include <QLocalSocket>
#include <QPointer>
#include <QTimer>
#include <QtGlobal>

#include <utility>

namespace whatevr::proto
{

namespace
{
// Reconnect backoff. Deliberately fixed and short: the daemon is a local
// process; a dropped socket usually means a restart we want to ride out fast.
constexpr int kReconnectDelayMs = 1000;

// Guard against a wedged peer flooding us with an unframed blob: a single
// protocol object is never remotely this large.
constexpr int kMaxLineBytes = 8 * 1024 * 1024;

// How long the daemon has to answer `hello`. A socket that connects and then
// says nothing is otherwise a permanent wedge: the client sits in Handshaking,
// and reconnect only ever runs off a disconnect that is never coming.
// Generous for a local process, so a busy daemon is never cut off.
constexpr int kHandshakeTimeoutMs = 10000;

// Ceilings on what one connection may owe. Both exist because a caller is never
// told "maybe": every callback fires exactly once, so an unanswered request has
// to be held until something answers it, and holding an unbounded number of
// them is how a daemon that stops replying turns into a frontend that grows
// without limit.
constexpr int kMaxInFlightRequests = 4096;
constexpr int kMaxQueuedRequests = 1024;

ProtocolError errorFromResponse(const QJsonObject &err)
{
    return ProtocolError{
        err.value(QStringLiteral("code")).toString(),
        err.value(QStringLiteral("message")).toString(),
    };
}
} // namespace

// ---------------------------------------------------------------------------
// Subscription
// ---------------------------------------------------------------------------

Subscription::Subscription(ProtocolClient *client, QString view, QJsonObject params, ViewSink *sink)
    : QObject(client)
    , m_client(client)
    , m_view(std::move(view))
    , m_params(std::move(params))
    , m_sink(sink)
{
}

Subscription::~Subscription()
{
    // Tell the daemon to drop it; also unregister locally so no late event is
    // routed to a dangling sink.
    if (m_client) {
        m_client->sendUnsubscribe(this);
        m_client->removeSubscription(this);
    }
}

void Subscription::extend(int count, const QString &direction)
{
    if (m_subId < 0) {
        // Not yet subscribed — remember it and flush once the sub id lands.
        m_pendingExtends.append(PendingExtend{count, direction});
        return;
    }
    if (m_client) {
        m_client->sendExtend(this, count, direction);
    }
}

void Subscription::unsubscribe()
{
    if (!m_client) {
        return;
    }
    m_client->sendUnsubscribe(this);
    m_client->removeSubscription(this);
    m_client = nullptr;
    m_subId = -1;
}

// ---------------------------------------------------------------------------
// ProtocolClient
// ---------------------------------------------------------------------------

ProtocolClient::ProtocolClient(QString socketPath, QString clientName, QObject *parent)
    : QObject(parent)
    , m_socketPath(std::move(socketPath))
    , m_clientName(std::move(clientName))
    , m_socket(new QLocalSocket(this))
    , m_reconnectTimer(new QTimer(this))
{
    m_reconnectTimer->setSingleShot(true);
    m_reconnectTimer->setInterval(kReconnectDelayMs);
    connect(m_reconnectTimer, &QTimer::timeout, this, [this] {
        if (m_running && m_state == State::Idle) {
            start();
        }
    });

    m_handshakeTimer = new QTimer(this);
    // Named so a test can shorten the deadline without widening the API.
    m_handshakeTimer->setObjectName(QStringLiteral("protocolHandshakeTimer"));
    m_handshakeTimer->setSingleShot(true);
    m_handshakeTimer->setInterval(kHandshakeTimeoutMs);
    connect(m_handshakeTimer, &QTimer::timeout, this, [this] {
        if (m_state != State::Handshaking) {
            return;
        }
        Q_EMIT errorOccurred(QStringLiteral("daemon did not answer the handshake; reconnecting"));
        // Through the ordinary teardown, so pending callers are failed and a
        // reconnect is scheduled. Aborting alone would not: a socket that is
        // open and silent never emits disconnected().
        m_socket->abort();
        onSocketDisconnected();
    });

    connect(m_socket, &QLocalSocket::connected, this, &ProtocolClient::onSocketConnected);
    connect(m_socket, &QLocalSocket::disconnected, this, &ProtocolClient::onSocketDisconnected);
    connect(m_socket, &QLocalSocket::readyRead, this, &ProtocolClient::onReadyRead);
    connect(m_socket, &QLocalSocket::errorOccurred, this, [this](QLocalSocket::LocalSocketError) {
        // A connect failure surfaces here without a disconnected(); funnel both
        // paths through the same teardown so reconnect always gets scheduled.
        Q_EMIT errorOccurred(m_socket->errorString());
        onSocketDisconnected();
    });
}

ProtocolClient::~ProtocolClient()
{
    m_running = false;
    // Detach live subscriptions (our QObject children) so their destructors,
    // which run after this body, don't call back into a half-torn-down client.
    for (Subscription *sub : std::as_const(m_subscriptions)) {
        sub->m_client = nullptr;
    }
}

void ProtocolClient::start()
{
    m_running = true;
    if (m_state != State::Idle) {
        return;
    }
    m_state = State::Connecting;
    m_readBuffer.clear();
    m_socket->connectToServer(m_socketPath);
}

void ProtocolClient::stop()
{
    m_running = false;
    m_reconnectTimer->stop();
    m_handshakeTimer->stop();
    m_state = State::Idle;
    m_socket->abort();
    failAllPending(QStringLiteral("io"), QStringLiteral("client stopped"));
}

void ProtocolClient::onSocketConnected()
{
    m_state = State::Handshaking;
    m_handshakeTimer->start();
    // The first request on a connection must be `hello`. It rides the normal
    // request path (id-correlated); everything else waits behind it.
    QJsonObject params{
        {QStringLiteral("client"), m_clientName},
        {QStringLiteral("protocol"), kProtocolVersion},
    };
    sendRequest(QStringLiteral("hello"), params,
                [this](const QJsonObject &result, const ProtocolError &error) {
                    handleHelloReply(result, error);
                });
}

void ProtocolClient::onSocketDisconnected()
{
    const bool wasConnecting = m_state != State::Idle;
    if (m_state == State::Idle && !m_running) {
        return; // deliberate stop(); nothing to tear down
    }

    m_state = State::Idle;
    m_handshakeTimer->stop();
    m_readBuffer.clear();
    m_subsBySubId.clear();

    // Fail every in-flight request so callers are never left hanging.
    failAllPending(QStringLiteral("io"), QStringLiteral("connection lost"));

    // Live subscriptions must discard their local copy; they will be re-issued
    // on reconnect and refilled from scratch.
    for (Subscription *sub : std::as_const(m_subscriptions)) {
        sub->m_subId = -1;
        if (sub->m_sink) {
            sub->m_sink->onReset();
        }
    }

    if (wasConnecting) {
        Q_EMIT disconnected();
    }
    scheduleReconnect();
}

void ProtocolClient::scheduleReconnect()
{
    if (m_running && !m_reconnectTimer->isActive()) {
        m_reconnectTimer->start();
    }
}

void ProtocolClient::onReadyRead()
{
    m_readBuffer += m_socket->readAll();
    int newline = m_readBuffer.indexOf('\n');
    while (newline >= 0) {
        const QByteArray line = m_readBuffer.left(newline);
        m_readBuffer.remove(0, newline + 1);
        if (!line.isEmpty()) {
            dispatchLine(line);
        }
        newline = m_readBuffer.indexOf('\n');
    }
    // Close the batch for every sink this drain touched. A fill arrives as one
    // frame per item, and the daemon flushes a burst in one write, so this is
    // where a whole window becomes a single model transaction.
    const QList<BatchedSink> touched = std::move(m_batchedSinks);
    m_batchedSinks.clear();
    for (const BatchedSink &entry : touched) {
        // A sink can be destroyed part way through the drain that batched it:
        // an event delivered above reaches a handler that evicts the warm
        // window this sink belongs to, and the eviction deletes it. Closing the
        // batch on that pointer is a use-after-free, so the token it left
        // behind is what says whether there is still anything to close.
        if (entry.alive.expired()) {
            continue;
        }
        entry.sink->onBatchEnd();
    }
    if (m_readBuffer.size() > kMaxLineBytes) {
        Q_EMIT errorOccurred(QStringLiteral("oversized protocol frame; dropping connection"));
        m_socket->abort();
        onSocketDisconnected();
    }
}

// noteBatched records a sink whose batch must be closed at the end of this
// drain. Linear scan on purpose: a drain touches a handful of distinct sinks,
// so this is cheaper than hashing.
void ProtocolClient::noteBatched(ViewSink *sink)
{
    for (const BatchedSink &entry : m_batchedSinks) {
        // Address alone is not identity across a drain that freed something: a
        // new sink can land exactly where a dead one was, and matching it would
        // leave the live sink without the batch it just asked for. An entry
        // whose token has expired describes a sink that is gone, never this one.
        if (entry.sink == sink && !entry.alive.expired()) {
            return;
        }
    }
    m_batchedSinks.append({sink, sink->lifetime()});
    sink->onBatchBegin();
}

void ProtocolClient::dispatchLine(const QByteArray &line)
{
    QJsonParseError parseError;
    const QJsonDocument doc = QJsonDocument::fromJson(line, &parseError);
    if (parseError.error != QJsonParseError::NoError || !doc.isObject()) {
        Q_EMIT errorOccurred(QStringLiteral("malformed message from daemon: %1").arg(parseError.errorString()));
        return;
    }
    const QJsonObject msg = doc.object();
    if (msg.contains(QStringLiteral("event"))) {
        handleEvent(msg);
    } else if (msg.contains(QStringLiteral("id"))) {
        handleResponse(msg);
    }
    // Anything else (no id, no event) is unknown; rule 5 says ignore it.
}

void ProtocolClient::handleResponse(const QJsonObject &msg)
{
    const int id = msg.value(QStringLiteral("id")).toInt(-1);
    const auto it = m_pending.find(id);
    if (it == m_pending.end()) {
        return; // response to an unknown/already-completed id
    }
    const ResponseCallback callback = it.value();
    m_pending.erase(it);

    if (!callback) {
        return;
    }
    if (msg.contains(QStringLiteral("error"))) {
        callback({}, errorFromResponse(msg.value(QStringLiteral("error")).toObject()));
    } else {
        callback(msg.value(QStringLiteral("result")).toObject(), ProtocolError{});
    }
}

void ProtocolClient::handleEvent(const QJsonObject &msg)
{
    const QString event = msg.value(QStringLiteral("event")).toString();

    // Connection-directed events carry no `sub`.
    if (!msg.contains(QStringLiteral("sub"))) {
        if (event == QLatin1String("open_chat")) {
            Q_EMIT openChatRequested(msg.value(QStringLiteral("chat_id")).toString());
        } else if (event == QLatin1String("activate_window")) {
            Q_EMIT activateWindowRequested();
        } else if (event == QLatin1String("show_tray_menu")) {
            Q_EMIT showTrayMenuRequested(msg.value(QStringLiteral("x")).toInt(),
                                         msg.value(QStringLiteral("y")).toInt());
        } else if (event == QLatin1String("media_stream_update")) {
            Q_EMIT mediaStreamUpdated(msg.value(QStringLiteral("stream_id")).toString(),
                                      msg.value(QStringLiteral("message_id")).toString(),
                                      msg.value(QStringLiteral("state")).toString(),
                                      msg.value(QStringLiteral("path")).toString(),
                                      msg.value(QStringLiteral("error")).toString());
        }
        return;
    }

    const int subId = msg.value(QStringLiteral("sub")).toInt(-1);
    Subscription *sub = m_subsBySubId.value(subId, nullptr);
    if (!sub || !sub->m_sink) {
        return; // event for a subscription we've torn down
    }
    ViewSink *sink = sub->m_sink;

    if (event == QLatin1String("upsert")) {
        noteBatched(sink);
        sink->onUpsert(msg.value(QStringLiteral("sort")).toString(),
                       msg.value(QStringLiteral("item")).toObject());
    } else if (event == QLatin1String("remove")) {
        noteBatched(sink);
        sink->onRemove(msg.value(QStringLiteral("id")).toString());
    } else if (event == QLatin1String("ready")) {
        const bool has = msg.contains(QStringLiteral("exhausted"));
        sink->onReady(msg.value(QStringLiteral("exhausted")).toBool(), has);
    } else if (event == QLatin1String("reset")) {
        sink->onReset();
    }
}

void ProtocolClient::handleHelloReply(const QJsonObject &result, const ProtocolError &error)
{
    m_handshakeTimer->stop();
    if (error.isError()) {
        Q_EMIT errorOccurred(QStringLiteral("hello rejected: %1").arg(error.message));
        m_socket->abort();
        onSocketDisconnected();
        return;
    }
    m_serverInfo = result.toVariantMap();
    m_state = State::Ready;

    // Re-issue every live subscription (fresh window, sink was reset on drop),
    // then flush any commands queued while we were connecting.
    for (Subscription *sub : std::as_const(m_subscriptions)) {
        sendSubscribe(sub);
    }
    flushPending();
    Q_EMIT ready();
}

void ProtocolClient::sendObject(const QJsonObject &obj)
{
    if (m_socket->state() != QLocalSocket::ConnectedState) {
        return;
    }
    m_socket->write(QJsonDocument(obj).toJson(QJsonDocument::Compact) + '\n');
}

int ProtocolClient::sendRequest(const QString &method, const QJsonObject &params, ResponseCallback callback)
{
    const int id = m_nextId++;
    if (callback && m_pending.size() >= kMaxInFlightRequests) {
        // A daemon that accepts requests and answers none would otherwise have
        // us hold every callback for ever. Refuse the new one rather than drop
        // an older one still waiting for a reply that may yet arrive, and answer
        // it on the next turn so the caller is never re-entered from its own
        // call into request().
        failLater(std::move(callback), QStringLiteral("io"),
                  QStringLiteral("too many requests are already awaiting the daemon"));
        return id;
    }
    if (callback) {
        m_pending.insert(id, std::move(callback));
    }
    sendObject(QJsonObject{
        {QStringLiteral("id"), id},
        {QStringLiteral("method"), method},
        {QStringLiteral("params"), params},
    });
    return id;
}

int ProtocolClient::request(const QString &method, const QJsonObject &params, ResponseCallback callback)
{
    if (m_state != State::Ready) {
        // Queue until hello lands. Reserve the id now so the return value is
        // stable and callers can correlate before the wire send.
        const int id = m_nextId++;
        if (m_preHelloQueue.size() >= kMaxQueuedRequests) {
            // The daemon has been unreachable long enough that the queue is
            // full. The oldest entry is the least likely to still matter (a
            // presence tick, a superseded session update), so it gives way, but
            // its caller is still told, because a callback that never fires is
            // a caller that waits for ever.
            QueuedRequest oldest = m_preHelloQueue.takeFirst();
            if (oldest.callback) {
                failLater(std::move(oldest.callback), QStringLiteral("io"),
                          QStringLiteral("request dropped while the daemon was unreachable"));
            }
        }
        m_preHelloQueue.append(QueuedRequest{method, params, std::move(callback), id});
        return id;
    }
    return sendRequest(method, params, std::move(callback));
}

void ProtocolClient::flushPending()
{
    const QList<QueuedRequest> queue = std::move(m_preHelloQueue);
    m_preHelloQueue.clear();
    for (const QueuedRequest &req : queue) {
        if (req.callback) {
            m_pending.insert(req.id, req.callback);
        }
        sendObject(QJsonObject{
            {QStringLiteral("id"), req.id},
            {QStringLiteral("method"), req.method},
            {QStringLiteral("params"), req.params},
        });
    }
}

// Answers a caller on the next event-loop turn. Used where the failure is
// decided inside the caller's own request() call: firing there would re-enter
// it before it has its request id back.
void ProtocolClient::failLater(ResponseCallback callback, const QString &code, const QString &message)
{
    if (!callback) {
        return;
    }
    QTimer::singleShot(0, this, [callback = std::move(callback), code, message] {
        callback({}, ProtocolError{code, message});
    });
}

void ProtocolClient::failAllPending(const QString &code, const QString &message)
{
    const QHash<int, ResponseCallback> pending = std::move(m_pending);
    m_pending.clear();
    const ProtocolError error{code, message};
    for (const ResponseCallback &callback : pending) {
        if (callback) {
            callback({}, error);
        }
    }

    const QList<QueuedRequest> queue = std::move(m_preHelloQueue);
    m_preHelloQueue.clear();
    for (const QueuedRequest &req : queue) {
        if (req.callback) {
            req.callback({}, error);
        }
    }
}

Subscription *ProtocolClient::subscribe(const QString &view, const QJsonObject &params, ViewSink *sink)
{
    auto *sub = new Subscription(this, view, params, sink);
    m_subscriptions.append(sub);
    if (m_state == State::Ready) {
        sendSubscribe(sub);
    }
    return sub;
}

void ProtocolClient::sendSubscribe(Subscription *sub)
{
    QJsonObject params = sub->m_params;
    params.insert(QStringLiteral("view"), sub->m_view);
    const QPointer<Subscription> guardedSub(sub);
    sendRequest(QStringLiteral("subscribe"), params,
                [this, guardedSub](const QJsonObject &result, const ProtocolError &error) {
                    // A replaced subscription can be allocated at the same raw
                    // address. QPointer tracks the original QObject identity.
                    if (!guardedSub) {
                        const int staleSubId = result.value(QStringLiteral("sub")).toInt(-1);
                        if (!error.isError() && staleSubId >= 0) {
                            sendRequest(QStringLiteral("unsubscribe"),
                                        QJsonObject{{QStringLiteral("sub"), staleSubId}}, {});
                        }
                        return;
                    }
                    Subscription *sub = guardedSub.data();
                    if (error.isError()) {
                        Q_EMIT sub->failed(error.code, error.message);
                        return;
                    }
                    sub->m_subId = result.value(QStringLiteral("sub")).toInt(-1);
                    QVariantMap meta = result.toVariantMap();
                    meta.remove(QStringLiteral("sub"));
                    sub->m_meta = meta;
                    if (sub->m_subId >= 0) {
                        m_subsBySubId.insert(sub->m_subId, sub);
                    }
                    // Flush any extends issued before the sub id was known.
                    const auto pending = sub->m_pendingExtends;
                    sub->m_pendingExtends.clear();
                    for (const auto &ext : pending) {
                        sendExtend(sub, ext.count, ext.direction);
                    }
                    Q_EMIT sub->subscribed(meta);
                });
}

void ProtocolClient::sendExtend(Subscription *sub, int count, const QString &direction)
{
    if (m_state != State::Ready || !sub || sub->m_subId < 0) {
        return;
    }
    const QPointer<Subscription> guardedSub(sub);
    sendRequest(QStringLiteral("extend"),
                 QJsonObject{
                     {QStringLiteral("sub"), sub->m_subId},
                     {QStringLiteral("count"), count},
                     {QStringLiteral("direction"), direction},
                 },
                 [guardedSub](const QJsonObject &, const ProtocolError &error) {
                     if (error.isError() && guardedSub) {
                         Q_EMIT guardedSub->extendFailed(error.code, error.message);
                     }
                 });
}

void ProtocolClient::sendUnsubscribe(Subscription *sub)
{
    if (m_state == State::Ready && sub->m_subId >= 0) {
        sendRequest(QStringLiteral("unsubscribe"),
                    QJsonObject{{QStringLiteral("sub"), sub->m_subId}}, {});
    }
    if (sub->m_subId >= 0) {
        m_subsBySubId.remove(sub->m_subId);
    }
}

void ProtocolClient::removeSubscription(Subscription *sub)
{
    m_subscriptions.removeAll(sub);
    if (sub->m_subId >= 0) {
        m_subsBySubId.remove(sub->m_subId);
    }
    sub->m_subId = -1;
}

} // namespace whatevr::proto
