// Unit tests for the whatevr protocol client core (D1): the ProtocolClient
// transport/dispatcher and the generic collection/object view models, exercised
// end-to-end against an in-process fake daemon speaking real NDJSON over a real
// Unix socket. No GUI, no gRPC, no daemon binary.

#include <QCoreApplication>
#include <QJsonArray>
#include <QJsonDocument>
#include <QJsonObject>
#include <QLocalServer>
#include <QLocalSocket>
#include <QSignalSpy>
#include <QTemporaryDir>
#include <QTest>

#include <utility>

#include "collectionviewmodel.h"
#include "objectviewmodel.h"
#include "protocolclient.h"

using namespace whatevr::proto;

namespace
{
// A minimal, programmable daemon stand-in. It auto-answers `hello`, assigns
// `sub` ids to `subscribe`, and acks everything else, while letting the test
// push arbitrary view events down to the client.
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
        });
    }

    void sendEvent(int sub, const QJsonObject &event)
    {
        QJsonObject obj = event;
        obj.insert(QStringLiteral("sub"), sub);
        writeObject(obj);
    }

    void sendUpsert(int sub, const QString &sort, const QJsonObject &item)
    {
        sendEvent(sub, QJsonObject{
                           {QStringLiteral("event"), QStringLiteral("upsert")},
                           {QStringLiteral("sort"), sort},
                           {QStringLiteral("item"), item},
                       });
    }

    void sendRemove(int sub, const QString &id)
    {
        sendEvent(sub, QJsonObject{
                           {QStringLiteral("event"), QStringLiteral("remove")},
                           {QStringLiteral("id"), id},
                       });
    }

    void sendReady(int sub, bool exhausted, bool includeFlag = true)
    {
        QJsonObject ev{{QStringLiteral("event"), QStringLiteral("ready")}};
        if (includeFlag) {
            ev.insert(QStringLiteral("exhausted"), exhausted);
        }
        sendEvent(sub, ev);
    }

    void sendReset(int sub)
    {
        sendEvent(sub, QJsonObject{{QStringLiteral("event"), QStringLiteral("reset")}});
    }

    void sendOpenChat(const QString &chatId)
    {
        writeObject(QJsonObject{
            {QStringLiteral("event"), QStringLiteral("open_chat")},
            {QStringLiteral("chat_id"), chatId},
        });
    }

    // Subscribe metadata the fake returns for the next subscribe.
    QJsonObject nextSubscribeMeta;
    // Records of what the client sent, for assertions.
    QString lastMethod;
    QJsonObject lastSubscribeParams;
    int lastExtendCount = 0;
    QString lastExtendDirection;
    int subscribeCount = 0;
    int unsubscribeCount = 0;
    bool rejectNextExtend = false;
    bool holdNextSubscribe = false;

    void releaseHeldSubscribe()
    {
        if (m_heldSubscribeId < 0) {
            return;
        }
        const int sub = ++subscribeCount;
        QJsonObject result = nextSubscribeMeta;
        result.insert(QStringLiteral("sub"), sub);
        reply(std::exchange(m_heldSubscribeId, -1), result);
        Q_EMIT subscribed(sub);
    }

Q_SIGNALS:
    void subscribed(int sub);
    void extended();
    void subscribeHeld();

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
        lastMethod = method;

        if (method == QLatin1String("hello")) {
            reply(id, QJsonObject{
                          {QStringLiteral("daemon"), QStringLiteral("whatevrd")},
                          {QStringLiteral("version"), QStringLiteral("0.7.0")},
                          {QStringLiteral("protocol"), 1},
                          {QStringLiteral("state"), QStringLiteral("online")},
                      });
        } else if (method == QLatin1String("subscribe")) {
            lastSubscribeParams = params;
            if (std::exchange(holdNextSubscribe, false)) {
                m_heldSubscribeId = id;
                Q_EMIT subscribeHeld();
                return;
            }
            const int sub = ++subscribeCount;
            QJsonObject result = nextSubscribeMeta;
            result.insert(QStringLiteral("sub"), sub);
            reply(id, result);
            Q_EMIT subscribed(sub);
        } else if (method == QLatin1String("extend")) {
            lastExtendCount = params.value(QStringLiteral("count")).toInt();
            lastExtendDirection = params.value(QStringLiteral("direction")).toString();
            if (std::exchange(rejectNextExtend, false)) {
                writeObject(QJsonObject{
                    {QStringLiteral("id"), id},
                    {QStringLiteral("error"), QJsonObject{
                         {QStringLiteral("code"), QStringLiteral("invalid_params")},
                         {QStringLiteral("message"), QStringLiteral("bad direction")},
                     }},
                });
            } else {
                reply(id, QJsonObject{});
            }
            Q_EMIT extended();
        } else if (method == QLatin1String("unsubscribe")) {
            ++unsubscribeCount;
            reply(id, QJsonObject{});
        } else {
            reply(id, QJsonObject{});
        }
    }

    void reply(int id, const QJsonObject &result)
    {
        writeObject(QJsonObject{
            {QStringLiteral("id"), id},
            {QStringLiteral("result"), result},
        });
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
    int m_heldSubscribeId = -1;
};

