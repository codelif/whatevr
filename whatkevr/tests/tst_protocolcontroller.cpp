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

#include "protocolcontroller.h"

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

    int reconnectCount = 0;

Q_SIGNALS:
    void reconnectRequested();

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
            sendReady(sub);
        } else if (method == QLatin1String("daemon.reconnect")) {
            ++reconnectCount;
            reply(id, QJsonObject{});
            Q_EMIT reconnectRequested();
        } else {
            reply(id, QJsonObject{});
        }
    }

    void sendUpsert(int sub, const QJsonObject &item)
    {
        writeObject(QJsonObject{
            {QStringLiteral("sub"), sub},
            {QStringLiteral("event"), QStringLiteral("upsert")},
            {QStringLiteral("sort"), QStringLiteral("0")},
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
};

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

private:
    QTemporaryDir *m_dir = nullptr;
    QString m_path;
};

QTEST_GUILESS_MAIN(TestProtocolController)
#include "tst_protocolcontroller.moc"
