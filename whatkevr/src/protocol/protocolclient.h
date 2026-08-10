#pragma once

#include <QByteArray>
#include <QHash>
#include <QJsonObject>
#include <QList>
#include <QObject>
#include <QString>
#include <QVariantMap>

#include <cstdint>
#include <functional>

QT_BEGIN_NAMESPACE
class QLocalSocket;
class QTimer;
QT_END_NAMESPACE

namespace whatevr::proto
{

// The one wire protocol version this client speaks (PROTOCOL.md, "Hello").
inline constexpr int kProtocolVersion = 1;

// An error envelope carried by a command/query response or a failed subscribe.
// `code` is the stable machine-readable string (PROTOCOL.md "Errors"); an empty
// `code` means "no error" so a single value can answer "did this fail?".
struct ProtocolError {
    QString code;
    QString message;

    [[nodiscard]] bool isError() const { return !code.isEmpty(); }
};

// The sink a view subscription pushes its updates into. This is the entire
// contract a view model implements — the four events of PROTOCOL.md's view
// grammar. The client never interprets item contents; sinks apply the
// universal algorithm (keyed upserts ordered by the opaque `sort`).
class ViewSink
{
public:
    virtual ~ViewSink() = default;

    // Insert or replace the item with this `item["id"]`, positioned by `sort`.
    virtual void onUpsert(const QString &sort, const QJsonObject &item) = 0;
    // Delete the item with this id.
    virtual void onRemove(const QString &id) = 0;
    // The window is fully populated for the latest subscribe/extend.
    // `hasExhausted` is false when the daemon omitted the flag.
    virtual void onReady(bool exhausted, bool hasExhausted) = 0;
    // Discard the local copy; fresh upserts (then a `ready`) follow.
    virtual void onReset() = 0;
    // Bracket a run of events delivered together — one drain of the socket.
    // Between them a sink may buffer and apply the run as a single
    // transaction: a view fill arrives as one event per item, and applying
    // eighty of them as eighty separate model changes made opening a chat cost
    // eighty layout passes. Outside a bracket, events apply as they arrive, so
    // a sink driven directly behaves exactly as it did before. Default is a
    // no-op for sinks that always apply eagerly.
    virtual void onBatchBegin() {}
    virtual void onBatchEnd() {}
};

class ProtocolClient;

// A live subscription to a single view. Created via ProtocolClient::subscribe,
// it owns the daemon-assigned `sub` id, routes the view's events to its sink,
// and re-issues itself automatically across reconnects (the sink sees an
// onReset() then a fresh fill). Destroying it (or calling unsubscribe) tears
// the subscription down.
class Subscription final : public QObject
{
    Q_OBJECT

public:
    ~Subscription() override;

    // Grow the window. `direction` is "older" or "newer" (PROTOCOL.md
    // "Windows"); the daemon rejects a nonsensical direction. Calls made
    // before the subscribe response lands are queued and flushed on subscribe.
    void extend(int count, const QString &direction);
    // Tear down the subscription (also happens on destruction).
    void unsubscribe();

    [[nodiscard]] bool isActive() const { return m_subId >= 0; }
    [[nodiscard]] const QVariantMap &meta() const { return m_meta; }

Q_SIGNALS:
    // The subscribe request succeeded; `meta` carries any view-specific
    // subscribe metadata (e.g. `anchor_id`).
    void subscribed(const QVariantMap &meta);
    // The subscribe request was rejected.
    void failed(const QString &code, const QString &message);
    // An extend request was rejected. View completion still arrives through
    // `ready`; this signal exists only so callers can clear in-flight UI state.
    void extendFailed(const QString &code, const QString &message);

private:
    friend class ProtocolClient;

    Subscription(ProtocolClient *client, QString view, QJsonObject params, ViewSink *sink);

    struct PendingExtend {
        int count;
        QString direction;
    };

