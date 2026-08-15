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

    // D4c: whether a fetch is in flight is the message row's own
    // `media.downloading`; the `transfers` view supplies only the byte counters.
    // The split is what stops the two views, which are recomputed independently
    // daemon-side, from disagreeing (PROTOCOL.md, "Messages").
    void composesTheTransfersView()
    {
        CollectionViewModel source;
        CollectionViewModel transfers;
        ProtocolMessageModel model(&source);
        model.setTransfersSource(&transfers);
        const auto mediaRow = [](bool downloading, const QString &path, const QString &error) {
            QJsonObject media{
                {QStringLiteral("mime"), QStringLiteral("image/jpeg")},
                {QStringLiteral("thumbnail_path"), QStringLiteral("/cache/thumb.jpg")},
            };
            if (downloading) {
                media.insert(QStringLiteral("downloading"), true);
            }
            if (!path.isEmpty()) {
                media.insert(QStringLiteral("path"), path);
            }
            if (!error.isEmpty()) {
                media.insert(QStringLiteral("download_error"), error);
            }
            return media;
        };
        QJsonObject item = message(QStringLiteral("m1"), 1'700'000'000);
        item.insert(QStringLiteral("kind"), QStringLiteral("image"));
        item.insert(QStringLiteral("media"), mediaRow(false, {}, {}));
        source.onUpsert(QStringLiteral("0001"), item);

        QVERIFY(!role(model, 0, ProtocolMessageModel::MediaDownloadingRole).toBool());
        QCOMPARE(role(model, 0, ProtocolMessageModel::MediaDownloadProgressRole).toDouble(), -1.0);

        QJsonObject downloading = item;
        downloading.insert(QStringLiteral("media"), mediaRow(true, {}, {}));
        source.onUpsert(QStringLiteral("0001"), downloading);
        QVERIFY(role(model, 0, ProtocolMessageModel::MediaDownloadingRole).toBool());
        // Downloading with no known size stays indeterminate (-1), not 0%.
        QCOMPARE(role(model, 0, ProtocolMessageModel::MediaDownloadProgressRole).toDouble(), -1.0);

        QSignalSpy changed(&model, &QAbstractItemModel::dataChanged);
        transfers.onUpsert(QStringLiteral("m1"), QJsonObject{
            {QStringLiteral("id"), QStringLiteral("m1")},
            {QStringLiteral("message_id"), QStringLiteral("m1")},
            {QStringLiteral("direction"), QStringLiteral("download")},
            {QStringLiteral("received_bytes"), 512},
            {QStringLiteral("total_bytes"), 2048},
        });
        QCOMPARE(changed.count(), 1);
        QCOMPARE(changed.constFirst().at(2).value<QList<int>>(),
                 QList<int>{ProtocolMessageModel::MediaDownloadProgressRole});
        QCOMPARE(role(model, 0, ProtocolMessageModel::MediaDownloadProgressRole).toDouble(), 0.25);

        // The transfer row going away on its own does not end the download: the
        // bubble must not fall back to "never fetched" before the path lands.
        transfers.onRemove(QStringLiteral("m1"));
        QVERIFY(role(model, 0, ProtocolMessageModel::MediaDownloadingRole).toBool());
        QCOMPARE(role(model, 0, ProtocolMessageModel::MediaDownloadProgressRole).toDouble(), -1.0);

        // The message row is what ends it, carrying the outcome in the same
        // update.
        QJsonObject done = item;
        done.insert(QStringLiteral("media"), mediaRow(false, QStringLiteral("/cache/m1.jpg"), {}));
        source.onUpsert(QStringLiteral("0001"), done);
        QVERIFY(!role(model, 0, ProtocolMessageModel::MediaDownloadingRole).toBool());
        QCOMPARE(role(model, 0, ProtocolMessageModel::MediaLocalPathRole).toString(),
                 QStringLiteral("/cache/m1.jpg"));

        QJsonObject failed = item;
        failed.insert(QStringLiteral("media"), QJsonObject{
            {QStringLiteral("mime"), QStringLiteral("image/jpeg")},
            {QStringLiteral("download_error"), QStringLiteral("network unreachable")},
        });
        source.onUpsert(QStringLiteral("0001"), failed);
        QVERIFY(!role(model, 0, ProtocolMessageModel::MediaDownloadingRole).toBool());
        QCOMPARE(role(model, 0, ProtocolMessageModel::MediaDownloadErrorRole).toString(),
                 QStringLiteral("network unreachable"));
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

    // A bare message carries no `media`, `reply_to` or transfer sub-map, so the
    // roles that read those keys are the ones most likely to fall through to a
    // default-constructed QVariant. That reaches QML as `undefined`, and a
    // delegate's `required property string` stringifies it to the literal
    // "undefined" — which is exactly what shipped in DN9 once the delegate
    // stopped laundering roles through `String(model.x || "")`. Every role must
    // therefore hand back a valid, typed value for every row.
    void everyRoleIsTypedForASparseMessage()
    {
        CollectionViewModel source;
        ProtocolMessageModel model(&source);
        source.onUpsert(QStringLiteral("0001"), message(QStringLiteral("m1"), 1'700'000'000));
        QCOMPARE(model.rowCount(), 1);

        const QModelIndex index = model.index(0);
        const QHash<int, QByteArray> names = model.roleNames();
        for (auto it = names.constBegin(); it != names.constEnd(); ++it) {
            const QVariant value = model.data(index, it.key());
            QVERIFY2(value.isValid(),
                     qPrintable(QStringLiteral("role %1 returned an invalid QVariant")
                                    .arg(QString::fromUtf8(it.value()))));
            QVERIFY2(value.metaType() != QMetaType::fromType<std::nullptr_t>(),
                     qPrintable(QStringLiteral("role %1 returned null").arg(QString::fromUtf8(it.value()))));
        }

        // Spot-check the three that produced the visible "undefined undefined"
        // reply banner on every row.
        QVERIFY(role(model, 0, ProtocolMessageModel::ReplyToMessageIdRole).toString().isEmpty());
        QVERIFY(role(model, 0, ProtocolMessageModel::ReplyToSenderNameRole).toString().isEmpty());
        QVERIFY(role(model, 0, ProtocolMessageModel::ReplyToTextRole).toString().isEmpty());
    }

    // The daemon sends a one-line `fallback` for the chat list, replies and
    // notifications. A bubble that draws the media itself must not also print
    // it: that is where "🎥 Video (0:11)" used to appear under every clip.
    void mediaKindsShowTheirCaptionRatherThanTheFallback()
    {
        CollectionViewModel source;
        ProtocolMessageModel model(&source);

        const QStringList kinds{QStringLiteral("video"), QStringLiteral("gif"),
                                QStringLiteral("video_note"), QStringLiteral("voice"),
                                QStringLiteral("audio"), QStringLiteral("document"),
                                QStringLiteral("image"), QStringLiteral("sticker")};
        for (int i = 0; i < kinds.size(); ++i) {
            QJsonObject item = message(QStringLiteral("m%1").arg(i), 1'700'000'000 + i);
            item.insert(QStringLiteral("kind"), kinds.at(i));
            item.insert(QStringLiteral("fallback"), QStringLiteral("FALLBACK"));
            item.remove(QStringLiteral("text"));
            item.insert(QStringLiteral("media"), QJsonObject{
                {QStringLiteral("mime"), QStringLiteral("application/octet-stream")},
            });
            source.onUpsert(QStringLiteral("%1").arg(i, 4, 10, QLatin1Char('0')), item);
        }

        for (int i = 0; i < kinds.size(); ++i) {
            QCOMPARE(role(model, i, ProtocolMessageModel::TextRole).toString(), QString());
        }
    }

    void aCaptionOnMediaStillReachesTheBubble()
    {
        CollectionViewModel source;
        ProtocolMessageModel model(&source);
        QJsonObject item = message(QStringLiteral("m1"), 1'700'000'000);
        item.insert(QStringLiteral("kind"), QStringLiteral("video"));
        item.insert(QStringLiteral("fallback"), QStringLiteral("🎥 Video (0:11)"));
        item.insert(QStringLiteral("text"), QStringLiteral("look at this"));
        item.insert(QStringLiteral("media"), QJsonObject{
            {QStringLiteral("mime"), QStringLiteral("video/mp4")},
        });
        source.onUpsert(QStringLiteral("0001"), item);

        QCOMPARE(role(model, 0, ProtocolMessageModel::TextRole).toString(), QStringLiteral("look at this"));
    }

    // Tombstones and deletions have nothing to draw, so their label is all the
    // bubble has.
    void kindsWithoutMediaKeepTheirFallback()
    {
        CollectionViewModel source;
        ProtocolMessageModel model(&source);

        QJsonObject unsupported = message(QStringLiteral("m1"), 1'700'000'000);
        unsupported.insert(QStringLiteral("kind"), QStringLiteral("unsupported"));
        unsupported.insert(QStringLiteral("fallback"), QStringLiteral("Unsupported message"));
        unsupported.remove(QStringLiteral("text"));

        QJsonObject revoked = message(QStringLiteral("m2"), 1'700'000'001);
        revoked.insert(QStringLiteral("kind"), QStringLiteral("video"));
        revoked.insert(QStringLiteral("revoked"), true);
        revoked.insert(QStringLiteral("fallback"), QStringLiteral("This message was deleted"));
        revoked.insert(QStringLiteral("text"), QStringLiteral("stale caption"));
        revoked.insert(QStringLiteral("media"), QJsonObject{
            {QStringLiteral("mime"), QStringLiteral("video/mp4")},
        });

        source.onUpsert(QStringLiteral("0001"), unsupported);
        source.onUpsert(QStringLiteral("0002"), revoked);

        QCOMPARE(role(model, 0, ProtocolMessageModel::TextRole).toString(),
                 QStringLiteral("Unsupported message"));
        QCOMPARE(role(model, 1, ProtocolMessageModel::TextRole).toString(),
                 QStringLiteral("This message was deleted"));
    }
};

QTEST_MAIN(TestProtocolMessageModel)
#include "tst_protocolmessagemodel.moc"
