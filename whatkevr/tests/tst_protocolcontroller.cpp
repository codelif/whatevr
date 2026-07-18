// Unit tests for ProtocolController (D2a): the whatevr-protocol connection
// lifecycle that drives the status/login/splash screens and the shell gate.
// Exercised end-to-end against an in-process fake daemon serving the
// `connection` and `login` object views over a real Unix socket. No GUI, no
// gRPC, no daemon binary.

#include <QCoreApplication>
#include <QDateTime>
#include <QJsonDocument>
#include <QJsonObject>
#include <QLocalServer>
#include <QLocalSocket>
#include <QSignalSpy>
#include <QTemporaryDir>
#include <QTest>

#include <utility>

#include "collectionviewmodel.h"
#include "protocolcontroller.h"
#include "protocolmessagemodel.h"

using whatevr::proto::CollectionViewModel;

namespace
{
// A fake daemon that serves the two object views ProtocolController subscribes
// to. The test seeds the current connection/login items; the fake replays them
// on subscribe (response → upsert → ready) and can push live updates after.
class FakeDaemon : public QObject
{
    Q_OBJECT

public:
    explicit FakeDaemon(QString path, QObject *parent = nullptr)
        : QObject(parent)
        , m_server(new QLocalServer(this))
    {
        QLocalServer::removeServer(path);
        m_server->listen(path);
        connect(m_server, &QLocalServer::newConnection, this, [this] {
            m_conn = m_server->nextPendingConnection();
            connect(m_conn, &QLocalSocket::readyRead, this, &FakeDaemon::onReadyRead);
            connect(m_conn, &QLocalSocket::disconnected, this, [this] {
                m_subByView.clear();
                m_conn = nullptr;
            });
        });
    }

    // Seed / update a view's single object item. If already subscribed, the new
    // item is delivered live as an upsert.
    void setItem(const QString &view, const QJsonObject &item)
    {
        m_items.insert(view, item);
        const int sub = m_subByView.value(view, -1);
        if (sub >= 0) {
            sendUpsert(sub, item);
        }
    }

    // Seed a collection view's rows (each row carries its own "id"; the row's
    // "sort" field, or its index, orders it). Replayed on the next subscribe.
    void setCollection(const QString &view, const QList<QJsonObject> &rows)
    {
        m_collections.insert(view, rows);
    }

    // The `chats` view is served per the subscribe `archived` param: active and
    // archived are two disjoint subscriptions of the same view.
    void setActiveChats(const QList<QJsonObject> &rows) { m_chatsActive = rows; }
    void setArchivedChats(const QList<QJsonObject> &rows) { m_chatsArchived = rows; }
    void setMessages(const QList<QJsonObject> &rows) { m_messages = rows; }
    void setUnreadAnchorId(const QString &id) { m_unreadAnchorId = id; }
    void setInitialMessagesExhausted(bool exhausted) { m_initialMessagesExhausted = exhausted; }
    void setNextExtendExhausted(bool exhausted) { m_nextExtendExhausted = exhausted; }
    void setHoldMessagesReady(bool hold) { m_holdMessagesReady = hold; }
    void setHoldExtendReady(bool hold) { m_holdExtendReady = hold; }
    void setRejectNextExtend(bool reject) { m_rejectNextExtend = reject; }

    void sendOpenChat(const QString &chatId)
    {
        writeObject(QJsonObject{{QStringLiteral("event"), QStringLiteral("open_chat")},
                                {QStringLiteral("chat_id"), chatId}});
    }

    void resetMessages()
    {
        const int sub = m_subByView.value(QStringLiteral("messages"), -1);
        if (sub >= 0) {
            writeObject(QJsonObject{{QStringLiteral("sub"), sub},
                                    {QStringLiteral("event"), QStringLiteral("reset")}});
        }
    }
    void pushMessage(const QJsonObject &item, const QString &sort)
    {
        const int sub = m_subByView.value(QStringLiteral("messages"), -1);
        if (sub >= 0) {
            sendUpsert(sub, item, sort);
        }
    }
    void readyMessages(bool exhausted)
    {
        const int sub = m_subByView.value(QStringLiteral("messages"), -1);
        if (sub >= 0) {
            sendReady(sub, true, exhausted);
        }
    }
    void releaseExtendReady()
    {
        if (m_heldExtendSub >= 0) {
            sendReady(std::exchange(m_heldExtendSub, -1), true, m_heldExtendExhausted);
        }
    }

    // Push a live collection update to whichever sub currently owns `view`.
    void pushUpsert(const QString &view, const QJsonObject &item, const QString &sort)
    {
        const int sub = m_subByView.value(view, -1);
        if (sub >= 0) {
            sendUpsert(sub, item, sort);
        }
    }
    void pushRemove(const QString &view, const QString &id)
    {
        const int sub = m_subByView.value(view, -1);
        if (sub >= 0) {
            writeObject(QJsonObject{{QStringLiteral("sub"), sub},
                                    {QStringLiteral("event"), QStringLiteral("remove")},
                                    {QStringLiteral("id"), id}});
        }
    }

    int reconnectCount = 0;
    // Params of the most recent `chats` subscribe, and how many landed — lets a
    // test assert the filter re-subscribe without racing the reply.
    QJsonObject lastChatsParams;
    int chatsSubscribeCount = 0;
    // The most recent chat.* command the controller sent.
    QString lastCommandMethod;
    QJsonObject lastCommandParams;
    QJsonObject lastMessagesParams;
    QJsonObject lastExtendParams;
    QJsonObject lastSessionParams;
    QJsonObject lastMarkReadParams;
    int messagesSubscribeCount = 0;
    int extendCount = 0;

Q_SIGNALS:
    void reconnectRequested();
    void chatsSubscribed();
    void commandReceived();
    void messagesSubscribed();
    void extended();
    void sessionUpdated();
    void markReadReceived();

private:
    void onReadyRead()
    {
        m_buf += m_conn->readAll();
        int nl = m_buf.indexOf('\n');
        while (nl >= 0) {
            const QByteArray line = m_buf.left(nl);
            m_buf.remove(0, nl + 1);
            handleLine(line);
            nl = m_buf.indexOf('\n');
        }
    }