    ProtocolClient *m_client;
    QString m_view;
    QJsonObject m_params;
    ViewSink *m_sink;
    QVariantMap m_meta;
    int m_subId = -1;
    QList<PendingExtend> m_pendingExtends;
};

// The transport and dispatcher for the whatevr protocol: one QLocalSocket to
// the daemon, NDJSON framing, the `hello` handshake, request/response
// correlation by `id`, and view-event routing by `sub`. It holds no view
// state; models subscribe through it. Auto-reconnects and re-subscribes.
class ProtocolClient final : public QObject
{
    Q_OBJECT

public:
    using ResponseCallback = std::function<void(const QJsonObject &result, const ProtocolError &error)>;

    explicit ProtocolClient(QString socketPath, QString clientName, QObject *parent = nullptr);
    ~ProtocolClient() override;

    // Begin connecting (idempotent). Reconnection is automatic afterwards.
    void start();
    // Stop reconnecting and close the socket.
    void stop();

    [[nodiscard]] bool isReady() const { return m_state == State::Ready; }
    [[nodiscard]] QVariantMap serverInfo() const { return m_serverInfo; }

    // Send a command/query. The callback fires exactly once with either a
    // result or an error. Requests issued before the connection is ready are
    // queued and flushed after `hello`. Returns the request id.
    int request(const QString &method, const QJsonObject &params, ResponseCallback callback = {});

    // Subscribe to a view. The returned Subscription is parented to the client;
    // its sink must outlive it (or the subscription must be destroyed first).
    Subscription *subscribe(const QString &view, const QJsonObject &params, ViewSink *sink);

Q_SIGNALS:
    // The connection is up and `hello` succeeded; requests now flow.
    void ready();
    // The connection dropped (a reconnect is scheduled unless stopped).
    void disconnected();
    // A transport/handshake failure worth surfacing (human-readable).
    void errorOccurred(const QString &message);
    // A connection-directed `open_chat` event (notification click / URL).
    void openChatRequested(const QString &chatId);

private:
    friend class Subscription;

    enum class State : std::uint8_t {
        Idle, // stopped; no reconnect
        Connecting, // socket connecting
        Handshaking, // socket up, hello sent, awaiting reply
        Ready, // hello acknowledged
    };

    void onSocketConnected();
    void onSocketDisconnected();
    void onReadyRead();
    void dispatchLine(const QByteArray &line);
    void handleResponse(const QJsonObject &msg);
    void handleEvent(const QJsonObject &msg);
    void noteBatched(ViewSink *sink);
    void handleHelloReply(const QJsonObject &result, const ProtocolError &error);

    void sendObject(const QJsonObject &obj);
    int sendRequest(const QString &method, const QJsonObject &params, ResponseCallback callback);
    void flushPending();
    void scheduleReconnect();
    void failAllPending(const QString &code, const QString &message);

    // Subscription plumbing (called by Subscription).
    void sendSubscribe(Subscription *sub);
    void sendExtend(Subscription *sub, int count, const QString &direction);
    void sendUnsubscribe(Subscription *sub);
    void removeSubscription(Subscription *sub);

    struct QueuedRequest {
        QString method;
        QJsonObject params;
        ResponseCallback callback;
        int id;
    };

    QString m_socketPath;
    QString m_clientName;
    QLocalSocket *m_socket;
    QTimer *m_reconnectTimer;
    State m_state = State::Idle;
    bool m_running = false;
    int m_nextId = 1;
    QByteArray m_readBuffer;
    QVariantMap m_serverInfo;

    QHash<int, ResponseCallback> m_pending; // by request id
    QList<QueuedRequest> m_preHelloQueue; // requests made before Ready
    QHash<int, Subscription *> m_subsBySubId; // by daemon-assigned sub id
    QList<Subscription *> m_subscriptions; // all live subscriptions
    // Sinks with an open batch, closed at the end of the current socket drain.
    QList<ViewSink *> m_batchedSinks;
};

} // namespace whatevr::proto