QJsonObject item(const QString &id, const QString &name)
{
    return QJsonObject{{QStringLiteral("id"), id}, {QStringLiteral("name"), name}};
}
} // namespace

class TestProtocolCore : public QObject
{
    Q_OBJECT

private Q_SLOTS:
    void init()
    {
        m_dir = new QTemporaryDir;
        m_path = m_dir->filePath(QStringLiteral("test.sock"));
        m_daemon = new FakeDaemon(m_path);
        m_client = new ProtocolClient(m_path, QStringLiteral("test"));
    }

    void cleanup()
    {
        delete m_client;
        delete m_daemon;
        delete m_dir;
        m_client = nullptr;
        m_daemon = nullptr;
        m_dir = nullptr;
    }

    void helloHandshake()
    {
        QSignalSpy readySpy(m_client, &ProtocolClient::ready);
        m_client->start();
        QVERIFY(readySpy.wait());
        QVERIFY(m_client->isReady());
        QCOMPARE(m_client->serverInfo().value(QStringLiteral("daemon")).toString(),
                 QStringLiteral("whatevrd"));
    }

    void collectionFillIsSortedAndReady()
    {
        CollectionViewModel model;
        connectAndSubscribe(&model, QStringLiteral("chats"));

        const int sub = waitForSub();
        // Deliver out of sort order; the model must render sorted ascending.
        m_daemon->sendUpsert(sub, QStringLiteral("b"), item(QStringLiteral("2"), QStringLiteral("Bob")));
        m_daemon->sendUpsert(sub, QStringLiteral("a"), item(QStringLiteral("1"), QStringLiteral("Ann")));
        m_daemon->sendUpsert(sub, QStringLiteral("c"), item(QStringLiteral("3"), QStringLiteral("Cy")));
        m_daemon->sendReady(sub, true);

        QTRY_COMPARE(model.count(), 3);
        QTRY_VERIFY(model.isReady());
        QVERIFY(model.isExhausted());
        QCOMPARE(rowName(model, 0), QStringLiteral("Ann"));
        QCOMPARE(rowName(model, 1), QStringLiteral("Bob"));
        QCOMPARE(rowName(model, 2), QStringLiteral("Cy"));
        // itemById exposes the full row map.
        QCOMPARE(model.itemById(QStringLiteral("2")).value(QStringLiteral("name")).toString(),
                 QStringLiteral("Bob"));
    }

    void upsertReplaceInPlace()
    {
        CollectionViewModel model;
        connectAndSubscribe(&model, QStringLiteral("chats"));
        const int sub = waitForSub();
        m_daemon->sendUpsert(sub, QStringLiteral("a"), item(QStringLiteral("1"), QStringLiteral("Ann")));
        QTRY_COMPARE(model.count(), 1);
        // Same sort key, new data: replace, no reorder, no count change.
        m_daemon->sendUpsert(sub, QStringLiteral("a"), item(QStringLiteral("1"), QStringLiteral("Annie")));
        QTRY_COMPARE(rowName(model, 0), QStringLiteral("Annie"));
        QCOMPARE(model.count(), 1);
    }

