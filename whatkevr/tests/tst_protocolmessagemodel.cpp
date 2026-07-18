#include <QJsonArray>
#include <QJsonObject>
#include <QAbstractItemModelTester>
#include <QSignalSpy>
#include <QTest>

#include "collectionviewmodel.h"
#include "protocolmessagemodel.h"

using whatevr::proto::CollectionViewModel;

namespace
{
QJsonObject message(const QString &id, qint64 timestamp, const QString &direction = QStringLiteral("incoming"))
{
    return {
        {QStringLiteral("id"), id},
        {QStringLiteral("chat_id"), QStringLiteral("family@g.us")},
        {QStringLiteral("kind"), QStringLiteral("text")},
        {QStringLiteral("fallback"), QStringLiteral("hello")},
        {QStringLiteral("text"), QStringLiteral("hello")},
        {QStringLiteral("sender"), QJsonObject{
             {QStringLiteral("id"), QStringLiteral("alice@s.whatsapp.net")},
             {QStringLiteral("name"), QStringLiteral("Alice Smith")},
             {QStringLiteral("avatar_path"), QStringLiteral("/cache/alice.jpg")},
         }},
        {QStringLiteral("timestamp"), timestamp},
        {QStringLiteral("direction"), direction},
        {QStringLiteral("status"), QStringLiteral("delivered")},
    };
}

QVariant role(const ProtocolMessageModel &model, int row, ProtocolMessageModel::Role role)
{
    return model.data(model.index(row), role);
}
} // namespace