    void handleLine(const QByteArray &line)
    {
        const QJsonObject msg = QJsonDocument::fromJson(line).object();
        const int id = msg.value(QStringLiteral("id")).toInt();
        const QString method = msg.value(QStringLiteral("method")).toString();
        const QJsonObject params = msg.value(QStringLiteral("params")).toObject();

        if (method == QLatin1String("hello")) {
            reply(id, QJsonObject{
                          {QStringLiteral("daemon"), QStringLiteral("whatevrd")},
                          {QStringLiteral("version"), QStringLiteral("0.7.0")},
                          {QStringLiteral("protocol"), 1},
                      });
        } else if (method == QLatin1String("subscribe")) {
            const QString view = params.value(QStringLiteral("view")).toString();
            const int sub = ++m_subCount;
            m_subByView.insert(view, sub);
            if (view == QLatin1String("messages")
                && params.value(QStringLiteral("anchor")).toString() == QLatin1String("not-found")) {
                error(id, QStringLiteral("not_found"), QStringLiteral("message not found"));
                return;
            }
            QJsonObject result{{QStringLiteral("sub"), sub}};
            if (view == QLatin1String("messages")) {
                const QString anchor = params.value(QStringLiteral("anchor")).toString();
                if (anchor == QLatin1String("unread") && !m_unreadAnchorId.isEmpty()) {
                    result.insert(QStringLiteral("anchor_id"), m_unreadAnchorId);
                } else if (anchor != QLatin1String("latest") && anchor != QLatin1String("unread")) {
                    result.insert(QStringLiteral("anchor_id"), anchor);
                }
            }
            reply(id, result);
            if (m_items.contains(view)) {
                sendUpsert(sub, m_items.value(view));
            }
            QList<QJsonObject> rows;
            bool hasRows = false;
            if (view == QLatin1String("chats")) {
                rows = params.value(QStringLiteral("archived")).toBool() ? m_chatsArchived : m_chatsActive;
                hasRows = true;
            } else if (view == QLatin1String("messages")) {
                rows = m_messages;
                hasRows = true;
            } else if (m_collections.contains(view)) {
                rows = m_collections.value(view);
                hasRows = true;
            }
            if (hasRows) {
                for (int i = 0; i < rows.size(); ++i) {
                    const QJsonObject &row = rows.at(i);
                    const QString sort = row.value(QStringLiteral("sort")).toString(
                        QStringLiteral("%1").arg(i, 4, 10, QLatin1Char('0')));
                    sendUpsert(sub, row, sort);
                }
            }
            if (view != QLatin1String("messages") || !m_holdMessagesReady) {
                sendReady(sub, view == QLatin1String("messages"), m_initialMessagesExhausted);
            }
            if (view == QLatin1String("chats")) {
                lastChatsParams = params;
                ++chatsSubscribeCount;
                Q_EMIT chatsSubscribed();
            } else if (view == QLatin1String("messages")) {
                lastMessagesParams = params;
                ++messagesSubscribeCount;
                Q_EMIT messagesSubscribed();
            }
        } else if (method == QLatin1String("extend")) {
            lastExtendParams = params;
            ++extendCount;
            if (std::exchange(m_rejectNextExtend, false)) {
                error(id, QStringLiteral("invalid_params"), QStringLiteral("extend rejected"));
                Q_EMIT extended();
                return;
            }
            reply(id, QJsonObject{});
            if (m_holdExtendReady) {
                m_heldExtendSub = params.value(QStringLiteral("sub")).toInt();
                m_heldExtendExhausted = m_nextExtendExhausted;
            } else {
                sendReady(params.value(QStringLiteral("sub")).toInt(), true, m_nextExtendExhausted);
            }
            Q_EMIT extended();
        } else if (method == QLatin1String("unsubscribe")) {
            reply(id, QJsonObject{});
        } else if (method == QLatin1String("session.update")) {
            lastSessionParams = params;
            reply(id, QJsonObject{});
            Q_EMIT sessionUpdated();
        } else if (method == QLatin1String("chat.mark_read")) {
            lastMarkReadParams = params;
            reply(id, QJsonObject{});
            Q_EMIT markReadReceived();
        } else if (method == QLatin1String("chat.request_older")) {
            lastCommandMethod = method;
            lastCommandParams = params;
            reply(id, QJsonObject{{QStringLiteral("requested"), true}});
            Q_EMIT commandReceived();
        } else if (method == QLatin1String("daemon.reconnect")) {
            ++reconnectCount;
            reply(id, QJsonObject{});
            Q_EMIT reconnectRequested();
        } else if (method.startsWith(QLatin1String("chat."))) {
            lastCommandMethod = method;
            lastCommandParams = params;
            reply(id, QJsonObject{});
            Q_EMIT commandReceived();
        } else {
            reply(id, QJsonObject{});
        }
    }

    void sendUpsert(int sub, const QJsonObject &item, const QString &sort = QStringLiteral("0"))
    {
        writeObject(QJsonObject{
            {QStringLiteral("sub"), sub},
            {QStringLiteral("event"), QStringLiteral("upsert")},
            {QStringLiteral("sort"), sort},
            {QStringLiteral("item"), item},
        });
    }

    void sendReady(int sub, bool includeExhausted = false, bool exhausted = false)
    {
        QJsonObject ready{
            {QStringLiteral("sub"), sub},
            {QStringLiteral("event"), QStringLiteral("ready")},
        };
        if (includeExhausted) {
            ready.insert(QStringLiteral("exhausted"), exhausted);
        }
        writeObject(ready);
    }

    void reply(int id, const QJsonObject &result)
    {
        writeObject(QJsonObject{{QStringLiteral("id"), id}, {QStringLiteral("result"), result}});
    }

    void error(int id, const QString &code, const QString &message)
    {
        writeObject(QJsonObject{{QStringLiteral("id"), id},
                                {QStringLiteral("error"), QJsonObject{
                                     {QStringLiteral("code"), code},
                                     {QStringLiteral("message"), message},
                                 }}});
    }