    void upsertWithChangedSortMoves()
    {
        CollectionViewModel model;
        connectAndSubscribe(&model, QStringLiteral("chats"));
        const int sub = waitForSub();
        m_daemon->sendUpsert(sub, QStringLiteral("a"), item(QStringLiteral("1"), QStringLiteral("Ann")));
        m_daemon->sendUpsert(sub, QStringLiteral("b"), item(QStringLiteral("2"), QStringLiteral("Bob")));
        m_daemon->sendUpsert(sub, QStringLiteral("c"), item(QStringLiteral("3"), QStringLiteral("Cy")));
        QTRY_COMPARE(model.count(), 3);
        // Move Ann to the end by changing its sort key.
        m_daemon->sendUpsert(sub, QStringLiteral("z"), item(QStringLiteral("1"), QStringLiteral("Ann")));
        QTRY_COMPARE(rowName(model, 2), QStringLiteral("Ann"));
        QCOMPARE(rowName(model, 0), QStringLiteral("Bob"));
        QCOMPARE(rowName(model, 1), QStringLiteral("Cy"));
        QCOMPARE(model.count(), 3);
        QCOMPARE(model.indexOfId(QStringLiteral("1")), 2);
    }

    void removeAndReset()
    {
        CollectionViewModel model;
        connectAndSubscribe(&model, QStringLiteral("chats"));
        const int sub = waitForSub();
        m_daemon->sendUpsert(sub, QStringLiteral("a"), item(QStringLiteral("1"), QStringLiteral("Ann")));
        m_daemon->sendUpsert(sub, QStringLiteral("b"), item(QStringLiteral("2"), QStringLiteral("Bob")));
        m_daemon->sendReady(sub, false);
        QTRY_COMPARE(model.count(), 2);

        m_daemon->sendRemove(sub, QStringLiteral("1"));
        QTRY_COMPARE(model.count(), 1);
        QCOMPARE(rowName(model, 0), QStringLiteral("Bob"));
        QCOMPARE(model.indexOfId(QStringLiteral("1")), -1);

        // reset discards the local copy; ready falls back to false.
        m_daemon->sendReset(sub);
        QTRY_COMPARE(model.count(), 0);
        QVERIFY(!model.isReady());
    }

    void readyWithoutFlagIsNotExhausted()
    {
        CollectionViewModel model;
        connectAndSubscribe(&model, QStringLiteral("chats"));
        const int sub = waitForSub();
        m_daemon->sendReady(sub, false, /*includeFlag=*/false);
        QTRY_VERIFY(model.isReady());
        QVERIFY(!model.isExhausted());
    }

    void everyReadyCompletionIsObservable()
    {
        CollectionViewModel model;
        connectAndSubscribe(&model, QStringLiteral("messages"));
        const int sub = waitForSub();
        QSignalSpy completionSpy(&model, &CollectionViewModel::readyReceived);

        m_daemon->sendReady(sub, false);
        m_daemon->sendReady(sub, false);

        QTRY_COMPARE(completionSpy.count(), 2);
        QCOMPARE(completionSpy.at(0).first().toBool(), false);
        QCOMPARE(completionSpy.at(1).first().toBool(), false);
    }

    void extendCarriesDirection()
    {
        CollectionViewModel model;
        connectAndSubscribe(&model, QStringLiteral("messages"));
        Subscription *sub = m_sub;
        waitForSub();
        QSignalSpy extendSpy(m_daemon, &FakeDaemon::extended);
        sub->extend(25, QStringLiteral("older"));
        QVERIFY(extendSpy.wait());
        QCOMPARE(m_daemon->lastExtendCount, 25);
        QCOMPARE(m_daemon->lastExtendDirection, QStringLiteral("older"));
    }

    void extendFailureIsObservable()
    {
        CollectionViewModel model;
        connectAndSubscribe(&model, QStringLiteral("messages"));
        Subscription *sub = m_sub;
        waitForSub();
        m_daemon->rejectNextExtend = true;
        QSignalSpy failureSpy(sub, &Subscription::extendFailed);

        sub->extend(25, QStringLiteral("newer"));

        QVERIFY(failureSpy.wait());
        QCOMPARE(failureSpy.first().at(0).toString(), QStringLiteral("invalid_params"));
        QCOMPARE(failureSpy.first().at(1).toString(), QStringLiteral("bad direction"));
    }

    void subscribeMetaIsExposed()
    {
        m_daemon->nextSubscribeMeta = QJsonObject{{QStringLiteral("anchor_id"), QStringLiteral("m42")}};
        CollectionViewModel model;
        connectAndSubscribe(&model, QStringLiteral("messages"));
        QSignalSpy subSpy(m_sub, &Subscription::subscribed);
        QVERIFY(subSpy.wait());
        const QVariantMap meta = subSpy.first().first().toMap();
        QCOMPARE(meta.value(QStringLiteral("anchor_id")).toString(), QStringLiteral("m42"));
        QVERIFY(!meta.contains(QStringLiteral("sub")));
        QCOMPARE(m_sub->meta().value(QStringLiteral("anchor_id")).toString(), QStringLiteral("m42"));
    }