class TestProtocolMessageModel : public QObject
{
    Q_OBJECT

private Q_SLOTS:
    void mapsWholeMessageItems()
    {
        CollectionViewModel source;
        ProtocolMessageModel model(&source);
        QJsonObject item = message(QStringLiteral("m1"), 1'700'000'000, QStringLiteral("outgoing"));
        item.insert(QStringLiteral("text"), QStringLiteral("caption"));
        item.insert(QStringLiteral("edited"), true);
        item.insert(QStringLiteral("starred"), true);
        item.insert(QStringLiteral("media"), QJsonObject{
            {QStringLiteral("mime"), QStringLiteral("image/jpeg")},
            {QStringLiteral("width"), 640},
            {QStringLiteral("height"), 480},
            {QStringLiteral("thumbnail_path"), QStringLiteral("/cache/thumb.jpg")},
            {QStringLiteral("path"), QStringLiteral("/cache/photo.jpg")},
        });
        item.insert(QStringLiteral("kind"), QStringLiteral("image"));
        item.insert(QStringLiteral("reply_to"), QJsonObject{
            {QStringLiteral("message_id"), QStringLiteral("quoted")},
            {QStringLiteral("sender_name"), QStringLiteral("Bob")},
            {QStringLiteral("text"), QStringLiteral("earlier")},
            {QStringLiteral("kind"), QStringLiteral("text")},
            {QStringLiteral("direction"), QStringLiteral("incoming")},
        });
        item.insert(QStringLiteral("reactions"), QJsonArray{QJsonObject{
            {QStringLiteral("emoji"), QString::fromUtf8("\xF0\x9F\x91\x8D")},
            {QStringLiteral("sender_id"), QStringLiteral("me")},
            {QStringLiteral("sender_name"), QStringLiteral("Me")},
            {QStringLiteral("from_me"), true},
        }});

        source.onUpsert(QStringLiteral("0002"), item);

        QCOMPARE(model.rowCount(), 1);
        QCOMPARE(role(model, 0, ProtocolMessageModel::IdRole).toString(), QStringLiteral("m1"));
        QCOMPARE(role(model, 0, ProtocolMessageModel::SenderNameRole).toString(), QStringLiteral("Alice Smith"));
        QCOMPARE(role(model, 0, ProtocolMessageModel::SenderInitialsRole).toString(), QStringLiteral("AS"));
        QCOMPARE(role(model, 0, ProtocolMessageModel::TextRole).toString(), QStringLiteral("caption"));
        QCOMPARE(role(model, 0, ProtocolMessageModel::DirectionRole).toInt(), 2);
        QCOMPARE(role(model, 0, ProtocolMessageModel::StatusRole).toInt(), 3);
        QVERIFY(role(model, 0, ProtocolMessageModel::IsOutgoingRole).toBool());
        QCOMPARE(role(model, 0, ProtocolMessageModel::MediaKindRole).toString(), QStringLiteral("image"));
        QCOMPARE(role(model, 0, ProtocolMessageModel::MediaMimeTypeRole).toString(), QStringLiteral("image/jpeg"));
        QCOMPARE(role(model, 0, ProtocolMessageModel::MediaLocalPathRole).toString(), QStringLiteral("/cache/photo.jpg"));
        QCOMPARE(role(model, 0, ProtocolMessageModel::ReplyToMessageIdRole).toString(), QStringLiteral("quoted"));
        QCOMPARE(role(model, 0, ProtocolMessageModel::ReplyToMediaKindRole).toString(), QString());
        QVERIFY(role(model, 0, ProtocolMessageModel::IsEditedRole).toBool());
        QVERIFY(role(model, 0, ProtocolMessageModel::IsStarredRole).toBool());
        const QVariantMap reaction = role(model, 0, ProtocolMessageModel::ReactionsRole).toList().first().toMap();
        QCOMPARE(reaction.value(QStringLiteral("senderId")).toString(), QStringLiteral("me"));
        QVERIFY(reaction.value(QStringLiteral("fromMe")).toBool());
    }

    void preservesDaemonOrderAndAscendingGrouping()
    {
        CollectionViewModel source;
        ProtocolMessageModel model(&source);
        source.onUpsert(QStringLiteral("0002"), message(QStringLiteral("m2"), 1'700'000'060));
        source.onUpsert(QStringLiteral("0001"), message(QStringLiteral("m1"), 1'700'000'000));
        source.onUpsert(QStringLiteral("0003"), message(QStringLiteral("m3"), 1'700'000'120, QStringLiteral("outgoing")));

        QCOMPARE(model.allMessageIds(), QStringList({QStringLiteral("m1"), QStringLiteral("m2"), QStringLiteral("m3")}));
        QCOMPARE(model.messageIdAt(0), QStringLiteral("m1"));
        QCOMPARE(model.messageIdAt(2), QStringLiteral("m3"));
        QVERIFY(model.messageIdAt(3).isEmpty());
        QVERIFY(role(model, 0, ProtocolMessageModel::GroupStartRole).toBool());
        QVERIFY(!role(model, 0, ProtocolMessageModel::GroupEndRole).toBool());
        QVERIFY(!role(model, 1, ProtocolMessageModel::GroupStartRole).toBool());
        QVERIFY(role(model, 1, ProtocolMessageModel::GroupEndRole).toBool());
        QVERIFY(role(model, 2, ProtocolMessageModel::GroupStartRole).toBool());
        QVERIFY(role(model, 2, ProtocolMessageModel::GroupEndRole).toBool());
        QVERIFY(!role(model, 0, ProtocolMessageModel::DateSeparatorTextRole).toString().isEmpty());
        QVERIFY(role(model, 1, ProtocolMessageModel::DateSeparatorTextRole).toString().isEmpty());
    }

    void unknownKindUsesFallback()
    {
        CollectionViewModel source;
        ProtocolMessageModel model(&source);
        QJsonObject item = message(QStringLiteral("poll"), 1'700'000'000);
        item.insert(QStringLiteral("kind"), QStringLiteral("poll"));
        item.remove(QStringLiteral("text"));
        item.insert(QStringLiteral("fallback"), QStringLiteral("Poll: dinner?"));
        source.onUpsert(QStringLiteral("0001"), item);

        QCOMPARE(role(model, 0, ProtocolMessageModel::TextRole).toString(), QStringLiteral("Poll: dinner?"));
        QCOMPARE(role(model, 0, ProtocolMessageModel::MediaKindRole).toString(), QStringLiteral("poll"));
    }

    void mirrorsMoveRemoveAndReset()
    {
        CollectionViewModel source;
        ProtocolMessageModel model(&source);
        QAbstractItemModelTester sourceTester(&source, QAbstractItemModelTester::FailureReportingMode::QtTest);
        QAbstractItemModelTester modelTester(&model, QAbstractItemModelTester::FailureReportingMode::QtTest);
        source.onUpsert(QStringLiteral("a"), message(QStringLiteral("m1"), 1'700'000'000));
        source.onUpsert(QStringLiteral("b"), message(QStringLiteral("m2"), 1'700'000'060));
        QSignalSpy moveSpy(&model, &QAbstractItemModel::rowsMoved);

        source.onUpsert(QStringLiteral("z"), message(QStringLiteral("m1"), 1'700'000'000));
        QCOMPARE(moveSpy.count(), 1);
        QCOMPARE(model.allMessageIds(), QStringList({QStringLiteral("m2"), QStringLiteral("m1")}));

        source.onRemove(QStringLiteral("m2"));
        QCOMPARE(model.allMessageIds(), QStringList({QStringLiteral("m1")}));
        source.onReset();
        QCOMPARE(model.rowCount(), 0);
    }

    void helpersUseSourceChronology()
    {
        CollectionViewModel source;
        ProtocolMessageModel model(&source);
        QJsonObject first = message(QStringLiteral("m1"), 1'700'000'000);
        first.insert(QStringLiteral("text"), QStringLiteral("first"));
        QJsonObject second = message(QStringLiteral("m2"), 1'700'000'060, QStringLiteral("outgoing"));
        second.insert(QStringLiteral("text"), QStringLiteral("second"));
        source.onUpsert(QStringLiteral("0001"), first);
        source.onUpsert(QStringLiteral("0002"), second);

        const QString copied = model.copyTextForMessages({QStringLiteral("m2"), QStringLiteral("m1")});
        QVERIFY(copied.indexOf(QStringLiteral("first")) < copied.indexOf(QStringLiteral("second")));
        QCOMPARE(model.messageIdsForDay(QStringLiteral("m1")),
                 QStringList({QStringLiteral("m1"), QStringLiteral("m2")}));
        QCOMPARE(model.messageSnapshot(QStringLiteral("m2")).value(QStringLiteral("text")).toString(),
                 QStringLiteral("second"));
    }
};

QTEST_MAIN(TestProtocolMessageModel)
#include "tst_protocolmessagemodel.moc"