    void writeObject(const QJsonObject &obj)
    {
        if (m_conn && m_conn->state() == QLocalSocket::ConnectedState) {
            m_conn->write(QJsonDocument(obj).toJson(QJsonDocument::Compact) + '\n');
        }
    }

    QLocalServer *m_server;
    QLocalSocket *m_conn = nullptr;
    QByteArray m_buf;
    int m_subCount = 0;
    QHash<QString, int> m_subByView;
    QHash<QString, QJsonObject> m_items;
    QHash<QString, QList<QJsonObject>> m_collections;
    QList<QJsonObject> m_chatsActive;
    QList<QJsonObject> m_chatsArchived;
    QList<QJsonObject> m_messages;
    QString m_unreadAnchorId;
    bool m_initialMessagesExhausted = false;
    bool m_nextExtendExhausted = false;
    bool m_holdMessagesReady = false;
    bool m_holdExtendReady = false;
    bool m_heldExtendExhausted = false;
    int m_heldExtendSub = -1;
    bool m_rejectNextExtend = false;
};

QJsonObject chatRow(const QString &id, const QString &name, const QString &sort,
                    const QJsonObject &extra = {})
{
    QJsonObject row{
        {QStringLiteral("id"), id},
        {QStringLiteral("name"), name},
        {QStringLiteral("sort"), sort},
    };
    for (auto it = extra.begin(); it != extra.end(); ++it) {
        row.insert(it.key(), it.value());
    }
    return row;
}

QJsonObject connectionItem(const QString &state, bool canReconnect = false)
{
    return QJsonObject{
        {QStringLiteral("id"), QStringLiteral("self")},
        {QStringLiteral("state"), state},
        {QStringLiteral("can_reconnect"), canReconnect},
    };
}

QJsonObject loginQrItem(const QString &code, int expiresInSecs)
{
    const QString expiresAt =
        QDateTime::currentDateTimeUtc().addSecs(expiresInSecs).toString(Qt::ISODateWithMs);
    return QJsonObject{
        {QStringLiteral("id"), QStringLiteral("self")},
        {QStringLiteral("state"), QStringLiteral("need_login")},
        {QStringLiteral("qr"), QJsonObject{{QStringLiteral("code"), code},
                                           {QStringLiteral("expires_at"), expiresAt}}},
    };
}

QJsonObject messageRow(const QString &id, const QString &sort)
{
    return QJsonObject{
        {QStringLiteral("id"), id},
        {QStringLiteral("chat_id"), QStringLiteral("a@s" )},
        {QStringLiteral("kind"), QStringLiteral("text")},
        {QStringLiteral("fallback"), id},
        {QStringLiteral("text"), id},
        {QStringLiteral("sender"), QJsonObject{{QStringLiteral("id"), QStringLiteral("a@s")},
                                                {QStringLiteral("name"), QStringLiteral("Alice")}}},
        {QStringLiteral("timestamp"), 1'700'000'000},
        {QStringLiteral("direction"), QStringLiteral("incoming")},
        {QStringLiteral("status"), QStringLiteral("delivered")},
        {QStringLiteral("sort"), sort},
    };
}
} // namespace

class TestProtocolController : public QObject
{
    Q_OBJECT

private Q_SLOTS:
    void init()
    {
        m_dir = new QTemporaryDir;
        m_path = m_dir->filePath(QStringLiteral("whatevrd.sock"));
    }

    void cleanup()
    {
        delete m_dir;
        m_dir = nullptr;
    }

    // No daemon behind the socket: after the cold-start grace elapses, the
    // controller reports "not running" (and stops flashing the splash).
    void notRunningAfterGrace()
    {
        ProtocolController ctrl(m_path, nullptr);
        ctrl.start();
        QVERIFY(ctrl.starting()); // in the grace window, splash

        QTRY_VERIFY_WITH_TIMEOUT(!ctrl.starting(), 3000);
        QVERIFY(!ctrl.daemonRunning());
        QVERIFY(!ctrl.shellVisible());
        QVERIFY(!ctrl.loginRequired());
        QCOMPARE(ctrl.connectionPhase(), QStringLiteral("not-running"));
        // Retry is offered when there's no live connection.
        QCOMPARE(ctrl.primaryActionText(), QStringLiteral("Retry"));
    }

    // An online daemon: the shell becomes visible (chat mode), phase connected.
    void onlineShowsShell()
    {
        FakeDaemon daemon(m_path);
        daemon.setItem(QStringLiteral("connection"), connectionItem(QStringLiteral("online")));

        ProtocolController ctrl(m_path, nullptr);
        ctrl.start();

        QTRY_VERIFY(ctrl.shellVisible());
        QVERIFY(!ctrl.starting());
        QVERIFY(!ctrl.loginRequired());
        QVERIFY(ctrl.daemonRunning());
        QCOMPARE(ctrl.connectionPhase(), QStringLiteral("connected"));
    }

    // A logged-out daemon: the login gate trips and the QR (with countdown) is
    // exposed from the login view.
    void needLoginShowsQr()
    {
        FakeDaemon daemon(m_path);
        daemon.setItem(QStringLiteral("connection"), connectionItem(QStringLiteral("need_login")));
        daemon.setItem(QStringLiteral("login"), loginQrItem(QStringLiteral("QR-PAYLOAD"), 60));

        ProtocolController ctrl(m_path, nullptr);
        ctrl.start();

        QTRY_VERIFY(ctrl.loginRequired());
        QVERIFY(!ctrl.shellVisible());
        QVERIFY(ctrl.qrAvailable());
        QCOMPARE(ctrl.qrCode(), QStringLiteral("QR-PAYLOAD"));
        QVERIFY(!ctrl.qrExpiryText().isEmpty());
        QCOMPARE(ctrl.statusTitle(), QStringLiteral("Scan to sign in"));
    }

