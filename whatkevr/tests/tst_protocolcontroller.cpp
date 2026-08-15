// Unit tests for ProtocolController (D2a): the whatevr-protocol connection
// lifecycle that drives the status/login/splash screens and the shell gate.
// Exercised end-to-end against an in-process fake daemon serving the
// `connection` and `login` object views over a real Unix socket. No GUI, no
// gRPC, no daemon binary.

#include <QCoreApplication>
#include <QDateTime>
#include <QJsonArray>
#include <QJsonDocument>
#include <QJsonObject>
#include <QLocalServer>
#include <QLocalSocket>
#include <QSettings>
#include <QSignalSpy>
#include <QStandardPaths>
#include <QTemporaryDir>
#include <QTest>

#include <utility>

#include "collectionviewmodel.h"
#include "protocolcontroller.h"
#include "protocolmessagemodel.h"
#include "protocolstickercontroller.h"

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
                m_viewBySub.clear();
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
    void setHoldChatReady(bool hold) { m_holdChatReady = hold; }
    void setHoldExtendReady(bool hold) { m_holdExtendReady = hold; }
    void setRejectNextExtend(bool reject) { m_rejectNextExtend = reject; }
    void setRejectNextSend(bool reject) { m_rejectNextSend = reject; }
    void setRejectMessageCommands(bool reject) { m_rejectMessageCommands = reject; }

    // Drop the client the way a daemon exit does: the socket closes under it.
    void dropClients()
    {
        if (m_conn) {
            m_conn->disconnectFromServer();
        }
    }

    void sendOpenChat(const QString &chatId)
    {
        writeObject(QJsonObject{{QStringLiteral("event"), QStringLiteral("open_chat")},
                                {QStringLiteral("chat_id"), chatId}});
    }

    void sendMediaStreamUpdate(const QString &streamId,
                               const QString &messageId,
                               const QString &state,
                               const QString &path = {},
                               const QString &error = {})
    {
        writeObject(QJsonObject{{QStringLiteral("event"), QStringLiteral("media_stream_update")},
                                {QStringLiteral("stream_id"), streamId},
                                {QStringLiteral("message_id"), messageId},
                                {QStringLiteral("state"), state},
                                {QStringLiteral("path"), path},
                                {QStringLiteral("error"), error}});
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
    // Both chat lists are the same view, so m_subByView only remembers the
    // archived one; the active subscription is tracked separately.
    void pushActiveChat(const QJsonObject &item)
    {
        if (m_activeChatsSub >= 0) {
            sendUpsert(m_activeChatsSub, item, item.value(QStringLiteral("sort")).toString());
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
    void readyThenSetItem(const QString &view, const QJsonObject &item)
    {
        m_items.insert(view, item);
        const int sub = m_subByView.value(view, -1);
        if (sub >= 0) {
            sendReady(sub);
            sendUpsert(sub, item);
        }
    }

    // Canned query results (D5). Queries are one-shot request/response, not
    // views, so the fake just answers with whatever the test seeded.
    void setSearchChats(const QJsonArray &chats) { m_searchChats = chats; }
    void setSearchMessages(const QJsonArray &messages) { m_searchMessages = messages; }
    void setSearchStickers(const QJsonArray &stickers) { m_searchStickers = stickers; }
    void setCheckPhone(const QJsonObject &result) { m_checkPhone = result; }
    void setEnsureDirectChatId(const QString &chatId) { m_ensureDirectChatId = chatId; }
    void setProfilePicturePath(const QString &path) { m_profilePicturePath = path; }

    int reconnectCount = 0;
    // Every query the controller issued, with its params.
    QStringList queryMethods;
    QHash<QString, QJsonObject> lastQueryParams;
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
    // Subscribe params / counts for every view, and the views whose
    // subscriptions were torn down, so lifetime-scoped views can be asserted.
    QHash<QString, QJsonObject> lastParamsByView;
    QHash<QString, int> subscribeCountByView;
    QStringList unsubscribedViews;
    // Every `message.*` command received, in order.
    QStringList messageCommands;

Q_SIGNALS:
    void reconnectRequested();
    void chatsSubscribed();
    void commandReceived();
    void messagesSubscribed();
    void extended();
    void sessionUpdated();
    void markReadReceived();
    void queryReceived(const QString &method);
    void viewSubscribed(const QString &view);
    void viewUnsubscribed(const QString &view);

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
            m_viewBySub.insert(sub, view);
            lastParamsByView.insert(view, params);
            subscribeCountByView[view] = subscribeCountByView.value(view) + 1;
            if (view == QLatin1String("receipts")
                && params.value(QStringLiteral("message_id")).toString() == QLatin1String("missing")) {
                error(id, QStringLiteral("not_found"), QStringLiteral("no message \"missing\""));
                Q_EMIT viewSubscribed(view);
                return;
            }
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
            } else if (view == QLatin1String("chat")) {
                const QString chatId = params.value(QStringLiteral("chat_id")).toString();
                const auto sendMatchingChat = [this, sub, &chatId](const QList<QJsonObject> &rows) {
                    for (const QJsonObject &row : rows) {
                        if (row.value(QStringLiteral("id")).toString() == chatId) {
                            sendUpsert(sub, row);
                            return true;
                        }
                    }
                    return false;
                };
                if (!sendMatchingChat(m_chatsActive)) {
                    sendMatchingChat(m_chatsArchived);
                }
            }
            QList<QJsonObject> rows;
            bool hasRows = false;
            if (view == QLatin1String("chats")) {
                const bool archived = params.value(QStringLiteral("archived")).toBool();
                if (!archived) {
                    m_activeChatsSub = sub;
                }
                rows = archived ? m_chatsArchived : m_chatsActive;
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
            if ((view != QLatin1String("messages") || !m_holdMessagesReady)
                && (view != QLatin1String("chat") || !m_holdChatReady)) {
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
            Q_EMIT viewSubscribed(view);
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
            const int sub = params.value(QStringLiteral("sub")).toInt();
            const QString view = m_viewBySub.take(sub);
            if (m_subByView.value(view, -1) == sub) {
                m_subByView.remove(view);
            }
            reply(id, QJsonObject{});
            if (!view.isEmpty()) {
                unsubscribedViews.append(view);
                Q_EMIT viewUnsubscribed(view);
            }
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
        } else if (method == QLatin1String("send.text") || method == QLatin1String("send.media")) {
            lastCommandMethod = method;
            lastCommandParams = params;
            if (std::exchange(m_rejectNextSend, false)) {
                error(id, QStringLiteral("rejected"), QStringLiteral("send rejected"));
            } else {
                reply(id, QJsonObject{{QStringLiteral("message_id"), QStringLiteral("sent-1")}});
            }
            Q_EMIT commandReceived();
        } else if (method.startsWith(QLatin1String("message."))) {
            lastCommandMethod = method;
            lastCommandParams = params;
            messageCommands.append(method);
            if (m_rejectMessageCommands) {
                error(id, QStringLiteral("rejected"), QStringLiteral("command rejected"));
            } else if (method == QLatin1String("message.forward")) {
                reply(id, QJsonObject{{QStringLiteral("message_ids"),
                                       QJsonArray{QStringLiteral("fwd-1")}}});
            } else {
                reply(id, QJsonObject{});
            }
            Q_EMIT commandReceived();
        } else if (method.startsWith(QLatin1String("media."))) {
            lastCommandMethod = method;
            lastCommandParams = params;
            if (method == QLatin1String("media.fetch_profile_picture")) {
                reply(id, QJsonObject{{QStringLiteral("path"), m_profilePicturePath}});
            } else if (method == QLatin1String("media.stream")) {
                reply(id, QJsonObject{{QStringLiteral("stream_id"), QStringLiteral("test-stream")},
                                      {QStringLiteral("url"), QStringLiteral("http://127.0.0.1:1/media/test")},
                                      {QStringLiteral("mime"), QStringLiteral("video/mp4")},
                                      {QStringLiteral("size_bytes"), 42}});
            } else {
                reply(id, QJsonObject{});
            }
            Q_EMIT commandReceived();
        } else if (method == QLatin1String("search.chats") || method == QLatin1String("search.messages")
                   || method == QLatin1String("search.stickers")
                   || method == QLatin1String("contacts.check_phone")) {
            queryMethods.append(method);
            lastQueryParams.insert(method, params);
            if (method == QLatin1String("search.chats")) {
                reply(id, QJsonObject{{QStringLiteral("chats"), m_searchChats}});
            } else if (method == QLatin1String("search.messages")) {
                reply(id, QJsonObject{{QStringLiteral("messages"), m_searchMessages},
                                      {QStringLiteral("has_more"), false}});
            } else if (method == QLatin1String("search.stickers")) {
                reply(id, QJsonObject{{QStringLiteral("stickers"), m_searchStickers}});
            } else {
                reply(id, m_checkPhone);
            }
            Q_EMIT queryReceived(method);
        } else if (method == QLatin1String("contact.block")) {
            lastCommandMethod = method;
            lastCommandParams = params;
            reply(id, QJsonObject{});
            Q_EMIT commandReceived();
        } else if (method == QLatin1String("privacy.set")
                   || method == QLatin1String("preferences.set")
                   || method == QLatin1String("self.set_about")
                   || method == QLatin1String("account.logout")
                   || method == QLatin1String("sticker.favorite")
                   || method == QLatin1String("sticker.download")
                   || method == QLatin1String("sticker_pack.install")
                   || method == QLatin1String("sticker_packs.refresh")
                   || method == QLatin1String("send.sticker")) {
            lastCommandMethod = method;
            lastCommandParams = params;
            if (method == QLatin1String("send.sticker")) {
                reply(id, QJsonObject{{QStringLiteral("message_id"), QStringLiteral("sticker-1")}});
            } else {
                reply(id, QJsonObject{});
            }
            Q_EMIT commandReceived();
        } else if (method == QLatin1String("chat.ensure_direct")) {
            lastCommandMethod = method;
            lastCommandParams = params;
            reply(id, QJsonObject{{QStringLiteral("chat_id"), m_ensureDirectChatId}});
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
    QHash<int, QString> m_viewBySub;
    QHash<QString, QJsonObject> m_items;
    QHash<QString, QList<QJsonObject>> m_collections;
    QList<QJsonObject> m_chatsActive;
    QList<QJsonObject> m_chatsArchived;
    QList<QJsonObject> m_messages;
    QString m_unreadAnchorId;
    bool m_initialMessagesExhausted = false;
    bool m_nextExtendExhausted = false;
    bool m_holdMessagesReady = false;
    bool m_holdChatReady = false;
    bool m_holdExtendReady = false;
    bool m_heldExtendExhausted = false;
    int m_activeChatsSub = -1;
    int m_heldExtendSub = -1;
    bool m_rejectNextExtend = false;
    bool m_rejectNextSend = false;
    bool m_rejectMessageCommands = false;
    QJsonArray m_searchChats;
    QJsonArray m_searchMessages;
    QJsonArray m_searchStickers;
    QJsonObject m_checkPhone;
    QString m_ensureDirectChatId;
    QString m_profilePicturePath;
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

    void mediaStreamUpdateRejectsStaleIds()
    {
        FakeDaemon daemon(m_path);
        daemon.setItem(QStringLiteral("connection"), connectionItem(QStringLiteral("online")));

        ProtocolController ctrl(m_path, nullptr);
        ctrl.start();
        QTRY_VERIFY(ctrl.shellVisible());

        QSignalSpy readySpy(&ctrl, &ProtocolController::mediaStreamReady);
        QSignalSpy updateSpy(&ctrl, &ProtocolController::mediaStreamUpdated);
        ctrl.streamMessageMedia(QStringLiteral("video-1"));
        QTRY_COMPARE(readySpy.count(), 1);
        QCOMPARE(readySpy.first().at(0).toString(), QStringLiteral("video-1"));
        QCOMPARE(readySpy.first().at(1).toString(), QStringLiteral("test-stream"));

        daemon.sendMediaStreamUpdate(QStringLiteral("stale-stream"),
                                     QStringLiteral("video-1"),
                                     QStringLiteral("local"),
                                     QStringLiteral("/cache/stale.mp4"));
        QTest::qWait(25);
        QCOMPARE(updateSpy.count(), 0);

        daemon.sendMediaStreamUpdate(QStringLiteral("test-stream"),
                                     QStringLiteral("video-1"),
                                     QStringLiteral("local"),
                                     QStringLiteral("/cache/video.mp4"));
        QTRY_COMPARE(updateSpy.count(), 1);
        QCOMPARE(updateSpy.first().at(3).toString(), QStringLiteral("/cache/video.mp4"));
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

    // DN6: the chat list is a *window*, not the whole roster. Both `chats`
    // subscriptions carry a `limit`, and the next page is asked for with an
    // `older` extend — one at a time, and never past the daemon's exhaustion.
    void chatListIsWindowedAndExtendsOnDemand()
    {
        FakeDaemon daemon(m_path);
        daemon.setItem(QStringLiteral("connection"), connectionItem(QStringLiteral("online")));
        daemon.setActiveChats(
            {chatRow(QStringLiteral("a@s"), QStringLiteral("Alice"), QStringLiteral("1-000"))});

        ProtocolController ctrl(m_path, nullptr);
        ctrl.start();
        QTRY_COMPARE(daemon.chatsSubscribeCount, 2);
        // Both the active and the archived subscribe are windowed. lastChatsParams
        // holds the archived one (subscribed second); the limit is what matters.
        const int limit = daemon.lastChatsParams.value(QStringLiteral("limit")).toInt();
        QVERIFY(limit > 0);
        QVERIFY(daemon.lastChatsParams.value(QStringLiteral("archived")).toBool());

        auto *model = qobject_cast<CollectionViewModel *>(ctrl.chatsModel());
        QVERIFY(model);
        QTRY_VERIFY(model->isReady());
        // The fake daemon's `ready` carries no exhausted flag, so the window is
        // treated as still growable — a client must never stop extending on an
        // unknown exhaustion.
        QVERIFY(!ctrl.chatsExhausted());

        ctrl.loadMoreChats();
        QTRY_COMPARE(daemon.extendCount, 1);
        QCOMPARE(daemon.lastExtendParams.value(QStringLiteral("count")).toInt(), limit);
        QCOMPARE(daemon.lastExtendParams.value(QStringLiteral("direction")).toString(),
                 QStringLiteral("older"));

        // A second ask while the first is still in flight is dropped: the list
        // scrolls continuously and must not pile pages onto the daemon.
        daemon.setHoldExtendReady(true);
        ctrl.loadMoreChats();
        QTRY_COMPARE(daemon.extendCount, 2);
        ctrl.loadMoreChats();
        // The socket is asynchronous, so "no extend was sent" has to be observed
        // rather than merely not-yet-arrived: queue a command behind it and wait
        // for that. One connection, so the daemon sees them in order.
        ctrl.setMessageStarred(QStringLiteral("m1"), true);
        QTRY_VERIFY(daemon.messageCommands.contains(QStringLiteral("message.star")));
        QCOMPARE(daemon.extendCount, 2);

        // Once the daemon says the window is exhausted, extending stops for good.
        daemon.setHoldExtendReady(false);
        daemon.releaseExtendReady();
        daemon.setNextExtendExhausted(true);
        // Releasing clears the in-flight guard asynchronously, so keep asking
        // until the next page actually goes out.
        const int held = daemon.extendCount;
        QTRY_VERIFY([&] {
            ctrl.loadMoreChats();
            return daemon.extendCount > held;
        }());
        QTRY_VERIFY(ctrl.chatsExhausted());
        const int settled = daemon.extendCount;
        ctrl.loadMoreChats();
        QCOMPARE(daemon.extendCount, settled);
    }

    // DN7: selection has its own `chat` object view, so an off-window chat gets
    // its full row without widening either sidebar collection. The row must land
    // before the controller chooses the default messages anchor.
    void selectingAnUnloadedChatUsesObjectView()
    {
        FakeDaemon daemon(m_path);
        daemon.setItem(QStringLiteral("connection"), connectionItem(QStringLiteral("online")));
        daemon.setActiveChats(
            {chatRow(QStringLiteral("a@s"), QStringLiteral("Alice"), QStringLiteral("1-000"))});
        daemon.setItem(QStringLiteral("chat"),
                       chatRow(QStringLiteral("deep@g.us"), QStringLiteral("Deep Chat"), QStringLiteral("0"),
                               QJsonObject{{QStringLiteral("unread"), 3},
                                           {QStringLiteral("avatar_path"), QStringLiteral("/deep.jpg")},
                                           {QStringLiteral("history_exhausted"), true},
                                           {QStringLiteral("is_group"), true}}));
        daemon.setUnreadAnchorId(QStringLiteral("u1"));

        ProtocolController ctrl(m_path, nullptr);
        ctrl.start();
        QTRY_COMPARE(daemon.chatsSubscribeCount, 2);
        auto *model = qobject_cast<CollectionViewModel *>(ctrl.chatsModel());
        QVERIFY(model);
        QTRY_COMPARE(model->count(), 1);

        ctrl.setConversationVisible(true);
        ctrl.selectChat(QStringLiteral("deep@g.us"));
        QTRY_COMPARE(daemon.subscribeCountByView.value(QStringLiteral("chat")), 1);
        QCOMPARE(daemon.lastParamsByView.value(QStringLiteral("chat")).value(QStringLiteral("chat_id")).toString(),
                 QStringLiteral("deep@g.us"));
        QTRY_COMPARE(ctrl.selectedChatName(), QStringLiteral("Deep Chat"));
        QCOMPARE(ctrl.selectedChatAvatarLocalPath(), QStringLiteral("/deep.jpg"));
        QCOMPARE(ctrl.selectedChatUnreadCount(), 3);
        QVERIFY(ctrl.selectedChatHistoryExhausted());
        QTRY_COMPARE(daemon.messagesSubscribeCount, 1);
        QCOMPARE(daemon.lastMessagesParams.value(QStringLiteral("anchor")).toString(), QStringLiteral("unread"));
        QCOMPARE(daemon.extendCount, 0);

        daemon.setItem(QStringLiteral("chat"),
                       chatRow(QStringLiteral("deep@g.us"), QStringLiteral("Renamed"), QStringLiteral("0"),
                               QJsonObject{{QStringLiteral("unread"), 2}}));
        QTRY_COMPARE(ctrl.selectedChatName(), QStringLiteral("Renamed"));

        daemon.pushRemove(QStringLiteral("chat"), QStringLiteral("deep@g.us"));
        QTRY_VERIFY(!ctrl.hasSelectedChat());
        QTRY_VERIFY(daemon.unsubscribedViews.contains(QStringLiteral("chat")));
    }

    // A successful subscription whose row vanished before the initial fill
    // completes must not leave the conversation spinner latched forever.
    void selectedChatEmptyReadyClearsStaleSelection()
    {
        FakeDaemon daemon(m_path);
        daemon.setItem(QStringLiteral("connection"), connectionItem(QStringLiteral("online")));

        ProtocolController ctrl(m_path, nullptr);
        ctrl.start();
        QTRY_VERIFY(ctrl.shellVisible());
        ctrl.setConversationVisible(true);
        ctrl.selectChat(QStringLiteral("gone@s"));

        QTRY_COMPARE(daemon.subscribeCountByView.value(QStringLiteral("chat")), 1);
        QTRY_VERIFY(!ctrl.hasSelectedChat());
        QVERIFY(!ctrl.messagesLoading());
        QCOMPARE(daemon.messagesSubscribeCount, 0);
    }

    // If a row is recreated immediately after an empty initial completion, its
    // live upsert still resolves the pending default anchor instead of racing the
    // stale-selection cleanup.
    void selectedChatCanAppearAfterEmptyReady()
    {
        FakeDaemon daemon(m_path);
        daemon.setItem(QStringLiteral("connection"), connectionItem(QStringLiteral("online")));
        daemon.setHoldChatReady(true);

        ProtocolController ctrl(m_path, nullptr);
        ctrl.start();
        QTRY_VERIFY(ctrl.shellVisible());
        ctrl.setConversationVisible(true);
        ctrl.selectChat(QStringLiteral("late@s"));
        QTRY_COMPARE(daemon.subscribeCountByView.value(QStringLiteral("chat")), 1);
        daemon.readyThenSetItem(
            QStringLiteral("chat"),
            chatRow(QStringLiteral("late@s"), QStringLiteral("Late Chat"), QStringLiteral("0")));

        QTRY_COMPARE(ctrl.selectedChatName(), QStringLiteral("Late Chat"));
        QTRY_COMPARE(daemon.messagesSubscribeCount, 1);
        QCOMPARE(daemon.lastMessagesParams.value(QStringLiteral("anchor")).toString(),
                 QStringLiteral("latest"));
        QVERIFY(ctrl.hasSelectedChat());
    }

    // A same-chat explicit jump wins even when the default unread/latest choice
    // is still waiting for the selected chat row.
    void explicitJumpSupersedesPendingSelectedChatLookup()
    {
        FakeDaemon daemon(m_path);
        daemon.setItem(QStringLiteral("connection"), connectionItem(QStringLiteral("online")));
        daemon.setMessages({messageRow(QStringLiteral("target"), QStringLiteral("0001"))});

        ProtocolController ctrl(m_path, nullptr);
        ctrl.start();
        QTRY_VERIFY(ctrl.shellVisible());
        ctrl.setConversationVisible(true);
        ctrl.selectChat(QStringLiteral("deep@s"));
        ctrl.showMessageInChat(QStringLiteral("deep@s"), QStringLiteral("target"));
        daemon.setItem(QStringLiteral("chat"),
                       chatRow(QStringLiteral("deep@s"), QStringLiteral("Deep Chat"), QStringLiteral("0"),
                               QJsonObject{{QStringLiteral("unread"), 4}}));

        QTRY_COMPARE(daemon.messagesSubscribeCount, 1);
        QCOMPARE(daemon.lastMessagesParams.value(QStringLiteral("anchor")).toString(),
                 QStringLiteral("target"));
        QTRY_COMPARE(ctrl.selectedChatName(), QStringLiteral("Deep Chat"));
        QTest::qWait(50);
        QCOMPARE(daemon.messagesSubscribeCount, 1);
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

    // The header text composes the per-chat `presence` view (availability/last
    // seen) with the global `typing` view, and the subscription follows exactly
    // what the conversation is showing.
    void presenceHeaderFollowsConversation()
    {
        FakeDaemon daemon(m_path);
        daemon.setItem(QStringLiteral("connection"), connectionItem(QStringLiteral("online")));
        daemon.setActiveChats({chatRow(QStringLiteral("a@s"), QStringLiteral("Alice"), QStringLiteral("1-000"))});
        daemon.setMessages({messageRow(QStringLiteral("m1"), QStringLiteral("0001"))});
        const qint64 lastSeen = QDateTime::currentSecsSinceEpoch() - 600;
        daemon.setCollection(QStringLiteral("presence"),
                             {QJsonObject{{QStringLiteral("id"), QStringLiteral("a@s")},
                                          {QStringLiteral("availability"), QStringLiteral("offline")},
                                          {QStringLiteral("last_seen_unix"), lastSeen}}});

        ProtocolController ctrl(m_path, nullptr);
        ctrl.start();
        QTRY_VERIFY(!ctrl.chatsLoading());
        QVERIFY(ctrl.selectedChatPresenceText().isEmpty()); // nothing selected yet

        ctrl.setConversationVisible(true);
        ctrl.selectChat(QStringLiteral("a@s"));
        QTRY_COMPARE(daemon.subscribeCountByView.value(QStringLiteral("presence")), 1);
        QCOMPARE(daemon.lastParamsByView.value(QStringLiteral("presence"))
                     .value(QStringLiteral("chat_id")).toString(),
                 QStringLiteral("a@s"));
        QTRY_VERIFY(ctrl.selectedChatPresenceText().startsWith(QStringLiteral("last seen")));

        // Availability flips live.
        daemon.pushUpsert(QStringLiteral("presence"),
                          QJsonObject{{QStringLiteral("id"), QStringLiteral("a@s")},
                                      {QStringLiteral("availability"), QStringLiteral("online")}},
                          QStringLiteral("a@s"));
        QTRY_COMPARE(ctrl.selectedChatPresenceText(), QStringLiteral("online"));

        // Typing (the global view) wins over availability.
        daemon.pushUpsert(QStringLiteral("typing"),
                          QJsonObject{{QStringLiteral("id"), QStringLiteral("a@s")}}, QStringLiteral("a@s"));
        QTRY_COMPARE(ctrl.selectedChatPresenceText(), QStringLiteral("typing..."));
        daemon.pushRemove(QStringLiteral("typing"), QStringLiteral("a@s"));
        QTRY_COMPARE(ctrl.selectedChatPresenceText(), QStringLiteral("online"));

        // Hiding the conversation drops the subscription (and the upstream
        // presence demand with it); showing it again re-subscribes.
        ctrl.setConversationVisible(false);
        QTRY_VERIFY(daemon.unsubscribedViews.contains(QStringLiteral("presence")));
        QVERIFY(ctrl.selectedChatPresenceText().isEmpty());

        ctrl.setConversationVisible(true);
        QTRY_COMPARE(daemon.subscribeCountByView.value(QStringLiteral("presence")), 2);
    }

    // The info dialog subscribes `receipts` for one message while it is open:
    // group rows land as a live roster and the subscription dies with the dialog.
    void messageReceiptsAreScopedToTheOpenDialog()
    {
        FakeDaemon daemon(m_path);
        daemon.setItem(QStringLiteral("connection"), connectionItem(QStringLiteral("online")));
        daemon.setActiveChats({chatRow(QStringLiteral("g@g"), QStringLiteral("Group"), QStringLiteral("1-000"),
                                       QJsonObject{{QStringLiteral("is_group"), true}})});
        daemon.setMessages({messageRow(QStringLiteral("m1"), QStringLiteral("0001"))});
        daemon.setCollection(QStringLiteral("receipts"),
                             {QJsonObject{{QStringLiteral("id"), QStringLiteral("x@s")},
                                          {QStringLiteral("name"), QStringLiteral("Xena")},
                                          {QStringLiteral("delivered_ts_unix"), 1'700'000'100},
                                          {QStringLiteral("read_ts_unix"), 1'700'000'200}},
                              QJsonObject{{QStringLiteral("id"), QStringLiteral("y@s")},
                                          {QStringLiteral("name"), QStringLiteral("Yuri")},
                                          {QStringLiteral("delivered_ts_unix"), 1'700'000'100}}});

        ProtocolController ctrl(m_path, nullptr);
        ctrl.start();
        QTRY_VERIFY(!ctrl.chatsLoading());
        ctrl.setConversationVisible(true);
        ctrl.selectChat(QStringLiteral("g@g"));
        QTRY_COMPARE(ctrl.displayedMessagesChatId(), QStringLiteral("g@g"));

        ctrl.openMessageReceipts(QStringLiteral("m1"));
        QVERIFY(ctrl.messageReceiptsLoading());
        QTRY_VERIFY(!ctrl.messageReceiptsLoading());
        QCOMPARE(daemon.lastParamsByView.value(QStringLiteral("receipts"))
                     .value(QStringLiteral("message_id")).toString(),
                 QStringLiteral("m1"));
        QVERIFY(ctrl.messageReceiptsIsGroup());
        QCOMPARE(ctrl.messageReceipts().size(), 2);
        // The sent time comes from the message row, not from a receipt.
        QCOMPARE(ctrl.messageReceiptsSentTimestamp(), Q_INT64_C(1'700'000'000));
        QVERIFY(ctrl.directMessageReceipt().isEmpty());

        // A member reading the message re-upserts their row while the dialog is open.
        QSignalSpy receiptsSpy(&ctrl, &ProtocolController::messageReceiptsChanged);
        daemon.pushUpsert(QStringLiteral("receipts"),
                          QJsonObject{{QStringLiteral("id"), QStringLiteral("y@s")},
                                      {QStringLiteral("name"), QStringLiteral("Yuri")},
                                      {QStringLiteral("delivered_ts_unix"), 1'700'000'100},
                                      {QStringLiteral("read_ts_unix"), 1'700'000'300}},
                          QStringLiteral("0001"));
        QTRY_VERIFY(receiptsSpy.count() > 0);
        QTRY_COMPARE(ctrl.messageReceipts().at(1).toMap()
                         .value(QStringLiteral("read_ts_unix")).toLongLong(),
                     Q_INT64_C(1'700'000'300));

        ctrl.closeMessageReceipts();
        QCOMPARE(ctrl.messageReceipts().size(), 0);
        QVERIFY(!ctrl.messageReceiptsLoading());
        QTRY_VERIFY(daemon.unsubscribedViews.contains(QStringLiteral("receipts")));
    }

    // A direct chat's receipts arrive as the daemon's single aggregate row, and a
    // rejected subscribe surfaces as the dialog's error text.
    void directReceiptsAggregateAndSubscribeErrorSurface()
    {
        FakeDaemon daemon(m_path);
        daemon.setItem(QStringLiteral("connection"), connectionItem(QStringLiteral("online")));
        daemon.setActiveChats({chatRow(QStringLiteral("a@s"), QStringLiteral("Alice"), QStringLiteral("1-000"))});
        daemon.setMessages({messageRow(QStringLiteral("m1"), QStringLiteral("0001"))});
        daemon.setCollection(QStringLiteral("receipts"),
                             {QJsonObject{{QStringLiteral("id"), QStringLiteral("peer")},
                                          {QStringLiteral("delivered_ts_unix"), 1'700'000'100},
                                          {QStringLiteral("read_ts_unix"), 1'700'000'200}}});

        ProtocolController ctrl(m_path, nullptr);
        ctrl.start();
        QTRY_VERIFY(!ctrl.chatsLoading());
        ctrl.setConversationVisible(true);
        ctrl.selectChat(QStringLiteral("a@s"));
        QTRY_COMPARE(ctrl.displayedMessagesChatId(), QStringLiteral("a@s"));

        ctrl.openMessageReceipts(QStringLiteral("m1"));
        QTRY_VERIFY(!ctrl.messageReceiptsLoading());
        QVERIFY(!ctrl.messageReceiptsIsGroup());
        QCOMPARE(ctrl.directMessageReceipt().value(QStringLiteral("read_ts_unix")).toLongLong(),
                 Q_INT64_C(1'700'000'200));

        // An unknown message is rejected at subscribe; the dialog shows why.
        ctrl.openMessageReceipts(QStringLiteral("missing"));
        QTRY_VERIFY(!ctrl.messageReceiptsError().isEmpty());
        QVERIFY(!ctrl.messageReceiptsLoading()); // an error ends the wait
        QCOMPARE(ctrl.messageReceipts().size(), 0);
    }

    // sendText maps straight to `send.text` (chat_id/text/reply_to/mentions);
    // the sent message is never applied locally (no message.* result reading —
    // it would arrive back through the `messages` view), and a send with an
    // unread anchor showing dismisses that divider (the user has now seen
    // everything up to it).
    void sendTextMapsToDaemonAndClearsUnreadAnchor()
    {
        FakeDaemon daemon(m_path);
        daemon.setItem(QStringLiteral("connection"), connectionItem(QStringLiteral("online")));
        daemon.setActiveChats({chatRow(QStringLiteral("a@s"), QStringLiteral("Alice"), QStringLiteral("1-000"),
                                       QJsonObject{{QStringLiteral("unread"), 2}})});
        daemon.setUnreadAnchorId(QStringLiteral("m1"));
        daemon.setMessages({messageRow(QStringLiteral("m2"), QStringLiteral("0002")),
                            messageRow(QStringLiteral("m1"), QStringLiteral("0001"))});

        ProtocolController ctrl(m_path, nullptr);
        ctrl.start();
        QTRY_VERIFY(!ctrl.chatsLoading());
        QVERIFY(!ctrl.composerEnabled()); // no chat selected yet
        ctrl.setConversationVisible(true);
        ctrl.selectChat(QStringLiteral("a@s"));
        QTRY_COMPARE(ctrl.unreadAnchorMessageId(), QStringLiteral("m1"));
        QVERIFY(ctrl.composerEnabled());

        QSignalSpy commandSpy(&daemon, &FakeDaemon::commandReceived);
        QSignalSpy composerSpy(&ctrl, &ProtocolController::composerChanged);
        QSignalSpy sentSpy(&ctrl, &ProtocolController::messageSent);
        ctrl.sendText(QStringLiteral("  hello  "), QStringLiteral("m1"), {QStringLiteral("x@s")});
        QVERIFY(ctrl.sendInFlight());
        // The timeline follows the live edge again on this signal (the
        // snap-to-bottom-on-send setting), so it has to fire with the request,
        // not with the ack.
        QCOMPARE(sentSpy.count(), 1);
        QVERIFY(commandSpy.wait());
        QCOMPARE(daemon.lastCommandMethod, QStringLiteral("send.text"));
        QCOMPARE(daemon.lastCommandParams.value(QStringLiteral("chat_id")).toString(), QStringLiteral("a@s"));
        QCOMPARE(daemon.lastCommandParams.value(QStringLiteral("text")).toString(), QStringLiteral("hello"));
        QCOMPARE(daemon.lastCommandParams.value(QStringLiteral("reply_to")).toString(), QStringLiteral("m1"));
        QCOMPARE(daemon.lastCommandParams.value(QStringLiteral("mentions")).toArray().size(), 1);

        QTRY_VERIFY(!ctrl.sendInFlight());
        QVERIFY(ctrl.composerErrorText().isEmpty());
        QVERIFY(composerSpy.count() > 0);
        // The unread divider is gone: the user just sent past it.
        QVERIFY(ctrl.unreadAnchorMessageId().isEmpty());
        QCOMPARE(ctrl.unreadAnchorCount(), 0);
    }

    // composerEnabled is notified by composerChanged, so every input it reads —
    // the selection and the connection — has to emit that signal too. Reading
    // the getter cannot catch this: a QML binding only re-evaluates on the
    // notify signal, and without it the composer stayed disabled ("Select a
    // chat to message") on a chat that was very much open.
    void composerEnabledNotifiesOnSelectionAndConnection()
    {
        FakeDaemon daemon(m_path);
        daemon.setItem(QStringLiteral("connection"), connectionItem(QStringLiteral("online")));
        daemon.setActiveChats({chatRow(QStringLiteral("a@s"), QStringLiteral("Alice"), QStringLiteral("1-000"))});

        ProtocolController ctrl(m_path, nullptr);
        ctrl.start();
        QTRY_VERIFY(!ctrl.chatsLoading());
        QVERIFY(!ctrl.composerEnabled());

        QSignalSpy composerSpy(&ctrl, &ProtocolController::composerChanged);
        ctrl.selectChat(QStringLiteral("a@s"));
        QVERIFY(ctrl.composerEnabled());
        QVERIFY(composerSpy.count() > 0);

        // Losing the daemon disables it again, and that is announced too.
        composerSpy.clear();
        daemon.dropClients();
        QTRY_VERIFY(!ctrl.composerEnabled());
        QVERIFY(composerSpy.count() > 0);
    }

    // sendMedia resolves a file:// URL to a local path and maps to `send.media`;
    // a rejected send surfaces through composerErrorText, not a thrown/ignored
    // failure.
    void sendMediaMapsToDaemonAndSurfacesFailure()
    {
        FakeDaemon daemon(m_path);
        daemon.setItem(QStringLiteral("connection"), connectionItem(QStringLiteral("online")));
        daemon.setActiveChats({chatRow(QStringLiteral("a@s"), QStringLiteral("Alice"), QStringLiteral("1-000"))});

        ProtocolController ctrl(m_path, nullptr);
        ctrl.start();
        QTRY_VERIFY(!ctrl.chatsLoading());
        ctrl.selectChat(QStringLiteral("a@s"));

        QSignalSpy commandSpy(&daemon, &FakeDaemon::commandReceived);
        ctrl.sendMedia(QStringLiteral("file:///tmp/photo.jpg"), QStringLiteral("caption"), QString());
        QVERIFY(commandSpy.wait());
        QCOMPARE(daemon.lastCommandMethod, QStringLiteral("send.media"));
        QCOMPARE(daemon.lastCommandParams.value(QStringLiteral("path")).toString(), QStringLiteral("/tmp/photo.jpg"));
        QCOMPARE(daemon.lastCommandParams.value(QStringLiteral("caption")).toString(), QStringLiteral("caption"));
        QVERIFY(!daemon.lastCommandParams.contains(QStringLiteral("reply_to")));
        QTRY_VERIFY(!ctrl.sendInFlight());
        QVERIFY(ctrl.composerErrorText().isEmpty());

        daemon.setRejectNextSend(true);
        commandSpy.clear();
        ctrl.sendMedia(QStringLiteral("/tmp/other.png"), QString(), QString());
        QVERIFY(commandSpy.wait());
        QTRY_VERIFY(!ctrl.composerErrorText().isEmpty());
        QVERIFY(!ctrl.sendInFlight());
    }

    // The composing indicator maps to `chat.typing`; a stop is only sent for
    // the chat a start was actually sent for (mirrors AppController's dedupe —
    // an unpaired stop for a chat that was never told "composing" is a no-op).
    void composingIndicatorMapsToChatTypingWithDedupe()
    {
        FakeDaemon daemon(m_path);
        daemon.setItem(QStringLiteral("connection"), connectionItem(QStringLiteral("online")));
        daemon.setActiveChats({chatRow(QStringLiteral("a@s"), QStringLiteral("Alice"), QStringLiteral("1-000"))});

        ProtocolController ctrl(m_path, nullptr);
        ctrl.start();
        QTRY_VERIFY(!ctrl.chatsLoading());
        ctrl.selectChat(QStringLiteral("a@s"));

        // No prior "true" for this chat: a bare "false" is a no-op.
        ctrl.setSelectedChatComposing(false);
        QTest::qWait(50);
        QVERIFY(daemon.lastCommandMethod.isEmpty());

        QSignalSpy commandSpy(&daemon, &FakeDaemon::commandReceived);
        ctrl.setSelectedChatComposing(true);
        QVERIFY(commandSpy.wait());
        QCOMPARE(daemon.lastCommandMethod, QStringLiteral("chat.typing"));
        QCOMPARE(daemon.lastCommandParams.value(QStringLiteral("chat_id")).toString(), QStringLiteral("a@s"));
        QCOMPARE(daemon.lastCommandParams.value(QStringLiteral("composing")).toBool(), true);

        commandSpy.clear();
        ctrl.setSelectedChatComposing(false);
        QVERIFY(commandSpy.wait());
        QCOMPARE(daemon.lastCommandMethod, QStringLiteral("chat.typing"));
        QCOMPARE(daemon.lastCommandParams.value(QStringLiteral("composing")).toBool(), false);
    }

    // Every message action maps to its `message.*` command with the daemon's
    // param names, and nothing is applied locally: the reaction/star/pin/edit
    // all come back through the views. A rejected command surfaces as the
    // timeline's messageActionFailed notification.
    void messageActionsMapToDaemonCommands()
    {
        FakeDaemon daemon(m_path);
        daemon.setItem(QStringLiteral("connection"), connectionItem(QStringLiteral("online")));
        daemon.setActiveChats({chatRow(QStringLiteral("a@s"), QStringLiteral("Alice"), QStringLiteral("1-000"),
                                       QJsonObject{{QStringLiteral("unread"), 2}})});
        daemon.setUnreadAnchorId(QStringLiteral("m1"));
        daemon.setMessages({messageRow(QStringLiteral("m2"), QStringLiteral("0002")),
                            messageRow(QStringLiteral("m1"), QStringLiteral("0001"))});

        ProtocolController ctrl(m_path, nullptr);
        ctrl.start();
        QTRY_VERIFY(!ctrl.chatsLoading());
        ctrl.setConversationVisible(true);
        ctrl.selectChat(QStringLiteral("a@s"));
        QTRY_COMPARE(ctrl.unreadAnchorMessageId(), QStringLiteral("m1"));

        QSignalSpy commandSpy(&daemon, &FakeDaemon::commandReceived);
        QSignalSpy sentSpy(&ctrl, &ProtocolController::messageSent);

        ctrl.sendReaction(QStringLiteral("m1"), QStringLiteral("👍"));
        QVERIFY(commandSpy.wait());
        QCOMPARE(daemon.lastCommandMethod, QStringLiteral("message.react"));
        QCOMPARE(daemon.lastCommandParams.value(QStringLiteral("message_id")).toString(), QStringLiteral("m1"));
        QCOMPARE(daemon.lastCommandParams.value(QStringLiteral("emoji")).toString(), QStringLiteral("👍"));
        // Reacting means the user has seen the divider they reacted past.
        QVERIFY(ctrl.unreadAnchorMessageId().isEmpty());
        // …but it puts nothing in the timeline, so it must not drag the
        // viewport to the newest message.
        QCOMPARE(sentSpy.count(), 0);

        commandSpy.clear();
        ctrl.setMessageStarred(QStringLiteral("m1"), true);
        QVERIFY(commandSpy.wait());
        QCOMPARE(daemon.lastCommandMethod, QStringLiteral("message.star"));
        QCOMPARE(daemon.lastCommandParams.value(QStringLiteral("starred")).toBool(), true);

        commandSpy.clear();
        ctrl.pinMessage(QStringLiteral("m1"), 24 * 60 * 60);
        QVERIFY(commandSpy.wait());
        QCOMPARE(daemon.lastCommandMethod, QStringLiteral("message.pin"));
        QCOMPARE(daemon.lastCommandParams.value(QStringLiteral("pinned")).toBool(), true);
        QCOMPARE(daemon.lastCommandParams.value(QStringLiteral("duration_secs")).toInt(), 86'400);

        commandSpy.clear();
        ctrl.unpinMessage(QStringLiteral("m1"));
        QVERIFY(commandSpy.wait());
        QCOMPARE(daemon.lastCommandMethod, QStringLiteral("message.pin"));
        QCOMPARE(daemon.lastCommandParams.value(QStringLiteral("pinned")).toBool(), false);

        commandSpy.clear();
        ctrl.editMessage(QStringLiteral("m1"), QStringLiteral("  fixed  "));
        QVERIFY(commandSpy.wait());
        QCOMPARE(daemon.lastCommandMethod, QStringLiteral("message.edit"));
        QCOMPARE(daemon.lastCommandParams.value(QStringLiteral("text")).toString(), QStringLiteral("fixed"));

        commandSpy.clear();
        ctrl.revokeMessage(QStringLiteral("m1"));
        QVERIFY(commandSpy.wait());
        QCOMPARE(daemon.lastCommandMethod, QStringLiteral("message.revoke"));

        commandSpy.clear();
        ctrl.deleteMessageForMe(QStringLiteral("m1"));
        QVERIFY(commandSpy.wait());
        QCOMPARE(daemon.lastCommandMethod, QStringLiteral("message.delete"));

        // Empty ids and blank edits never reach the daemon.
        const int sent = daemon.messageCommands.size();
        ctrl.sendReaction(QString(), QStringLiteral("👍"));
        ctrl.editMessage(QStringLiteral("m1"), QStringLiteral("   "));
        ctrl.pinMessage(QStringLiteral("m1"), 0);
        QTest::qWait(50);
        QCOMPARE(daemon.messageCommands.size(), sent);

        // A rejected command is reported to the timeline.
        QSignalSpy failedSpy(&ctrl, &ProtocolController::messageActionFailed);
        daemon.setRejectMessageCommands(true);
        ctrl.setMessageStarred(QStringLiteral("m1"), false);
        QVERIFY(failedSpy.wait());
        QCOMPARE(failedSpy.first().first().toString(), QStringLiteral("command rejected"));

        // The edit window is a local pre-check only; the daemon still decides.
        QVERIFY(ctrl.canEditAt(QDateTime::currentSecsSinceEpoch() - 60));
        QVERIFY(!ctrl.canEditAt(QDateTime::currentSecsSinceEpoch() - 3600));
        QVERIFY(!ctrl.canEditAt(0));
    }

    // The pinned banner is the displayed conversation's `pinned` view: it
    // subscribes with the chat, renders rows by index, tracks live pins/unpins,
    // and is dropped when the conversation goes off screen.
    void pinnedBannerFollowsTheConversation()
    {
        FakeDaemon daemon(m_path);
        daemon.setItem(QStringLiteral("connection"), connectionItem(QStringLiteral("online")));
        daemon.setActiveChats({chatRow(QStringLiteral("a@s"), QStringLiteral("Alice"), QStringLiteral("1-000"))});
        daemon.setMessages({messageRow(QStringLiteral("m1"), QStringLiteral("0001"))});
        daemon.setCollection(QStringLiteral("pinned"), {messageRow(QStringLiteral("m1"), QStringLiteral("0001"))});

        ProtocolController ctrl(m_path, nullptr);
        ctrl.start();
        QTRY_VERIFY(!ctrl.chatsLoading());
        // Nothing subscribed yet, so there is nothing for the banner to wait on.
        QVERIFY(ctrl.pinnedMessagesReady());

        ctrl.setConversationVisible(true);
        ctrl.selectChat(QStringLiteral("a@s"));
        QTRY_COMPARE(ctrl.pinnedMessagesCount(), 1);
        QVERIFY(ctrl.pinnedMessagesReady());
        QCOMPARE(daemon.lastParamsByView.value(QStringLiteral("pinned"))
                     .value(QStringLiteral("chat_id")).toString(),
                 QStringLiteral("a@s"));

        const QVariantMap first = ctrl.pinnedMessageAt(0);
        QCOMPARE(first.value(QStringLiteral("messageId")).toString(), QStringLiteral("m1"));
        QCOMPARE(first.value(QStringLiteral("senderName")).toString(), QStringLiteral("Alice"));
        QCOMPARE(first.value(QStringLiteral("preview")).toString(), QStringLiteral("m1"));
        QVERIFY(ctrl.pinnedMessageAt(1).isEmpty());

        // A second pin arrives as an ordinary upsert; unpinning removes it.
        QSignalSpy pinnedSpy(&ctrl, &ProtocolController::pinnedMessagesChanged);
        daemon.pushUpsert(QStringLiteral("pinned"), messageRow(QStringLiteral("m2"), QStringLiteral("0002")),
                          QStringLiteral("0002"));
        QTRY_COMPARE(ctrl.pinnedMessagesCount(), 2);
        QVERIFY(pinnedSpy.count() > 0);
        QCOMPARE(ctrl.pinnedMessageAt(1).value(QStringLiteral("messageId")).toString(), QStringLiteral("m2"));

        daemon.pushRemove(QStringLiteral("pinned"), QStringLiteral("m1"));
        QTRY_COMPARE(ctrl.pinnedMessagesCount(), 1);
        QCOMPARE(ctrl.pinnedMessageAt(0).value(QStringLiteral("messageId")).toString(), QStringLiteral("m2"));

        // Hiding the conversation drops the subscription with the banner.
        ctrl.setConversationVisible(false);
        QCOMPARE(ctrl.pinnedMessagesCount(), 0);
        QVERIFY(ctrl.pinnedMessagesReady());
        QTRY_VERIFY(daemon.unsubscribedViews.contains(QStringLiteral("pinned")));
    }

    // D4c: `media.download` is a bare ack. Whether a fetch is in flight is the
    // message row's own `media.downloading`; the global `transfers` view supplies
    // only the byte counters. Nothing about a download is stored frontend-side.
    void mediaDownloadRidesTheTransfersView()
    {
        const auto mediaMessage = [](bool downloading, const QString &path) {
            QJsonObject media{{QStringLiteral("mime"), QStringLiteral("image/jpeg")}};
            if (downloading) {
                media.insert(QStringLiteral("downloading"), true);
            }
            if (!path.isEmpty()) {
                media.insert(QStringLiteral("path"), path);
            }
            QJsonObject row = messageRow(QStringLiteral("m1"), QStringLiteral("0001"));
            row.insert(QStringLiteral("kind"), QStringLiteral("image"));
            row.insert(QStringLiteral("media"), media);
            return row;
        };

        FakeDaemon daemon(m_path);
        daemon.setItem(QStringLiteral("connection"), connectionItem(QStringLiteral("online")));
        daemon.setActiveChats({chatRow(QStringLiteral("a@s"), QStringLiteral("Alice"), QStringLiteral("1-000"))});
        daemon.setMessages({mediaMessage(false, {})});

        ProtocolController ctrl(m_path, nullptr);
        ctrl.start();
        QTRY_VERIFY(!ctrl.chatsLoading());
        // Global and session-long, like typing/sync — it carries only what is
        // downloading right now.
        QTRY_COMPARE(daemon.subscribeCountByView.value(QStringLiteral("transfers")), 1);

        ctrl.setConversationVisible(true);
        ctrl.selectChat(QStringLiteral("a@s"));
        QAbstractItemModel *timeline = ctrl.messageListModel();
        QTRY_COMPARE(timeline->rowCount(), 1);
        const auto downloading = [timeline] {
            return timeline->data(timeline->index(0, 0), ProtocolMessageModel::MediaDownloadingRole).toBool();
        };
        const auto progress = [timeline] {
            return timeline->data(timeline->index(0, 0), ProtocolMessageModel::MediaDownloadProgressRole).toDouble();
        };
        QVERIFY(!downloading());

        ctrl.downloadMessageMedia(QStringLiteral("m1"));
        QTRY_COMPARE(daemon.lastCommandMethod, QStringLiteral("media.download"));
        QCOMPARE(daemon.lastCommandParams.value(QStringLiteral("message_id")).toString(),
                 QStringLiteral("m1"));
        // The ack alone changes nothing on screen: the bubble only starts
        // spinning once the daemon upserts the message row saying so.
        QVERIFY(!downloading());

        daemon.pushUpsert(QStringLiteral("messages"), mediaMessage(true, {}), QStringLiteral("0001"));
        QTRY_VERIFY(downloading());

        daemon.pushUpsert(QStringLiteral("transfers"), QJsonObject{
                              {QStringLiteral("id"), QStringLiteral("m1")},
                              {QStringLiteral("message_id"), QStringLiteral("m1")},
                              {QStringLiteral("direction"), QStringLiteral("download")},
                              {QStringLiteral("received_bytes"), 256},
                              {QStringLiteral("total_bytes"), 1024},
                          }, QStringLiteral("m1"));
        QTRY_COMPARE(progress(), 0.25);

        // The transfer row disappearing on its own must not end the download:
        // that is the race that used to flash the download button back before
        // the path arrived.
        daemon.pushRemove(QStringLiteral("transfers"), QStringLiteral("m1"));
        QTRY_COMPARE(progress(), -1.0);
        QVERIFY(downloading());

        // Terminal: one message upsert both ends the download and delivers the
        // file, so the bubble never sees a state in between.
        daemon.pushUpsert(QStringLiteral("messages"), mediaMessage(false, QStringLiteral("/cache/m1.jpg")),
                          QStringLiteral("0001"));
        QTRY_VERIFY(!downloading());
        QCOMPARE(timeline->data(timeline->index(0, 0), ProtocolMessageModel::MediaLocalPathRole).toString(),
                 QStringLiteral("/cache/m1.jpg"));
    }

    // The forward picker holds its own `chats` subscription for exactly as long
    // as it is open, filters those rows presentation-side, and reports one
    // "forwarded" for the whole multi-message batch.
    void forwardPickerScopesItsChatsViewAndBatchesTheReport()
    {
        FakeDaemon daemon(m_path);
        daemon.setItem(QStringLiteral("connection"), connectionItem(QStringLiteral("online")));
        daemon.setActiveChats({chatRow(QStringLiteral("a@s"), QStringLiteral("Alice"), QStringLiteral("1-000")),
                               chatRow(QStringLiteral("b@s"), QStringLiteral("Bob"), QStringLiteral("2-000"))});

        ProtocolController ctrl(m_path, nullptr);
        ctrl.start();
        QTRY_VERIFY(!ctrl.chatsLoading());
        const int chatsSubsBefore = daemon.subscribeCountByView.value(QStringLiteral("chats"));

        ctrl.openForwardTargets();
        QTRY_COMPARE(daemon.subscribeCountByView.value(QStringLiteral("chats")), chatsSubsBefore + 1);
        // Its own params: every chat, not whatever the sidebar filter shows.
        QCOMPARE(daemon.lastParamsByView.value(QStringLiteral("chats"))
                     .value(QStringLiteral("filter")).toString(),
                 QStringLiteral("all"));
        QCOMPARE(daemon.lastParamsByView.value(QStringLiteral("chats"))
                     .value(QStringLiteral("archived")).toBool(),
                 false);
        QTRY_COMPARE(ctrl.forwardChatTargets(QString()).size(), 2);
        QCOMPARE(ctrl.forwardChatTargets(QStringLiteral("bo")).size(), 1);
        QCOMPARE(ctrl.forwardChatTargets(QStringLiteral("bo")).first().toMap()
                     .value(QStringLiteral("id")).toString(),
                 QStringLiteral("b@s"));
        QCOMPARE(ctrl.forwardChatTargets(QStringLiteral("nobody")).size(), 0);

        // Two source messages to two chats: one report for the batch.
        QSignalSpy forwardedSpy(&ctrl, &ProtocolController::messageForwarded);
        const QStringList targets{QStringLiteral("a@s"), QStringLiteral("b@s")};
        ctrl.forwardMessage(QStringLiteral("m1"), targets);
        ctrl.forwardMessage(QStringLiteral("m2"), targets);
        QVERIFY(forwardedSpy.wait());
        QCOMPARE(forwardedSpy.count(), 1);
        QCOMPARE(forwardedSpy.first().first().toInt(), 2);
        QCOMPARE(daemon.lastCommandMethod, QStringLiteral("message.forward"));
        QCOMPARE(daemon.lastCommandParams.value(QStringLiteral("chat_ids")).toArray().size(), 2);

        // A failing batch reports once too, as a failure rather than a success.
        daemon.setRejectMessageCommands(true);
        QSignalSpy failedSpy(&ctrl, &ProtocolController::messageActionFailed);
        forwardedSpy.clear();
        ctrl.forwardMessage(QStringLiteral("m1"), targets);
        ctrl.forwardMessage(QStringLiteral("m2"), targets);
        QVERIFY(failedSpy.wait());
        QTest::qWait(50);
        QCOMPARE(failedSpy.count(), 1);
        QCOMPARE(forwardedSpy.count(), 0);

        ctrl.closeForwardTargets();
        QCOMPARE(ctrl.forwardChatTargets(QString()).size(), 0);
        QTRY_VERIFY(daemon.unsubscribedViews.contains(QStringLiteral("chats")));
    }

    // D5: the chat-list search runs the daemon's *queries* (chat names, message
    // text, and — only for a number-shaped query — a phone lookup) and renders
    // each result set in its own section, in the daemon's order.
    void unifiedSearchRunsDaemonQueries()
    {
        FakeDaemon daemon(m_path);
        daemon.setItem(QStringLiteral("connection"), connectionItem(QStringLiteral("online")));
        daemon.setSearchChats({chatRow(QStringLiteral("a@s"), QStringLiteral("Alice"), QStringLiteral("1-000"),
                                       QJsonObject{{QStringLiteral("preview"), QStringLiteral("hi there")}})});
        daemon.setSearchMessages({messageRow(QStringLiteral("m1"), QStringLiteral("0001"))});

        ProtocolController ctrl(m_path, nullptr);
        ctrl.start();
        QTRY_VERIFY(!ctrl.chatsLoading());
        QVERIFY(!ctrl.searchActive());

        ctrl.setSearchQuery(QStringLiteral("ali"));
        QVERIFY(ctrl.searchActive());
        auto *model = ctrl.searchResultsModel();
        QTRY_COMPARE(model->rowCount(), 2);
        QTRY_VERIFY(!ctrl.searchBusy());
        // A name search never hits the phone lookup.
        QVERIFY(!daemon.queryMethods.contains(QStringLiteral("contacts.check_phone")));
        QCOMPARE(daemon.lastQueryParams.value(QStringLiteral("search.chats"))
                     .value(QStringLiteral("query")).toString(),
                 QStringLiteral("ali"));

        const auto roleValue = [model](int row, const char *role) {
            const auto names = model->roleNames();
            for (auto it = names.begin(); it != names.end(); ++it) {
                if (it.value() == QByteArray(role)) {
                    return model->data(model->index(row, 0), it.key());
                }
            }
            return QVariant{};
        };
        QCOMPARE(roleValue(0, "kind").toString(), QStringLiteral("chat"));
        QCOMPARE(roleValue(0, "title").toString(), QStringLiteral("Alice"));
        QCOMPARE(roleValue(0, "subtitle").toString(), QStringLiteral("hi there"));
        QCOMPARE(roleValue(1, "kind").toString(), QStringLiteral("message"));
        QCOMPARE(roleValue(1, "messageId").toString(), QStringLiteral("m1"));
        QCOMPARE(roleValue(1, "senderName").toString(), QStringLiteral("Alice"));

        // A number-shaped query adds the phone row above both sections.
        daemon.setCheckPhone(QJsonObject{{QStringLiteral("registered"), true},
                                         {QStringLiteral("jid"), QStringLiteral("911@s")},
                                         {QStringLiteral("display_name"), QStringLiteral("Ravi")},
                                         {QStringLiteral("phone"), QStringLiteral("+91 98765 43210")}});
        ctrl.setSearchQuery(QStringLiteral("+91 98765 43210"));
        QTRY_COMPARE(model->rowCount(), 3);
        QCOMPARE(roleValue(0, "kind").toString(), QStringLiteral("number"));
        QCOMPARE(roleValue(0, "jid").toString(), QStringLiteral("911@s"));
        QVERIFY(roleValue(0, "registered").toBool());

        // Clearing drops every result and the busy flag with them.
        ctrl.clearSearch();
        QCOMPARE(model->rowCount(), 0);
        QVERIFY(!ctrl.searchActive());
        QVERIFY(!ctrl.searchBusy());
    }

    // D5: in-chat search is the same query scoped to the selected chat; the
    // match cursor (presentation state) wraps in both directions, and leaving
    // the conversation ends the search.
    void chatSearchWalksMatchesAndEndsWithTheChat()
    {
        FakeDaemon daemon(m_path);
        daemon.setItem(QStringLiteral("connection"), connectionItem(QStringLiteral("online")));
        daemon.setActiveChats({chatRow(QStringLiteral("a@s"), QStringLiteral("Alice"), QStringLiteral("1-000")),
                               chatRow(QStringLiteral("b@s"), QStringLiteral("Bob"), QStringLiteral("2-000"))});
        daemon.setSearchMessages({messageRow(QStringLiteral("m1"), QStringLiteral("0001")),
                                  messageRow(QStringLiteral("m2"), QStringLiteral("0002"))});

        ProtocolController ctrl(m_path, nullptr);
        ctrl.start();
        QTRY_VERIFY(!ctrl.chatsLoading());
        ctrl.setConversationVisible(true);
        ctrl.selectChat(QStringLiteral("a@s"));

        ctrl.openChatSearch();
        QVERIFY(ctrl.chatSearchActive());
        ctrl.setChatSearchQuery(QStringLiteral("hello"));
        QTRY_COMPARE(ctrl.chatSearchMatchCount(), 2);
        QCOMPARE(daemon.lastQueryParams.value(QStringLiteral("search.messages"))
                     .value(QStringLiteral("chat_id")).toString(),
                 QStringLiteral("a@s"));
        QCOMPARE(ctrl.chatSearchCurrentIndex(), 1);
        QCOMPARE(ctrl.chatSearchActiveMessageId(), QStringLiteral("m1"));

        ctrl.chatSearchNext();
        QCOMPARE(ctrl.chatSearchActiveMessageId(), QStringLiteral("m2"));
        ctrl.chatSearchNext(); // wraps
        QCOMPARE(ctrl.chatSearchActiveMessageId(), QStringLiteral("m1"));
        ctrl.chatSearchPrevious(); // wraps the other way
        QCOMPARE(ctrl.chatSearchActiveMessageId(), QStringLiteral("m2"));

        // An emptied query keeps the bar open but drops the matches.
        ctrl.setChatSearchQuery(QString());
        QCOMPARE(ctrl.chatSearchMatchCount(), 0);
        QVERIFY(ctrl.chatSearchActive());

        ctrl.setChatSearchQuery(QStringLiteral("hello"));
        QTRY_COMPARE(ctrl.chatSearchMatchCount(), 2);
        // The search is scoped to one conversation; switching chats ends it.
        ctrl.selectChat(QStringLiteral("b@s"));
        QVERIFY(!ctrl.chatSearchActive());
        QCOMPARE(ctrl.chatSearchMatchCount(), 0);
        QVERIFY(ctrl.chatSearchQuery().isEmpty());
    }

    // D5: the starred page owns a windowed `starred` subscription for exactly as
    // long as it is open, and extends it (always `older`) as the user scrolls.
    void starredPageOwnsItsWindowedView()
    {
        FakeDaemon daemon(m_path);
        daemon.setItem(QStringLiteral("connection"), connectionItem(QStringLiteral("online")));
        daemon.setCollection(QStringLiteral("starred"),
                             {messageRow(QStringLiteral("m1"), QStringLiteral("0001"))});

        ProtocolController ctrl(m_path, nullptr);
        ctrl.start();
        QTRY_VERIFY(!ctrl.chatsLoading());
        // Nothing is subscribed before the page opens.
        QCOMPARE(ctrl.starredMessagesModel()->rowCount(), 0);
        QVERIFY(!ctrl.starredMessagesLoading());

        ctrl.openStarredMessages(QStringLiteral("a@s"));
        QTRY_COMPARE(ctrl.starredMessagesModel()->rowCount(), 1);
        QCOMPARE(daemon.lastParamsByView.value(QStringLiteral("starred"))
                     .value(QStringLiteral("chat_id")).toString(),
                 QStringLiteral("a@s"));
        QVERIFY(!ctrl.starredMessagesLoading());

        // Display strings come off the daemon row itself (no second lookup).
        const QVariantMap item = ctrl.starredMessagesModel()
                                     ->data(ctrl.starredMessagesModel()->index(0, 0),
                                            CollectionViewModel::ItemRole)
                                     .toMap();
        const QVariantMap row = ctrl.messageRowDisplay(item);
        QCOMPARE(row.value(QStringLiteral("messageId")).toString(), QStringLiteral("m1"));
        QCOMPARE(row.value(QStringLiteral("senderName")).toString(), QStringLiteral("Alice"));
        QCOMPARE(row.value(QStringLiteral("preview")).toString(), QStringLiteral("m1"));
        QVERIFY(!row.value(QStringLiteral("timeText")).toString().isEmpty());

        // Unstarring elsewhere is an ordinary remove; a new star an upsert.
        daemon.pushUpsert(QStringLiteral("starred"), messageRow(QStringLiteral("m2"), QStringLiteral("0002")),
                          QStringLiteral("0002"));
        QTRY_COMPARE(ctrl.starredMessagesModel()->rowCount(), 2);
        daemon.pushRemove(QStringLiteral("starred"), QStringLiteral("m1"));
        QTRY_COMPARE(ctrl.starredMessagesModel()->rowCount(), 1);

        const int extendsBefore = daemon.extendCount;
        ctrl.loadMoreStarredMessages();
        QTRY_COMPARE(daemon.extendCount, extendsBefore + 1);
        QCOMPARE(daemon.lastExtendParams.value(QStringLiteral("direction")).toString(),
                 QStringLiteral("older"));

        ctrl.closeStarredMessages();
        QCOMPARE(ctrl.starredMessagesModel()->rowCount(), 0);
        QTRY_VERIFY(daemon.unsubscribedViews.contains(QStringLiteral("starred")));
    }

    // D5: the contact card is a window onto the `contact` view — the local card
    // renders immediately, the network "about" arrives as an ordinary upsert,
    // and blocked-ness is membership in the `blocklist` view, not card state.
    void contactCardStreamsItsSecondPhase()
    {
        FakeDaemon daemon(m_path);
        daemon.setItem(QStringLiteral("connection"), connectionItem(QStringLiteral("online")));
        daemon.setItem(QStringLiteral("contact"),
                       QJsonObject{{QStringLiteral("id"), QStringLiteral("a@s")},
                                   {QStringLiteral("jid"), QStringLiteral("a@s")},
                                   {QStringLiteral("phone"), QStringLiteral("+91 1")},
                                   {QStringLiteral("saved_name"), QStringLiteral("Alice")}});

        ProtocolController ctrl(m_path, nullptr);
        ctrl.start();
        QTRY_VERIFY(!ctrl.chatsLoading());

        ctrl.openContactCard(QStringLiteral("a@s"));
        QCOMPARE(ctrl.infoCardKind(), QStringLiteral("contact"));
        QTRY_COMPARE(ctrl.infoCard().value(QStringLiteral("saved_name")).toString(), QStringLiteral("Alice"));
        QVERIFY(!ctrl.infoCardLoading());
        QVERIFY(ctrl.infoCardError().isEmpty());
        QVERIFY(!ctrl.infoCardBlocked());

        // Phase two: the same item upserts again carrying the about text.
        daemon.pushUpsert(QStringLiteral("contact"),
                          QJsonObject{{QStringLiteral("id"), QStringLiteral("a@s")},
                                      {QStringLiteral("jid"), QStringLiteral("a@s")},
                                      {QStringLiteral("phone"), QStringLiteral("+91 1")},
                                      {QStringLiteral("saved_name"), QStringLiteral("Alice")},
                                      {QStringLiteral("about"), QStringLiteral("at the beach")}},
                          QStringLiteral("0"));
        QTRY_COMPARE(ctrl.infoCard().value(QStringLiteral("about")).toString(), QStringLiteral("at the beach"));
        // Phase one's fields survive: the daemon re-upserts the whole card.
        QCOMPARE(ctrl.infoCard().value(QStringLiteral("phone")).toString(), QStringLiteral("+91 1"));

        // Blocking is an ack; the state comes back through the blocklist view.
        ctrl.setContactBlocked(QStringLiteral("a@s"), true);
        QTRY_COMPARE(daemon.lastCommandMethod, QStringLiteral("contact.block"));
        QCOMPARE(daemon.lastCommandParams.value(QStringLiteral("blocked")).toBool(), true);
        daemon.pushUpsert(QStringLiteral("blocklist"),
                          QJsonObject{{QStringLiteral("id"), QStringLiteral("a@s")},
                                      {QStringLiteral("jid"), QStringLiteral("a@s")}},
                          QStringLiteral("0"));
        QTRY_VERIFY(ctrl.infoCardBlocked());
        daemon.pushRemove(QStringLiteral("blocklist"), QStringLiteral("a@s"));
        QTRY_VERIFY(!ctrl.infoCardBlocked());

        // The avatar viewer's full-resolution fetch is a plain command.
        daemon.setProfilePicturePath(QStringLiteral("/cache/a.jpg"));
        QSignalSpy pictureSpy(&ctrl, &ProtocolController::profilePictureReady);
        ctrl.viewProfilePicture(QStringLiteral("a@s"));
        QVERIFY(pictureSpy.wait());
        QCOMPARE(pictureSpy.first().at(1).toString(), QStringLiteral("/cache/a.jpg"));

        // Closing the dialog drops both of its subscriptions.
        ctrl.closeInfoCard();
        QVERIFY(ctrl.infoCardKind().isEmpty());
        QVERIFY(ctrl.infoCard().isEmpty());
        QTRY_VERIFY(daemon.unsubscribedViews.contains(QStringLiteral("contact")));
        QVERIFY(daemon.unsubscribedViews.contains(QStringLiteral("blocklist")));
    }

    // D5: the group card is two views — the card itself and its roster — and a
    // join/leave/promotion is an ordinary upsert/remove on the second one.
    // Member search filters rows the frontend already has; it never reorders.
    void groupCardAndRosterAreSeparateViews()
    {
        FakeDaemon daemon(m_path);
        daemon.setItem(QStringLiteral("connection"), connectionItem(QStringLiteral("online")));
        daemon.setItem(QStringLiteral("group"),
                       QJsonObject{{QStringLiteral("id"), QStringLiteral("g@g.us")},
                                   {QStringLiteral("subject"), QStringLiteral("Trip")},
                                   {QStringLiteral("member_count"), 2}});
        daemon.setCollection(QStringLiteral("group_members"),
                             {QJsonObject{{QStringLiteral("id"), QStringLiteral("a@s")},
                                          {QStringLiteral("jid"), QStringLiteral("a@s")},
                                          {QStringLiteral("display_name"), QStringLiteral("Alice")},
                                          {QStringLiteral("role"), QStringLiteral("admin")},
                                          {QStringLiteral("sort"), QStringLiteral("0001")}},
                              QJsonObject{{QStringLiteral("id"), QStringLiteral("b@s")},
                                          {QStringLiteral("jid"), QStringLiteral("b@s")},
                                          {QStringLiteral("display_name"), QStringLiteral("Bob")},
                                          {QStringLiteral("phone"), QStringLiteral("+91 5")},
                                          {QStringLiteral("role"), QStringLiteral("member")},
                                          {QStringLiteral("sort"), QStringLiteral("0002")}}});

        ProtocolController ctrl(m_path, nullptr);
        ctrl.start();
        QTRY_VERIFY(!ctrl.chatsLoading());

        ctrl.openGroupCard(QStringLiteral("g@g.us"));
        QCOMPARE(ctrl.infoCardKind(), QStringLiteral("group"));
        QTRY_COMPARE(ctrl.groupMemberCount(), 2);
        QCOMPARE(ctrl.infoCard().value(QStringLiteral("subject")).toString(), QStringLiteral("Trip"));
        QCOMPARE(daemon.lastParamsByView.value(QStringLiteral("group_members"))
                     .value(QStringLiteral("chat_id")).toString(),
                 QStringLiteral("g@g.us"));

        // Roster order is the daemon's; search only narrows it.
        QCOMPARE(ctrl.groupMembers(QString()).size(), 2);
        QCOMPARE(ctrl.groupMembers(QString()).first().toMap()
                     .value(QStringLiteral("display_name")).toString(),
                 QStringLiteral("Alice"));
        QCOMPARE(ctrl.groupMembers(QStringLiteral("bo")).size(), 1);
        QCOMPARE(ctrl.groupMembers(QStringLiteral("+91")).size(), 1); // phone matches too
        QCOMPARE(ctrl.groupMembers(QStringLiteral("nobody")).size(), 0);

        // A join is an upsert, a departure a remove — no card rewrite.
        daemon.pushUpsert(QStringLiteral("group_members"),
                          QJsonObject{{QStringLiteral("id"), QStringLiteral("c@s")},
                                      {QStringLiteral("jid"), QStringLiteral("c@s")},
                                      {QStringLiteral("display_name"), QStringLiteral("Chandni")},
                                      {QStringLiteral("role"), QStringLiteral("member")}},
                          QStringLiteral("0003"));
        QTRY_COMPARE(ctrl.groupMemberCount(), 3);
        daemon.pushRemove(QStringLiteral("group_members"), QStringLiteral("b@s"));
        QTRY_COMPARE(ctrl.groupMemberCount(), 2);

        ctrl.closeInfoCard();
        QCOMPARE(ctrl.groupMemberCount(), 0);
        QTRY_VERIFY(daemon.unsubscribedViews.contains(QStringLiteral("group")));
        QVERIFY(daemon.unsubscribedViews.contains(QStringLiteral("group_members")));
    }

    // D5: the composer's `@`-mention roster is a `group_members` subscription on
    // the *displayed* conversation — held only while a group is on screen, and
    // only once something asks for it. Resolving a group's members is the most
    // expensive thing the daemon does for a chat, so opening one must not pay
    // for a roster nothing is showing yet; ensureChatMembers() is what the
    // composer calls when an `@` token opens.
    void mentionRosterFollowsTheDisplayedGroup()
    {
        FakeDaemon daemon(m_path);
        daemon.setItem(QStringLiteral("connection"), connectionItem(QStringLiteral("online")));
        daemon.setActiveChats({chatRow(QStringLiteral("g@g.us"), QStringLiteral("Trip"), QStringLiteral("1-000")),
                               chatRow(QStringLiteral("a@s"), QStringLiteral("Alice"), QStringLiteral("2-000"))});
        daemon.setCollection(QStringLiteral("group_members"),
                             {QJsonObject{{QStringLiteral("id"), QStringLiteral("a@s")},
                                          {QStringLiteral("jid"), QStringLiteral("a@s")},
                                          {QStringLiteral("display_name"), QStringLiteral("Alice")},
                                          {QStringLiteral("role"), QStringLiteral("member")},
                                          {QStringLiteral("sort"), QStringLiteral("0001")}}});

        ProtocolController ctrl(m_path, nullptr);
        ctrl.start();
        QTRY_VERIFY(!ctrl.chatsLoading());
        QCOMPARE(ctrl.chatMembers(QString()).size(), 0);

        ctrl.setConversationVisible(true);
        ctrl.selectChat(QStringLiteral("g@g.us"));
        // Opening the group alone must not subscribe the roster.
        QCoreApplication::processEvents();
        QCOMPARE(daemon.subscribeCountByView.value(QStringLiteral("group_members")), 0);
        QCOMPARE(ctrl.chatMembers(QString()).size(), 0);

        ctrl.ensureChatMembers();
        QTRY_COMPARE(ctrl.chatMembers(QString()).size(), 1);
        QCOMPARE(ctrl.chatMembers(QStringLiteral("ali")).size(), 1);

        // A 1:1 conversation has no roster to hold.
        ctrl.selectChat(QStringLiteral("a@s"));
        QCOMPARE(ctrl.chatMembers(QString()).size(), 0);
        QTRY_VERIFY(daemon.unsubscribedViews.contains(QStringLiteral("group_members")));

        // The demand does not carry across conversations: the next group starts
        // without a roster until it is asked for again.
        ctrl.selectChat(QStringLiteral("g@g.us"));
        QCoreApplication::processEvents();
        QCOMPARE(ctrl.chatMembers(QString()).size(), 0);
        ctrl.ensureChatMembers();
        QTRY_COMPARE(ctrl.chatMembers(QString()).size(), 1);

        // Hiding the conversation drops it again.
        ctrl.setConversationVisible(false);
        QCOMPARE(ctrl.chatMembers(QString()).size(), 0);
    }

    // D5: a phone-number hit starts a chat through `chat.ensure_direct`; the row
    // itself arrives through the `chats` view, so all the controller does with
    // the ack is select the chat and ask the shell to surface it.
    void startDirectChatSelectsTheDaemonsChat()
    {
        FakeDaemon daemon(m_path);
        daemon.setItem(QStringLiteral("connection"), connectionItem(QStringLiteral("online")));
        daemon.setActiveChats({chatRow(QStringLiteral("911@s"), QStringLiteral("Ravi"), QStringLiteral("1-000"))});
        daemon.setEnsureDirectChatId(QStringLiteral("911@s"));

        ProtocolController ctrl(m_path, nullptr);
        ctrl.start();
        QTRY_VERIFY(!ctrl.chatsLoading());
        ctrl.setSearchQuery(QStringLiteral("+91 98765 43210"));

        QSignalSpy openSpy(&ctrl, &ProtocolController::openChatRequested);
        ctrl.startDirectChat(QStringLiteral("911@s"));
        QVERIFY(openSpy.wait());
        QCOMPARE(openSpy.first().first().toString(), QStringLiteral("911@s"));
        QCOMPARE(ctrl.selectedChatId(), QStringLiteral("911@s"));
        QCOMPARE(daemon.lastCommandParams.value(QStringLiteral("jid")).toString(), QStringLiteral("911@s"));
        // Opening a result dismisses the search.
        QVERIFY(!ctrl.searchActive());
    }

    // D6: session-long self/preferences rows and page-scoped privacy/blocklist
    // rows stay daemon-owned; every mutation is an ack and waits for a view
    // upsert/remove rather than changing the controller's copy optimistically.
    void settingsAndProfileUseProtocolViewsAndCommands()
    {
        FakeDaemon daemon(m_path);
        daemon.setItem(QStringLiteral("connection"), connectionItem(QStringLiteral("online")));
        daemon.setItem(QStringLiteral("self"),
                       QJsonObject{{QStringLiteral("id"), QStringLiteral("self")},
                                   {QStringLiteral("jid"), QStringLiteral("911@s")},
                                   {QStringLiteral("phone"), QStringLiteral("+91 1")},
                                   {QStringLiteral("push_name"), QStringLiteral("Harsh")},
                                   {QStringLiteral("about"), QStringLiteral("hello")},
                                   {QStringLiteral("avatar_path"), QStringLiteral("/self.jpg")}});
        daemon.setItem(QStringLiteral("preferences"),
                       QJsonObject{{QStringLiteral("id"), QStringLiteral("self")},
                                   {QStringLiteral("notifications_enabled"), true},
                                   {QStringLiteral("auto_download_photos"), false}});
        daemon.setItem(QStringLiteral("privacy"),
                       QJsonObject{{QStringLiteral("id"), QStringLiteral("self")},
                                   {QStringLiteral("last_seen"), QStringLiteral("contacts")},
                                   {QStringLiteral("read_receipts"), true}});
        daemon.setCollection(QStringLiteral("blocklist"),
                             {QJsonObject{{QStringLiteral("id"), QStringLiteral("a@s")},
                                          {QStringLiteral("jid"), QStringLiteral("a@s")},
                                          {QStringLiteral("name"), QStringLiteral("Alice")},
                                          {QStringLiteral("phone"), QStringLiteral("+91 2")}}});

        ProtocolController ctrl(m_path, nullptr);
        ctrl.start();
        QTRY_COMPARE(ctrl.currentUserName(), QStringLiteral("Harsh"));
        QCOMPARE(ctrl.currentUserStatusText(), QStringLiteral("hello"));
        QCOMPARE(ctrl.currentUserAvatarPath(), QStringLiteral("/self.jpg"));
        QTRY_COMPARE(ctrl.appPreferences().value(QStringLiteral("notifications_enabled")).toBool(), true);
        QCOMPARE(daemon.subscribeCountByView.value(QStringLiteral("self")), 1);
        QCOMPARE(daemon.subscribeCountByView.value(QStringLiteral("preferences")), 1);

        ctrl.openPrivacySettings();
        QTRY_COMPARE(ctrl.privacySettings().value(QStringLiteral("last_seen")).toString(),
                     QStringLiteral("contacts"));
        QCOMPARE(daemon.subscribeCountByView.value(QStringLiteral("privacy")), 1);

        ctrl.openBlockedContacts();
        QTRY_COMPARE(ctrl.blockedContactsModel()->rowCount(), 1);
        QCOMPARE(ctrl.blockedContactsModel()->data(ctrl.blockedContactsModel()->index(0, 0),
                                                   CollectionViewModel::ItemRole).toMap()
                     .value(QStringLiteral("name")).toString(),
                 QStringLiteral("Alice"));

        QSignalSpy commandSpy(&daemon, &FakeDaemon::commandReceived);
        ctrl.setPrivacyAudience(QStringLiteral("last_seen"), QStringLiteral("none"));
        QVERIFY(commandSpy.wait());
        QCOMPARE(daemon.lastCommandMethod, QStringLiteral("privacy.set"));
        QCOMPARE(daemon.lastCommandParams.value(QStringLiteral("category")).toString(),
                 QStringLiteral("last_seen"));
        QCOMPARE(daemon.lastCommandParams.value(QStringLiteral("value")).toString(), QStringLiteral("none"));
        // Ack did not mutate the row; the daemon still owns its current value.
        QCOMPARE(ctrl.privacySettings().value(QStringLiteral("last_seen")).toString(),
                 QStringLiteral("contacts"));

        commandSpy.clear();
        ctrl.setAppPreference(QStringLiteral("auto_download_photos"), true);
        QVERIFY(commandSpy.wait());
        QCOMPARE(daemon.lastCommandMethod, QStringLiteral("preferences.set"));
        QCOMPARE(daemon.lastCommandParams.value(QStringLiteral("auto_download_photos")).toBool(), true);
        QCOMPARE(ctrl.appPreferences().value(QStringLiteral("auto_download_photos")).toBool(), false);

        commandSpy.clear();
        ctrl.setProfileStatus(QString());
        QVERIFY(commandSpy.wait());
        QCOMPARE(daemon.lastCommandMethod, QStringLiteral("self.set_about"));
        QVERIFY(daemon.lastCommandParams.contains(QStringLiteral("text")));
        QCOMPARE(daemon.lastCommandParams.value(QStringLiteral("text")).toString(), QString());

        ctrl.closePrivacySettings();
        ctrl.closeBlockedContacts();
        QTRY_VERIFY(daemon.unsubscribedViews.contains(QStringLiteral("privacy")));
        QVERIFY(daemon.unsubscribedViews.contains(QStringLiteral("blocklist")));
    }

    // D6: sticker source/pack rows are generic daemon-sorted views, search is a
    // transient daemon query, and send/install/refresh are wire-only actions.
    void stickerPickerUsesViewsQueryAndAckOnlyActions()
    {
        FakeDaemon daemon(m_path);
        daemon.setItem(QStringLiteral("connection"), connectionItem(QStringLiteral("online")));
        daemon.setActiveChats({chatRow(QStringLiteral("a@s"), QStringLiteral("Alice"), QStringLiteral("1")),
                               chatRow(QStringLiteral("b@s"), QStringLiteral("Bob"), QStringLiteral("2"))});
        daemon.setCollection(QStringLiteral("stickers"),
                             {QJsonObject{{QStringLiteral("id"), QStringLiteral("s1")},
                                          {QStringLiteral("cache_key"), QStringLiteral("s1")},
                                          {QStringLiteral("local_path"), QStringLiteral("/s1.webp")},
                                          {QStringLiteral("is_favorite"), false}}});
        daemon.setCollection(QStringLiteral("sticker_packs"),
                             {QJsonObject{{QStringLiteral("id"), QStringLiteral("p1")},
                                          {QStringLiteral("name"), QStringLiteral("Pack One")},
                                          {QStringLiteral("installed"), true}}});
        daemon.setSearchStickers(
            {QJsonObject{{QStringLiteral("id"), QStringLiteral("s2")},
                         {QStringLiteral("cache_key"), QStringLiteral("s2")},
                         {QStringLiteral("local_path"), QStringLiteral("/s2.webp")}},
             QJsonObject{{QStringLiteral("id"), QStringLiteral("s1")},
                         {QStringLiteral("cache_key"), QStringLiteral("s1")},
                         {QStringLiteral("local_path"), QStringLiteral("/s1.webp")}}});

        ProtocolController ctrl(m_path, nullptr);
        ctrl.start();
        QTRY_VERIFY(!ctrl.chatsLoading());
        ctrl.selectChat(QStringLiteral("a@s"));
        auto *stickers = qobject_cast<ProtocolStickerController *>(ctrl.stickers());
        QVERIFY(stickers);
        stickers->activate();
        QTRY_COMPARE(stickers->stickerModel()->rowCount(), 1);
        QTRY_COMPARE(stickers->packModel()->rowCount(), 1);
        QCOMPARE(daemon.lastParamsByView.value(QStringLiteral("stickers"))
                     .value(QStringLiteral("source")).toString(),
                 QStringLiteral("recent"));
        QCOMPARE(daemon.lastParamsByView.value(QStringLiteral("stickers"))
                     .value(QStringLiteral("limit")).toInt(),
                 200);

        const int stickerSubs = daemon.subscribeCountByView.value(QStringLiteral("stickers"));
        stickers->beginFavoriteTracking();
        QTRY_COMPARE(daemon.subscribeCountByView.value(QStringLiteral("stickers")), stickerSubs + 1);
        stickers->endFavoriteTracking();
        QTRY_VERIFY(daemon.unsubscribedViews.contains(QStringLiteral("stickers")));

        QSignalSpy querySpy(&daemon, &FakeDaemon::queryReceived);
        stickers->search(QStringLiteral("wave"));
        QTRY_COMPARE_WITH_TIMEOUT(querySpy.count(), 1, 1000);
        QCOMPARE(daemon.lastQueryParams.value(QStringLiteral("search.stickers"))
                     .value(QStringLiteral("query")).toString(),
                 QStringLiteral("wave"));
        QTRY_COMPARE(stickers->stickerModel()->rowCount(), 2);
        // Query order is preserved exactly; no frontend sort/merge.
        QCOMPARE(stickers->stickerModel()->data(stickers->stickerModel()->index(0, 0),
                                                CollectionViewModel::ItemRole).toMap()
                     .value(QStringLiteral("id")).toString(),
                 QStringLiteral("s2"));

        QSignalSpy commandSpy(&daemon, &FakeDaemon::commandReceived);
        stickers->refreshStore();
        QVERIFY(commandSpy.wait());
        QCOMPARE(daemon.lastCommandMethod, QStringLiteral("sticker_packs.refresh"));

        commandSpy.clear();
        stickers->setPackInstalled(QStringLiteral("p1"), false);
        QVERIFY(commandSpy.wait());
        QCOMPARE(daemon.lastCommandMethod, QStringLiteral("sticker_pack.install"));
        QCOMPARE(daemon.lastCommandParams.value(QStringLiteral("installed")).toBool(), false);

        QSignalSpy sentSpy(stickers, &ProtocolStickerController::stickerSent);
        stickers->sendSticker(QStringLiteral("s1"), QStringLiteral("m1"));
        QVERIFY(sentSpy.wait());
        QCOMPARE(daemon.lastCommandMethod, QStringLiteral("send.sticker"));
        QCOMPARE(daemon.lastCommandParams.value(QStringLiteral("chat_id")).toString(), QStringLiteral("a@s"));
        QCOMPARE(daemon.lastCommandParams.value(QStringLiteral("cache_key")).toString(), QStringLiteral("s1"));
        QCOMPARE(daemon.lastCommandParams.value(QStringLiteral("reply_to")).toString(), QStringLiteral("m1"));

        // Alternate navigation routes through setSelectedChat too; sticker sends
        // must never retain the previous conversation id.
        ctrl.showMessageInChat(QStringLiteral("b@s"), QStringLiteral("m2"));
        sentSpy.clear();
        stickers->sendSticker(QStringLiteral("s2"));
        QVERIFY(sentSpy.wait());
        QCOMPARE(daemon.lastCommandParams.value(QStringLiteral("chat_id")).toString(), QStringLiteral("b@s"));

        stickers->deactivate();
        QCOMPARE(stickers->activeSource(), QStringLiteral("recents"));
        QCOMPARE(stickers->stickerModel()->rowCount(), 0);
        QCOMPARE(stickers->packModel()->rowCount(), 0);
        QTRY_VERIFY(daemon.unsubscribedViews.contains(QStringLiteral("sticker_packs")));
    }

    // D7: a `whatevr://chat/<id>` launch argument raises the window immediately
    // and holds the chat until the shell can show it — a notification click may
    // cold-start the app long before the daemon reports online.
    void deepLinkWaitsForShell()
    {
        FakeDaemon daemon(m_path);
        daemon.setItem(QStringLiteral("connection"), connectionItem(QStringLiteral("need_login")));
        daemon.setActiveChats({chatRow(QStringLiteral("a@s"), QStringLiteral("Alice"), QStringLiteral("1"))});

        ProtocolController ctrl(m_path, nullptr);
        ctrl.start();
        QTRY_VERIFY(ctrl.loginRequired());

        QSignalSpy activateSpy(&ctrl, &ProtocolController::activateWindowRequested);
        QSignalSpy openSpy(&ctrl, &ProtocolController::openChatRequested);
        ctrl.handleCommandLine({QStringLiteral("whatkevr"),
                                QStringLiteral("whatevr://chat/a%40s")});

        // The window comes up at once; the chat cannot be opened yet.
        QCOMPARE(activateSpy.count(), 1);
        QCOMPARE(openSpy.count(), 0);
        QVERIFY(ctrl.selectedChatId().isEmpty());

        // Login completes: the held link applies exactly once.
        daemon.setItem(QStringLiteral("connection"), connectionItem(QStringLiteral("online")));
        QTRY_COMPARE(openSpy.count(), 1);
        QCOMPARE(openSpy.first().first().toString(), QStringLiteral("a@s"));
        QCOMPARE(ctrl.selectedChatId(), QStringLiteral("a@s"));

        // Further state churn must not re-fire it.
        daemon.setItem(QStringLiteral("connection"), connectionItem(QStringLiteral("reconnecting")));
        QTest::qWait(50);
        QCOMPARE(openSpy.count(), 1);
    }

    // A plain launch (no URL) only raises the window; a malformed whatevr: URL
    // does the same rather than selecting a bogus chat.
    void commandLineWithoutDeepLinkRaisesWindow()
    {
        FakeDaemon daemon(m_path);
        daemon.setItem(QStringLiteral("connection"), connectionItem(QStringLiteral("online")));

        ProtocolController ctrl(m_path, nullptr);
        ctrl.start();
        QTRY_VERIFY(ctrl.shellVisible());

        QSignalSpy activateSpy(&ctrl, &ProtocolController::activateWindowRequested);
        QSignalSpy openSpy(&ctrl, &ProtocolController::openChatRequested);

        ctrl.handleCommandLine({QStringLiteral("whatkevr")});
        QCOMPARE(activateSpy.count(), 1);
        QCOMPARE(openSpy.count(), 0);

        ctrl.handleCommandLine({QStringLiteral("whatkevr"), QStringLiteral("whatevr://chat/")});
        QCOMPARE(activateSpy.count(), 2);
        QCOMPARE(openSpy.count(), 0);
        QVERIFY(ctrl.selectedChatId().isEmpty());
    }

    // D7: composer drafts are frontend-only state (rule 1). Blank text clears a
    // draft, and the store survives a controller restart via QSettings.
    void chatDraftsRoundTrip()
    {
        QStandardPaths::setTestModeEnabled(true);
        QSettings().remove(QStringLiteral("settings/drafts"));

        {
            ProtocolController ctrl(m_path, nullptr);
            QVERIFY(ctrl.chatDraft(QStringLiteral("a@s")).isEmpty());

            ctrl.setChatDraft(QStringLiteral("a@s"), QStringLiteral("half typed"));
            QCOMPARE(ctrl.chatDraft(QStringLiteral("a@s")), QStringLiteral("half typed"));
            // An empty chat id is ignored, not stored under "".
            ctrl.setChatDraft(QString(), QStringLiteral("orphan"));
            QVERIFY(ctrl.chatDraft(QString()).isEmpty());
        }

        {
            ProtocolController restarted(m_path, nullptr);
            QCOMPARE(restarted.chatDraft(QStringLiteral("a@s")), QStringLiteral("half typed"));
            // Sending clears the composer, which clears the draft.
            restarted.setChatDraft(QStringLiteral("a@s"), QStringLiteral("   "));
            QVERIFY(restarted.chatDraft(QStringLiteral("a@s")).isEmpty());
        }

        ProtocolController afterClear(m_path, nullptr);
        QVERIFY(afterClear.chatDraft(QStringLiteral("a@s")).isEmpty());

        QSettings().remove(QStringLiteral("settings/drafts"));
        QStandardPaths::setTestModeEnabled(false);
    }

    // The Backspace helper deletes whole grapheme clusters, so an emoji with a
    // skin-tone modifier goes in one keystroke rather than leaving half of it.
    void graphemeBoundaryWalksClusters()
    {
        ProtocolController ctrl(m_path, nullptr);
        const QString text = QStringLiteral("hi ") + QString::fromUtf8("\xF0\x9F\x91\x8D\xF0\x9F\x8F\xBD");
        QCOMPARE(ctrl.previousGraphemeBoundary(text, text.size()), 3);
        QCOMPARE(ctrl.previousGraphemeBoundary(text, 0), 0);
        // Out-of-range positions clamp instead of misbehaving.
        QCOMPARE(ctrl.previousGraphemeBoundary(text, text.size() + 10), 3);
    }

private:
    QTemporaryDir *m_dir = nullptr;
    QString m_path;
};

QTEST_GUILESS_MAIN(TestProtocolController)
#include "tst_protocolcontroller.moc"