    void staleSubscribeReplyCannotAttachToReplacement()
    {
        QSignalSpy readySpy(m_client, &ProtocolClient::ready);
        m_client->start();
        QVERIFY(readySpy.wait());
        CollectionViewModel firstModel;
        CollectionViewModel secondModel;
        m_daemon->holdNextSubscribe = true;
        QSignalSpy heldSpy(m_daemon, &FakeDaemon::subscribeHeld);
        Subscription *first = m_client->subscribe(QStringLiteral("messages"), QJsonObject{}, &firstModel);
        QVERIFY(heldSpy.wait());
        delete first;

        Subscription *second = m_client->subscribe(QStringLiteral("messages"), QJsonObject{}, &secondModel);
        QSignalSpy secondSpy(second, &Subscription::subscribed);
        QVERIFY(secondSpy.wait());
        QVERIFY(second->isActive());

        m_daemon->releaseHeldSubscribe();
        QTRY_COMPARE(m_daemon->unsubscribeCount, 1);
        QVERIFY(second->isActive());
    }

    void objectView()
    {
        ObjectViewModel obj;
        QSignalSpy readySpy(m_client, &ProtocolClient::ready);
        m_client->start();
        QVERIFY(readySpy.wait());
        m_sub = m_client->subscribe(QStringLiteral("self"), QJsonObject{}, &obj);
        const int sub = waitForSub();

        QVERIFY(!obj.isPresent());
        m_daemon->sendUpsert(sub, QString(), item(QStringLiteral("self"), QStringLiteral("Me")));
        m_daemon->sendReady(sub, true);
        QTRY_VERIFY(obj.isPresent());
        QVERIFY(obj.isReady());
        QCOMPARE(obj.value().value(QStringLiteral("name")).toString(), QStringLiteral("Me"));

        m_daemon->sendReset(sub);
        QTRY_VERIFY(!obj.isPresent());
        QVERIFY(obj.value().isEmpty());
    }

    void openChatRouted()
    {
        QSignalSpy readySpy(m_client, &ProtocolClient::ready);
        m_client->start();
        QVERIFY(readySpy.wait());
        QSignalSpy openSpy(m_client, &ProtocolClient::openChatRequested);
        m_daemon->sendOpenChat(QStringLiteral("123@g.us"));
        QVERIFY(openSpy.wait());
        QCOMPARE(openSpy.first().first().toString(), QStringLiteral("123@g.us"));
    }

    void queuedRequestFlushesAfterHello()
    {
        // A request issued before the connection is ready is queued and sent
        // once hello lands; its callback fires with the ack.
        bool called = false;
        m_client->request(QStringLiteral("chat.pin"),
                          QJsonObject{{QStringLiteral("chat_id"), QStringLiteral("x")}},
                          [&called](const QJsonObject &, const ProtocolError &error) {
                              called = !error.isError();
                          });
        m_client->start();
        QTRY_VERIFY(called);
    }

private:
    void connectAndSubscribe(ViewSink *sink, const QString &view)
    {
        QSignalSpy readySpy(m_client, &ProtocolClient::ready);
        m_client->start();
        QVERIFY(readySpy.wait());
        m_sub = m_client->subscribe(view, QJsonObject{}, sink);
    }

    int waitForSub()
    {
        QSignalSpy subSpy(m_daemon, &FakeDaemon::subscribed);
        if (m_daemon->subscribeCount == 0) {
            subSpy.wait();
        }
        return m_daemon->subscribeCount;
    }

    static QString rowName(const CollectionViewModel &model, int row)
    {
        return model.data(model.index(row), CollectionViewModel::ItemRole)
            .toMap()
            .value(QStringLiteral("name"))
            .toString();
    }

    QTemporaryDir *m_dir = nullptr;
    QString m_path;
    FakeDaemon *m_daemon = nullptr;
    ProtocolClient *m_client = nullptr;
    Subscription *m_sub = nullptr;
};

QTEST_MAIN(TestProtocolCore)
#include "tst_protocolcore.moc"