    // A live connection-view update flips the gate without any resubscribe: the
    // daemon going from online to logged-out routes the app to the login page.
    void liveStateTransitionFlipsGate()
    {
        FakeDaemon daemon(m_path);
        daemon.setItem(QStringLiteral("connection"), connectionItem(QStringLiteral("online")));

        ProtocolController ctrl(m_path, nullptr);
        ctrl.start();
        QTRY_VERIFY(ctrl.shellVisible());

        QSignalSpy spy(&ctrl, &ProtocolController::stateChanged);
        daemon.setItem(QStringLiteral("connection"), connectionItem(QStringLiteral("need_login")));
        daemon.setItem(QStringLiteral("login"), loginQrItem(QStringLiteral("QR2"), 60));

        QTRY_VERIFY(ctrl.loginRequired());
        QVERIFY(!ctrl.shellVisible());
        QVERIFY(spy.count() > 0);
    }

    // With a reconnectable live connection the primary action is Reconnect, and
    // triggering it sends the daemon.reconnect command (button disabled while in
    // flight, re-enabled after the ack).
    void reconnectSendsCommand()
    {
        FakeDaemon daemon(m_path);
        daemon.setItem(QStringLiteral("connection"), connectionItem(QStringLiteral("online"), true));

        ProtocolController ctrl(m_path, nullptr);
        ctrl.start();
        QTRY_COMPARE(ctrl.primaryActionText(), QStringLiteral("Reconnect"));
        QVERIFY(ctrl.primaryActionEnabled());

        QSignalSpy reconnectSpy(&daemon, &FakeDaemon::reconnectRequested);
        ctrl.triggerPrimaryAction();
        QVERIFY(!ctrl.primaryActionEnabled()); // in flight

        QVERIFY(reconnectSpy.wait());
        QCOMPARE(daemon.reconnectCount, 1);
        QTRY_VERIFY(ctrl.primaryActionEnabled()); // ack re-enables
    }

    // The chat list fills from the `chats` collection view: rows land as keyed
    // upserts ordered by the daemon `sort`, and loading/empty track ready+count.
    void chatsFillAndReady()
    {
        FakeDaemon daemon(m_path);
        daemon.setItem(QStringLiteral("connection"), connectionItem(QStringLiteral("online")));
        daemon.setActiveChats(
                             {chatRow(QStringLiteral("a@s"), QStringLiteral("Alice"), QStringLiteral("1-000")),
                              chatRow(QStringLiteral("b@s"), QStringLiteral("Bob"), QStringLiteral("1-001"))});

        ProtocolController ctrl(m_path, nullptr);
        ctrl.start();
        QVERIFY(ctrl.chatsLoading()); // not ready before the first window lands

        auto *model = qobject_cast<CollectionViewModel *>(ctrl.chatsModel());
        QVERIFY(model);
        QTRY_COMPARE(model->count(), 2);
        QVERIFY(!ctrl.chatsLoading());
        QVERIFY(!ctrl.chatsEmpty());
        // Ordered by the opaque sort; fields are the daemon row's own.
        QCOMPARE(model->indexOfId(QStringLiteral("a@s")), 0);
        QCOMPARE(model->indexOfId(QStringLiteral("b@s")), 1);
        QCOMPARE(model->itemById(QStringLiteral("a@s")).value(QStringLiteral("name")).toString(),
                 QStringLiteral("Alice"));
    }

    // Changing the filter re-subscribes with the new `filter` param (daemon-side
    // filtering) rather than narrowing the list in the frontend.
    void chatFilterResubscribes()
    {
        FakeDaemon daemon(m_path);
        daemon.setItem(QStringLiteral("connection"), connectionItem(QStringLiteral("online")));
        daemon.setActiveChats(
                             {chatRow(QStringLiteral("a@s"), QStringLiteral("Alice"), QStringLiteral("1-000"))});

        ProtocolController ctrl(m_path, nullptr);
        ctrl.start();
        // Two `chats` subscribes per filter: active (archived:false) + archived.
        QTRY_COMPARE(daemon.chatsSubscribeCount, 2);
        QCOMPARE(daemon.lastChatsParams.value(QStringLiteral("filter")).toString(), QStringLiteral("all"));

        ctrl.setChatFilter(2); // groups → both subscriptions re-issued
        QTRY_COMPARE(daemon.chatsSubscribeCount, 4);
        QCOMPARE(daemon.lastChatsParams.value(QStringLiteral("filter")).toString(), QStringLiteral("groups"));
        QCOMPARE(ctrl.chatFilter(), 2);
    }

    // A list command maps to the daemon `chat.*` command (ack only); no local
    // state — the row change would arrive back through the `chats` view.
    void chatCommandMapsToDaemon()
    {
        FakeDaemon daemon(m_path);
        daemon.setItem(QStringLiteral("connection"), connectionItem(QStringLiteral("online")));

        ProtocolController ctrl(m_path, nullptr);
        ctrl.start();
        QTRY_VERIFY(ctrl.shellVisible());

        QSignalSpy commandSpy(&daemon, &FakeDaemon::commandReceived);
        ctrl.setChatPinned(QStringLiteral("a@s"), true);
        QVERIFY(commandSpy.wait());
        QCOMPARE(daemon.lastCommandMethod, QStringLiteral("chat.pin"));
        QCOMPARE(daemon.lastCommandParams.value(QStringLiteral("chat_id")).toString(), QStringLiteral("a@s"));
        QCOMPARE(daemon.lastCommandParams.value(QStringLiteral("pinned")).toBool(), true);
    }

    // Archived chats are a *separate* `chats` subscription (`archived: true`) with
    // its own model and count; the active model never contains archived rows.
    void archivedPopulatesSeparateModel()
    {
        FakeDaemon daemon(m_path);
        daemon.setItem(QStringLiteral("connection"), connectionItem(QStringLiteral("online")));
        daemon.setActiveChats({chatRow(QStringLiteral("a@s"), QStringLiteral("Alice"), QStringLiteral("1-000"))});
        daemon.setArchivedChats({chatRow(QStringLiteral("z@s"), QStringLiteral("Zed"), QStringLiteral("1-000")),
                                 chatRow(QStringLiteral("y@s"), QStringLiteral("Yara"), QStringLiteral("1-001"))});

        ProtocolController ctrl(m_path, nullptr);
        ctrl.start();

        auto *active = qobject_cast<CollectionViewModel *>(ctrl.chatsModel());
        auto *archived = qobject_cast<CollectionViewModel *>(ctrl.archivedChatsModel());
        QVERIFY(active);
        QVERIFY(archived);
        QTRY_COMPARE(active->count(), 1);
        QTRY_COMPARE(archived->count(), 2);
        QCOMPARE(ctrl.archivedCount(), 2);
        QCOMPARE(active->indexOfId(QStringLiteral("z@s")), -1); // not in the active list
    }

