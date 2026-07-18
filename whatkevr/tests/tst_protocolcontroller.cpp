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

#include "collectionviewmodel.h"
#include "protocolcontroller.h"

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

    int reconnectCount = 0;
    // Params of the most recent `chats` subscribe, and how many landed — lets a
    // test assert the filter re-subscribe without racing the reply.
    QJsonObject lastChatsParams;
    int chatsSubscribeCount = 0;
    // The most recent chat.* command the controller sent.
    QString lastCommandMethod;
    QJsonObject lastCommandParams;

Q_SIGNALS:
    void reconnectRequested();
    void chatsSubscribed();
    void commandReceived();

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
            reply(id, QJsonObject{{QStringLiteral("sub"), sub}});
            if (m_items.contains(view)) {
                sendUpsert(sub, m_items.value(view));
            }
            if (m_collections.contains(view)) {
                const QList<QJsonObject> rows = m_collections.value(view);
                for (int i = 0; i < rows.size(); ++i) {
                    const QJsonObject &row = rows.at(i);
                    const QString sort = row.value(QStringLiteral("sort")).toString(
                        QStringLiteral("%1").arg(i, 4, 10, QLatin1Char('0')));
                    sendUpsert(sub, row, sort);
                }
            }
            sendReady(sub);
            if (view == QLatin1String("chats")) {
                lastChatsParams = params;
                ++chatsSubscribeCount;
                Q_EMIT chatsSubscribed();
            }
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

    void sendReady(int sub)
    {
        writeObject(QJsonObject{
            {QStringLiteral("sub"), sub},
            {QStringLiteral("event"), QStringLiteral("ready")},
        });
    }

    void reply(int id, const QJsonObject &result)
    {
        writeObject(QJsonObject{{QStringLiteral("id"), id}, {QStringLiteral("result"), result}});
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
        daemon.setCollection(QStringLiteral("chats"),
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
        daemon.setCollection(QStringLiteral("chats"),
                             {chatRow(QStringLiteral("a@s"), QStringLiteral("Alice"), QStringLiteral("1-000"))});

        ProtocolController ctrl(m_path, nullptr);
        ctrl.start();
        QTRY_COMPARE(daemon.chatsSubscribeCount, 1);
        QCOMPARE(daemon.lastChatsParams.value(QStringLiteral("filter")).toString(), QStringLiteral("all"));

        ctrl.setChatFilter(2); // groups
        QTRY_COMPARE(daemon.chatsSubscribeCount, 2);
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

private:
    QTemporaryDir *m_dir = nullptr;
    QString m_path;
};

QTEST_GUILESS_MAIN(TestProtocolController)
#include "tst_protocolcontroller.moc"