    // The typing overlay tracks the global `typing` view keyed by chat_id; live
    // start/stop flips chatTyping() and bumps typingRevision for the row binding.
    void typingOverlayTracksView()
    {
        FakeDaemon daemon(m_path);
        daemon.setItem(QStringLiteral("connection"), connectionItem(QStringLiteral("online")));
        daemon.setCollection(QStringLiteral("typing"),
                             {QJsonObject{{QStringLiteral("id"), QStringLiteral("a@s")}}});

        ProtocolController ctrl(m_path, nullptr);
        ctrl.start();
        QTRY_VERIFY(ctrl.chatTyping(QStringLiteral("a@s")));
        QVERIFY(!ctrl.chatTyping(QStringLiteral("b@s")));

        QSignalSpy typingSpy(&ctrl, &ProtocolController::typingChanged);
        daemon.pushRemove(QStringLiteral("typing"), QStringLiteral("a@s")); // stopped typing
        QTRY_VERIFY(!ctrl.chatTyping(QStringLiteral("a@s")));
        QVERIFY(typingSpy.count() > 0);

        daemon.pushUpsert(QStringLiteral("typing"), QJsonObject{{QStringLiteral("id"), QStringLiteral("b@s")}},
                          QStringLiteral("0")); // someone else starts
        QTRY_VERIFY(ctrl.chatTyping(QStringLiteral("b@s")));
    }

    // The history-sync strip derives visible/percent/title/detail from the `sync`
    // object view; complete and on-demand states hide it.
    void historySyncStripDerives()
    {
        FakeDaemon daemon(m_path);
        daemon.setItem(QStringLiteral("connection"), connectionItem(QStringLiteral("online")));
        daemon.setItem(QStringLiteral("sync"), QJsonObject{
                                                   {QStringLiteral("id"), QStringLiteral("self")},
                                                   {QStringLiteral("type"), QStringLiteral("recent")},
                                                   {QStringLiteral("phase"), QStringLiteral("processing")},
                                                   {QStringLiteral("progress_percent"), 40},
                                                   {QStringLiteral("chunk_order"), 2},
                                                   {QStringLiteral("is_complete"), false},
                                               });

        ProtocolController ctrl(m_path, nullptr);
        ctrl.start();
        QTRY_VERIFY(ctrl.historySyncVisible());
        QCOMPARE(ctrl.historySyncPercent(), 40);
        QCOMPARE(ctrl.historySyncTitle(), QStringLiteral("Recent history sync"));
        QVERIFY(ctrl.historySyncDetail().contains(QStringLiteral("Chunk 2")));

        // Complete hides the strip.
        daemon.setItem(QStringLiteral("sync"), QJsonObject{{QStringLiteral("id"), QStringLiteral("self")},
                                                           {QStringLiteral("phase"), QStringLiteral("complete")},
                                                           {QStringLiteral("progress_percent"), 100},
                                                           {QStringLiteral("is_complete"), true}});
        QTRY_VERIFY(!ctrl.historySyncVisible());

        // On-demand (per-chat) history never drives the strip, even mid-progress.
        daemon.setItem(QStringLiteral("sync"), QJsonObject{{QStringLiteral("id"), QStringLiteral("self")},
                                                           {QStringLiteral("type"), QStringLiteral("on_demand")},
                                                           {QStringLiteral("phase"), QStringLiteral("processing")},
                                                           {QStringLiteral("progress_percent"), 50},
                                                           {QStringLiteral("is_complete"), false}});
        // Give the update a chance to land, then assert it stayed hidden.
        QTest::qWait(50);
        QVERIFY(!ctrl.historySyncVisible());
    }

    void latestConversationUsesProtocolTimelineAndSession()
    {
        FakeDaemon daemon(m_path);
        daemon.setItem(QStringLiteral("connection"), connectionItem(QStringLiteral("online")));
        daemon.setActiveChats({chatRow(QStringLiteral("a@s"), QStringLiteral("Alice"), QStringLiteral("1-000"),
                                       QJsonObject{{QStringLiteral("unread"), 0},
                                                   {QStringLiteral("avatar_path"), QStringLiteral("/alice.jpg")}})});
        daemon.setMessages({messageRow(QStringLiteral("m2"), QStringLiteral("0002")),
                            messageRow(QStringLiteral("m1"), QStringLiteral("0001"))});

        ProtocolController ctrl(m_path, nullptr);
        ctrl.start();
        QTRY_VERIFY(!ctrl.chatsLoading());
        ctrl.setConversationVisible(true);
        ctrl.selectChat(QStringLiteral("a@s"));

        QTRY_COMPARE(daemon.messagesSubscribeCount, 1);
        QTRY_COMPARE(ctrl.displayedMessagesChatId(), QStringLiteral("a@s"));
        QCOMPARE(daemon.lastMessagesParams.value(QStringLiteral("anchor")).toString(), QStringLiteral("latest"));
        QCOMPARE(daemon.lastMessagesParams.value(QStringLiteral("limit")).toInt(), 80);
        QCOMPARE(ctrl.selectedChatName(), QStringLiteral("Alice"));
        QCOMPARE(ctrl.selectedChatAvatarLocalPath(), QStringLiteral("/alice.jpg"));
        QVERIFY(ctrl.messagesAtLiveEdge());
        QVERIFY(ctrl.canLoadOlderMessages());
        QVERIFY(!ctrl.canLoadNewerMessages());

        auto *messages = qobject_cast<ProtocolMessageModel *>(ctrl.messageListModel());
        QVERIFY(messages);
        QCOMPARE(messages->allMessageIds(), QStringList({QStringLiteral("m1"), QStringLiteral("m2")}));
        QTRY_COMPARE(daemon.lastSessionParams.value(QStringLiteral("active_chat_id")).toString(), QStringLiteral("a@s"));
    }

    void unreadConversationExtendsFrontiersIndependently()
    {
        FakeDaemon daemon(m_path);
        daemon.setItem(QStringLiteral("connection"), connectionItem(QStringLiteral("online")));
        daemon.setActiveChats({chatRow(QStringLiteral("a@s"), QStringLiteral("Alice"), QStringLiteral("1-000"),
                                       QJsonObject{{QStringLiteral("unread"), 3}})});
        daemon.setUnreadAnchorId(QStringLiteral("u1"));
        daemon.setMessages({messageRow(QStringLiteral("m0"), QStringLiteral("0000")),
                            messageRow(QStringLiteral("u1"), QStringLiteral("0001")),
                            messageRow(QStringLiteral("m2"), QStringLiteral("0002"))});

        ProtocolController ctrl(m_path, nullptr);
        ctrl.start();
        QTRY_VERIFY(!ctrl.chatsLoading());
        ctrl.setConversationVisible(true);
        ctrl.selectChat(QStringLiteral("a@s"));
        QTRY_COMPARE(ctrl.unreadAnchorMessageId(), QStringLiteral("u1"));
        QCOMPARE(ctrl.unreadAnchorCount(), 3);
        QVERIFY(!ctrl.unreadAnchorResolving());
        QVERIFY(ctrl.canLoadOlderMessages());
        QVERIFY(ctrl.canLoadNewerMessages());
        QVERIFY(!ctrl.messagesAtLiveEdge());

        daemon.setNextExtendExhausted(true);
        ctrl.loadOlderMessages();
        QTRY_COMPARE(daemon.extendCount, 1);
        QCOMPARE(daemon.lastExtendParams.value(QStringLiteral("direction")).toString(), QStringLiteral("older"));
        QTRY_VERIFY(!ctrl.olderMessagesLoading());
        QVERIFY(!ctrl.canLoadOlderMessages());

        daemon.setNextExtendExhausted(false);
        ctrl.loadNewerMessages();
        QTRY_COMPARE(daemon.extendCount, 2);
        QCOMPARE(daemon.lastExtendParams.value(QStringLiteral("direction")).toString(), QStringLiteral("newer"));
        QVERIFY(ctrl.canLoadNewerMessages());
        QVERIFY(!ctrl.messagesAtLiveEdge());

        daemon.setNextExtendExhausted(true);
        ctrl.loadNewerMessages();
        QTRY_COMPARE(daemon.extendCount, 3);
        QTRY_VERIFY(ctrl.messagesAtLiveEdge());
        QVERIFY(!ctrl.canLoadNewerMessages());
    }

    void messageJumpReanchorsAndBottomReturnsToLatest()
    {
        FakeDaemon daemon(m_path);
        daemon.setItem(QStringLiteral("connection"), connectionItem(QStringLiteral("online")));
        daemon.setActiveChats({chatRow(QStringLiteral("a@s"), QStringLiteral("Alice"), QStringLiteral("1-000"))});
        daemon.setMessages({messageRow(QStringLiteral("m1"), QStringLiteral("0001"))});

        ProtocolController ctrl(m_path, nullptr);
        ctrl.start();
        QTRY_VERIFY(!ctrl.chatsLoading());
        ctrl.setConversationVisible(true);
        ctrl.selectChat(QStringLiteral("a@s"));
        QTRY_COMPARE(ctrl.displayedMessagesChatId(), QStringLiteral("a@s"));

        QSignalSpy readySpy(&ctrl, &ProtocolController::messageJumpReady);
        ctrl.jumpToMessage(QStringLiteral("m1"));
        QTRY_COMPARE(readySpy.count(), 1);
        QCOMPARE(daemon.messagesSubscribeCount, 1); // already loaded: no re-subscribe

        daemon.setMessages({messageRow(QStringLiteral("target"), QStringLiteral("0005"))});
        ctrl.jumpToMessage(QStringLiteral("target"));
        QTRY_COMPARE(daemon.messagesSubscribeCount, 2);
        QTRY_COMPARE(readySpy.count(), 2);
        QCOMPARE(daemon.lastMessagesParams.value(QStringLiteral("anchor")).toString(), QStringLiteral("target"));
        QVERIFY(!ctrl.messagesAtLiveEdge());

        ctrl.jumpToBottom();
        QTRY_COMPARE(daemon.messagesSubscribeCount, 3);
        QCOMPARE(daemon.lastMessagesParams.value(QStringLiteral("anchor")).toString(), QStringLiteral("latest"));
        QTRY_VERIFY(ctrl.messagesAtLiveEdge());

        QSignalSpy unavailableSpy(&ctrl, &ProtocolController::messageJumpUnavailable);
        ctrl.jumpToMessage(QStringLiteral("not-found"));
        QTRY_COMPARE(unavailableSpy.count(), 1);
        QVERIFY(ctrl.messageErrorText().isEmpty());
        QTRY_COMPARE(daemon.messagesSubscribeCount, 4); // prior latest window restored
    }

    void readWatermarkAndOpenChatUseConnectionSurface()
    {
        FakeDaemon daemon(m_path);
        daemon.setItem(QStringLiteral("connection"), connectionItem(QStringLiteral("online")));
        daemon.setActiveChats({chatRow(QStringLiteral("a@s"), QStringLiteral("Alice"), QStringLiteral("1-000"),
                                       QJsonObject{{QStringLiteral("unread"), 1}})});
        daemon.setUnreadAnchorId(QStringLiteral("m1"));
        daemon.setMessages({messageRow(QStringLiteral("m1"), QStringLiteral("0001")),
                            messageRow(QStringLiteral("m2"), QStringLiteral("0002"))});

        ProtocolController ctrl(m_path, nullptr);
        ctrl.start();
        QTRY_VERIFY(!ctrl.chatsLoading());
        ctrl.setConversationVisible(true);
        ctrl.selectChat(QStringLiteral("a@s"));
        QTRY_COMPARE(ctrl.displayedMessagesChatId(), QStringLiteral("a@s"));

        QSignalSpy markSpy(&daemon, &FakeDaemon::markReadReceived);
        ctrl.markSelectedChatViewed(QStringLiteral("m2"));
        ctrl.markSelectedChatViewed(QStringLiteral("m1")); // must not regress the debounce frontier
        QTRY_COMPARE_WITH_TIMEOUT(markSpy.count(), 1, 1000);
        QCOMPARE(daemon.lastMarkReadParams.value(QStringLiteral("chat_id")).toString(), QStringLiteral("a@s"));
        QCOMPARE(daemon.lastMarkReadParams.value(QStringLiteral("up_to_message_id")).toString(), QStringLiteral("m2"));

        QSignalSpy openSpy(&ctrl, &ProtocolController::openChatRequested);
        daemon.sendOpenChat(QStringLiteral("b@s"));
        QTRY_COMPARE(openSpy.count(), 1);
        QCOMPARE(openSpy.first().first().toString(), QStringLiteral("b@s"));
    }

    void resetPreservesUnreadMetadataAndFrontiers()
    {
        FakeDaemon daemon(m_path);
        daemon.setItem(QStringLiteral("connection"), connectionItem(QStringLiteral("online")));
        daemon.setActiveChats({chatRow(QStringLiteral("a@s"), QStringLiteral("Alice"), QStringLiteral("1-000"),
                                       QJsonObject{{QStringLiteral("unread"), 2}})});
        daemon.setUnreadAnchorId(QStringLiteral("u1"));
        daemon.setMessages({messageRow(QStringLiteral("u1"), QStringLiteral("0001"))});

        ProtocolController ctrl(m_path, nullptr);
        ctrl.start();
        QTRY_VERIFY(!ctrl.chatsLoading());
        ctrl.setConversationVisible(true);
        ctrl.selectChat(QStringLiteral("a@s"));
        QTRY_COMPARE(ctrl.unreadAnchorMessageId(), QStringLiteral("u1"));

        daemon.setNextExtendExhausted(true);
        ctrl.loadOlderMessages();
        QTRY_VERIFY(!ctrl.olderMessagesLoading());
        QVERIFY(!ctrl.canLoadOlderMessages());
        QVERIFY(ctrl.canLoadNewerMessages());

        daemon.resetMessages();
        QTRY_VERIFY(ctrl.messagesLoading());
        QCOMPARE(ctrl.unreadAnchorMessageId(), QStringLiteral("u1"));
        QVERIFY(!ctrl.unreadAnchorResolving());
        daemon.pushMessage(messageRow(QStringLiteral("u1"), QStringLiteral("0001")), QStringLiteral("0001"));
        daemon.readyMessages(false);

        QTRY_VERIFY(!ctrl.messagesLoading());
        QCOMPARE(ctrl.displayedMessagesChatId(), QStringLiteral("a@s"));
        QVERIFY(!ctrl.canLoadOlderMessages());
        QVERIFY(ctrl.canLoadNewerMessages());
    }

    void hiddenConversationClearsSessionAndResubscribesWhenShown()
    {
        FakeDaemon daemon(m_path);
        daemon.setItem(QStringLiteral("connection"), connectionItem(QStringLiteral("online")));
        daemon.setActiveChats({chatRow(QStringLiteral("a@s"), QStringLiteral("Alice"), QStringLiteral("1-000"))});
        daemon.setMessages({messageRow(QStringLiteral("m1"), QStringLiteral("0001"))});

        ProtocolController ctrl(m_path, nullptr);
        ctrl.start();
        QTRY_VERIFY(!ctrl.chatsLoading());
        ctrl.setConversationVisible(true);
        ctrl.selectChat(QStringLiteral("a@s"));
        QTRY_COMPARE(daemon.messagesSubscribeCount, 1);
        QTRY_COMPARE(daemon.lastSessionParams.value(QStringLiteral("active_chat_id")).toString(), QStringLiteral("a@s"));

        ctrl.setConversationVisible(false);
        QTRY_COMPARE(daemon.lastSessionParams.value(QStringLiteral("active_chat_id")).toString(), QString());
        QVERIFY(ctrl.hasSelectedChat()); // presentation selection survives the status page
        QVERIFY(ctrl.displayedMessagesChatId().isEmpty());

        ctrl.setConversationVisible(true);
        QTRY_COMPARE(daemon.messagesSubscribeCount, 2);
        QTRY_COMPARE(ctrl.displayedMessagesChatId(), QStringLiteral("a@s"));
    }

    void resetDuringInitialFillStillCompletesUnreadAndJump()
    {
        FakeDaemon daemon(m_path);
        daemon.setItem(QStringLiteral("connection"), connectionItem(QStringLiteral("online")));
        daemon.setActiveChats({chatRow(QStringLiteral("a@s"), QStringLiteral("Alice"), QStringLiteral("1-000"),
                                       QJsonObject{{QStringLiteral("unread"), 1}})});
        daemon.setUnreadAnchorId(QStringLiteral("u1"));
        daemon.setMessages({messageRow(QStringLiteral("u1"), QStringLiteral("0001"))});
        daemon.setHoldMessagesReady(true);

        ProtocolController ctrl(m_path, nullptr);
        ctrl.start();
        QTRY_VERIFY(!ctrl.chatsLoading());
        ctrl.setConversationVisible(true);
        ctrl.selectChat(QStringLiteral("a@s"));
        QTRY_COMPARE(ctrl.unreadAnchorMessageId(), QStringLiteral("u1"));
        QVERIFY(ctrl.messagesLoading());

        daemon.resetMessages();
        daemon.pushMessage(messageRow(QStringLiteral("u1"), QStringLiteral("0001")), QStringLiteral("0001"));
        daemon.readyMessages(false);
        QTRY_VERIFY(!ctrl.messagesLoading());
        QCOMPARE(ctrl.displayedMessagesChatId(), QStringLiteral("a@s"));
        QVERIFY(ctrl.canLoadOlderMessages());
        QVERIFY(ctrl.canLoadNewerMessages());

        daemon.setMessages({messageRow(QStringLiteral("target"), QStringLiteral("0002"))});
        QSignalSpy jumpSpy(&ctrl, &ProtocolController::messageJumpReady);
        ctrl.jumpToMessage(QStringLiteral("target"));
        QTRY_COMPARE(daemon.messagesSubscribeCount, 2);
        daemon.resetMessages();
        daemon.pushMessage(messageRow(QStringLiteral("target"), QStringLiteral("0002")), QStringLiteral("0002"));
        daemon.readyMessages(false);
        QTRY_COMPARE(jumpSpy.count(), 1);
        QCOMPARE(jumpSpy.first().first().toString(), QStringLiteral("target"));
    }

    void resetDuringExtendKeepsDirectionalCompletion()
    {
        FakeDaemon daemon(m_path);
        daemon.setItem(QStringLiteral("connection"), connectionItem(QStringLiteral("online")));
        daemon.setActiveChats({chatRow(QStringLiteral("a@s"), QStringLiteral("Alice"), QStringLiteral("1-000"))});
        daemon.setMessages({messageRow(QStringLiteral("m1"), QStringLiteral("0001"))});

        ProtocolController ctrl(m_path, nullptr);
        ctrl.start();
        QTRY_VERIFY(!ctrl.chatsLoading());
        ctrl.setConversationVisible(true);
        ctrl.selectChat(QStringLiteral("a@s"));
        QTRY_COMPARE(ctrl.displayedMessagesChatId(), QStringLiteral("a@s"));

        daemon.setNextExtendExhausted(true);
        daemon.setHoldExtendReady(true);
        ctrl.loadOlderMessages();
        QTRY_COMPARE(daemon.extendCount, 1);
        QVERIFY(ctrl.olderMessagesLoading());
        daemon.resetMessages();
        daemon.pushMessage(messageRow(QStringLiteral("m1"), QStringLiteral("0001")), QStringLiteral("0001"));
        daemon.releaseExtendReady();

        QTRY_VERIFY(!ctrl.olderMessagesLoading());
        QVERIFY(!ctrl.canLoadOlderMessages());
        QCOMPARE(ctrl.displayedMessagesChatId(), QStringLiteral("a@s"));
    }

    void phoneHistoryWaitsForSettledOlderRows()
    {
        FakeDaemon daemon(m_path);
        daemon.setItem(QStringLiteral("connection"), connectionItem(QStringLiteral("online")));
        daemon.setActiveChats({chatRow(QStringLiteral("a@s"), QStringLiteral("Alice"), QStringLiteral("1-000"),
                                       QJsonObject{{QStringLiteral("history_exhausted"), false}})});
        daemon.setMessages({messageRow(QStringLiteral("m1"), QStringLiteral("0001"))});
        daemon.setInitialMessagesExhausted(true);

        ProtocolController ctrl(m_path, nullptr);
        ctrl.start();
        QTRY_VERIFY(!ctrl.chatsLoading());
        ctrl.setConversationVisible(true);
        ctrl.selectChat(QStringLiteral("a@s"));
        QTRY_COMPARE(ctrl.displayedMessagesChatId(), QStringLiteral("a@s"));
        QVERIFY(!ctrl.canLoadOlderMessages());

        ctrl.requestOlderMessagesFromPhone();
        QTRY_VERIFY(ctrl.phoneHistoryRequesting());
        QTRY_COMPARE(daemon.extendCount, 1);
        daemon.pushMessage(messageRow(QStringLiteral("live"), QStringLiteral("0002")), QStringLiteral("0002"));
        QTest::qWait(75);
        QVERIFY(ctrl.phoneHistoryRequesting()); // a live append is not backfill completion

        daemon.pushMessage(messageRow(QStringLiteral("old2"), QStringLiteral("0000")), QStringLiteral("0000"));
        daemon.pushMessage(messageRow(QStringLiteral("old1"), QStringLiteral("0000a")), QStringLiteral("0000a"));
        QTRY_VERIFY(!ctrl.phoneHistoryRequesting());
        auto *messages = qobject_cast<ProtocolMessageModel *>(ctrl.messageListModel());
        QVERIFY(messages);
        QCOMPARE(messages->messageIdAt(0), QStringLiteral("old2"));

        ctrl.requestOlderMessagesFromPhone();
        QTRY_VERIFY(ctrl.phoneHistoryRequesting());
        ctrl.setConversationVisible(false);
        QVERIFY(!ctrl.phoneHistoryRequesting());
    }

    void extendFailureIsNonfatalAndStopsPrefetchRetry()
    {
        FakeDaemon daemon(m_path);
        daemon.setItem(QStringLiteral("connection"), connectionItem(QStringLiteral("online")));
        daemon.setActiveChats({chatRow(QStringLiteral("a@s"), QStringLiteral("Alice"), QStringLiteral("1-000"))});
        daemon.setMessages({messageRow(QStringLiteral("m1"), QStringLiteral("0001"))});

        ProtocolController ctrl(m_path, nullptr);
        ctrl.start();
        QTRY_VERIFY(!ctrl.chatsLoading());
        ctrl.setConversationVisible(true);
        ctrl.selectChat(QStringLiteral("a@s"));
        QTRY_VERIFY(ctrl.canLoadOlderMessages());

        daemon.setRejectNextExtend(true);
        ctrl.loadOlderMessages();
        QTRY_COMPARE(daemon.extendCount, 1);
        QTRY_VERIFY(!ctrl.olderMessagesLoading());
        QVERIFY(!ctrl.canLoadOlderMessages());
        QVERIFY(ctrl.olderMessagesFailed());
        QVERIFY(ctrl.messageErrorText().isEmpty());

        daemon.setNextExtendExhausted(true);
        ctrl.loadOlderMessages();
        QTRY_COMPARE(daemon.extendCount, 2);
        QTRY_VERIFY(!ctrl.olderMessagesFailed());
        QVERIFY(!ctrl.canLoadOlderMessages()); // successful retry reached exhaustion
    }

private:
    QTemporaryDir *m_dir = nullptr;
    QString m_path;
};

QTEST_GUILESS_MAIN(TestProtocolController)
#include "tst_protocolcontroller.moc"
