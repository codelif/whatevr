#include <QJsonArray>
#include <QJsonObject>
#include <QAbstractItemModelTester>
#include <QSignalSpy>
#include <QTest>

#include <cmath>

#include "collectionviewmodel.h"
#include "messagemarkup.h"
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
        item.insert(QStringLiteral("forwarded"), true);
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
        QVERIFY(role(model, 0, ProtocolMessageModel::IsForwardedRole).toBool());
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

    // A line ending in an emoji is measured as wide as it is drawn.
    //
    // The rich-text path enlarges inline emoji, and the width oracle measured
    // the whole line in the plain body font, so it reported a line narrower than
    // what lands on screen. The bubble is built from that width, so a message
    // like "Ok <emoji>" got a plate too narrow for its own last line and its
    // timestamp dropped onto a line of its own, while a plain message of the
    // same length kept the timestamp inline. Same words, different answer,
    // purely because one of them carried an emoji.
    void anEmojiIsMeasuredAtTheSizeItIsDrawn()
    {
        CollectionViewModel source;
        ProtocolMessageModel model(&source);

        // An explicit font, so the reference advances below are the same ones
        // the model is measuring with.
        QFont body;
        body.setPointSizeF(11.0);
        model.setBodyMetricsFont(body);

        const QString emoji = QStringLiteral("\U0001F4AF"); // the hundred-points emoji
        const QFontMetricsF bodyMetrics(body);
        QFont enlarged = body;
        enlarged.setPointSizeF(body.pointSizeF() * whatevr::util::inlineEmojiScale());
        const QFontMetricsF enlargedMetrics(enlarged);
        if (enlargedMetrics.horizontalAdvance(emoji) - bodyMetrics.horizontalAdvance(emoji) < 1.0) {
            QSKIP("no emoji glyph here that changes width with its size");
        }

        const auto lastLineWidth = [&](const QString &id, const QString &text) {
            QJsonObject row = message(id, 1'700'000'000);
            row.insert(QStringLiteral("text"), text);
            row.insert(QStringLiteral("fallback"), text);
            source.onUpsert(id, row);
            const int i = model.indexOf(id);
            Q_ASSERT(i >= 0);
            return model.data(model.index(i, 0), ProtocolMessageModel::LastLineWidthRole).toReal();
        };

        const qreal measured = lastLineWidth(QStringLiteral("emoji"), QStringLiteral("Ok ") + emoji);
        // Exactly what the old oracle reported, rounding included, so the
        // comparison is about the emoji's size and not about the +1 the oracle
        // adds to every line it measures.
        const qreal unscaled =
            std::ceil(bodyMetrics.horizontalAdvance(QStringLiteral("Ok ") + emoji)) + 1;

        QVERIFY2(whatevr::util::isEmojiGraphemeCluster(emoji),
                 "the fixture is not an emoji, so this would measure ordinary text");
        QVERIFY2(measured > unscaled,
                 qPrintable(QStringLiteral("the emoji line measured %1, no more than the %2 the "
                                           "body font alone gives: the enlargement is ignored")
                                .arg(measured)
                                .arg(unscaled)));

        // A line with no emoji is untouched, so ordinary text keeps measuring
        // exactly as it did.
        const qreal plain = lastLineWidth(QStringLiteral("plain"), QStringLiteral("Wahi na"));
        QCOMPARE(plain, std::ceil(bodyMetrics.horizontalAdvance(QStringLiteral("Wahi na"))) + 1);
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

    // The bubble decides whether to offer a download from `hasMedia`, not from
    // the kind. A structured kind (a poll, a contact card, a system event) has
    // a kind and no bytes behind it, and inferring one from the other put a
    // download button over nothing and fired media.download on every scroll-in.
    void hasMediaFollowsTheDaemonNotTheKind()
    {
        CollectionViewModel source;
        ProtocolMessageModel model(&source);

        QJsonObject poll = message(QStringLiteral("m1"), 1'700'000'000);
        poll.insert(QStringLiteral("kind"), QStringLiteral("poll"));
        poll.insert(QStringLiteral("fallback"), QStringLiteral("📊 Poll: dinner?"));
        poll.remove(QStringLiteral("text"));

        QJsonObject photo = message(QStringLiteral("m2"), 1'700'000'001);
        photo.insert(QStringLiteral("kind"), QStringLiteral("image"));
        photo.insert(QStringLiteral("media"), QJsonObject{
            {QStringLiteral("mime"), QStringLiteral("image/jpeg")},
        });

        source.onUpsert(QStringLiteral("0001"), poll);
        source.onUpsert(QStringLiteral("0002"), photo);

        QCOMPARE(role(model, 0, ProtocolMessageModel::HasMediaRole).toBool(), false);
        QCOMPARE(role(model, 1, ProtocolMessageModel::HasMediaRole).toBool(), true);
        // A kind with no media of its own still has its label to render.
        QCOMPARE(role(model, 0, ProtocolMessageModel::TextRole).toString(),
                 QStringLiteral("📊 Poll: dinner?"));
    }

    // A kind that draws itself must not also print the daemon's one-line
    // summary: a contact card that did grew a caption repeating its own name.
    // The fallback is for kinds this build cannot draw, and nothing else.
    void kindsThatDrawThemselvesDoNotPrintTheirFallback()
    {
        CollectionViewModel source;
        ProtocolMessageModel model(&source);

        QJsonObject contact = message(QStringLiteral("m1"), 1'700'000'000);
        contact.insert(QStringLiteral("kind"), QStringLiteral("contact"));
        contact.insert(QStringLiteral("fallback"), QStringLiteral("👤 Contact: Aditi Rao"));
        contact.remove(QStringLiteral("text"));
        contact.insert(QStringLiteral("contacts"), QJsonObject{
            {QStringLiteral("cards"), QJsonArray{QJsonObject{
                {QStringLiteral("display_name"), QStringLiteral("Aditi Rao")},
            }}},
        });

        QJsonObject location = message(QStringLiteral("m2"), 1'700'000'001);
        location.insert(QStringLiteral("kind"), QStringLiteral("location"));
        location.insert(QStringLiteral("fallback"), QStringLiteral("📍 Location: Cafe Noir"));
        location.remove(QStringLiteral("text"));
        location.insert(QStringLiteral("location"), QJsonObject{
            {QStringLiteral("lat"), 12.9716}, {QStringLiteral("lng"), 77.5946},
        });

        // Caught in the running app, not here: a group invite drew its card and
        // then printed "👥 Group invite: Wow3" underneath it, because adding a
        // card kind means adding its payload key here too and nothing said so.
        QJsonObject invite = message(QStringLiteral("m4"), 1'700'000'003);
        invite.insert(QStringLiteral("kind"), QStringLiteral("group_invite"));
        invite.insert(QStringLiteral("fallback"), QStringLiteral("👥 Group invite: Wow3"));
        invite.remove(QStringLiteral("text"));
        invite.insert(QStringLiteral("invite"), QJsonObject{
            {QStringLiteral("group_jid"), QStringLiteral("120@g.us")},
            {QStringLiteral("subject"), QStringLiteral("Wow3")},
        });

        QJsonObject album = message(QStringLiteral("m5"), 1'700'000'004);
        album.insert(QStringLiteral("kind"), QStringLiteral("album"));
        album.insert(QStringLiteral("fallback"), QStringLiteral("🖼️ Album: 3 photos"));
        album.remove(QStringLiteral("text"));
        album.insert(QStringLiteral("album"), QJsonObject{
            {QStringLiteral("items"), QJsonArray{QJsonObject{
                {QStringLiteral("id"), QStringLiteral("m5-p1")},
                {QStringLiteral("kind"), QStringLiteral("image")},
            }}},
        });

        // A kind from a newer daemon, whose payload this build has never heard
        // of, must still say something: that is what fallback is for.
        QJsonObject future = message(QStringLiteral("m3"), 1'700'000'002);
        future.insert(QStringLiteral("kind"), QStringLiteral("hologram"));
        future.insert(QStringLiteral("fallback"), QStringLiteral("🪩 Hologram"));
        future.remove(QStringLiteral("text"));
        future.insert(QStringLiteral("hologram"), QJsonObject{{QStringLiteral("shimmer"), 11}});

        source.onUpsert(QStringLiteral("0001"), contact);
        source.onUpsert(QStringLiteral("0002"), location);
        source.onUpsert(QStringLiteral("0003"), future);
        source.onUpsert(QStringLiteral("0004"), invite);
        source.onUpsert(QStringLiteral("0005"), album);

        QCOMPARE(role(model, 0, ProtocolMessageModel::TextRole).toString(), QString());
        QCOMPARE(role(model, 1, ProtocolMessageModel::TextRole).toString(), QString());
        QCOMPARE(role(model, 2, ProtocolMessageModel::TextRole).toString(),
                 QStringLiteral("🪩 Hologram"));
        QCOMPARE(role(model, 3, ProtocolMessageModel::TextRole).toString(), QString());
        QCOMPARE(role(model, 4, ProtocolMessageModel::TextRole).toString(), QString());
    }

    // An album's pictures are messages with real ids that occupy no row of
    // their own, and everything that acts on a message takes an id: the viewer,
    // Save As, Forward, the context menu. A lookup that only knew about rows
    // would find nothing for any of them.
    void aPictureInsideAnAlbumIsStillFoundByItsId()
    {
        CollectionViewModel source;
        ProtocolMessageModel model(&source);

        QJsonObject album = message(QStringLiteral("al-1"), 1'700'000'000);
        album.insert(QStringLiteral("kind"), QStringLiteral("album"));
        album.remove(QStringLiteral("text"));
        album.insert(QStringLiteral("album"), QJsonObject{
            {QStringLiteral("items"), QJsonArray{QJsonObject{
                {QStringLiteral("id"), QStringLiteral("al-1-p1")},
                {QStringLiteral("kind"), QStringLiteral("image")},
                {QStringLiteral("timestamp"), 1'700'000'001},
                {QStringLiteral("media"), QJsonObject{
                    {QStringLiteral("path"), QStringLiteral("/cache/p1.jpg")},
                    {QStringLiteral("filename"), QStringLiteral("p1.jpg")},
                    {QStringLiteral("width"), 1200},
                    {QStringLiteral("height"), 900},
                }},
            }}},
        });
        source.onUpsert(QStringLiteral("0001"), album);

        const QVariantMap tile = model.messageSnapshot(QStringLiteral("al-1-p1"));
        QCOMPARE(tile.value(QStringLiteral("messageId")).toString(), QStringLiteral("al-1-p1"));
        QCOMPARE(tile.value(QStringLiteral("mediaKind")).toString(), QStringLiteral("image"));
        QCOMPARE(tile.value(QStringLiteral("mediaLocalPath")).toString(),
                 QStringLiteral("/cache/p1.jpg"));
        QCOMPARE(tile.value(QStringLiteral("mediaFileName")).toString(), QStringLiteral("p1.jpg"));
        QCOMPARE(tile.value(QStringLiteral("timestampUnix")).toLongLong(), 1'700'000'001LL);

        // And the album still carries its pictures, which is what lets opening
        // one of them open the set rather than a lone photo.
        const QVariantMap set = model.messageSnapshot(QStringLiteral("al-1"));
        QCOMPARE(set.value(QStringLiteral("album")).toMap()
                     .value(QStringLiteral("items")).toList().size(), 1);

        QVERIFY(model.messageSnapshot(QStringLiteral("nobody")).isEmpty());
    }

    // A caption on a kind that draws itself still belongs to the bubble.
    void aCaptionOnACardStillReachesTheBubble()
    {
        CollectionViewModel source;
        ProtocolMessageModel model(&source);
        QJsonObject item = message(QStringLiteral("m1"), 1'700'000'000);
        item.insert(QStringLiteral("kind"), QStringLiteral("location"));
        item.insert(QStringLiteral("fallback"), QStringLiteral("meet me here"));
        item.insert(QStringLiteral("text"), QStringLiteral("meet me here"));
        item.insert(QStringLiteral("location"), QJsonObject{
            {QStringLiteral("lat"), 12.9716}, {QStringLiteral("lng"), 77.5946},
        });
        source.onUpsert(QStringLiteral("0001"), item);

        QCOMPARE(role(model, 0, ProtocolMessageModel::TextRole).toString(),
                 QStringLiteral("meet me here"));
    }

    void keptRidesTheItem()
    {
        CollectionViewModel source;
        ProtocolMessageModel model(&source);

        QJsonObject plain = message(QStringLiteral("m1"), 1'700'000'000);
        QJsonObject kept = message(QStringLiteral("m2"), 1'700'000'001);
        kept.insert(QStringLiteral("kept"), true);

        source.onUpsert(QStringLiteral("0001"), plain);
        source.onUpsert(QStringLiteral("0002"), kept);

        QCOMPARE(role(model, 0, ProtocolMessageModel::IsKeptRole).toBool(), false);
        QCOMPARE(role(model, 1, ProtocolMessageModel::IsKeptRole).toBool(), true);
    }

    // The shaped-text cache outlives a model reset now, so that returning to a
    // chat does not re-parse and re-measure every line of it. That is only safe
    // because an entry checks the words it was built from before it is handed
    // back: a message can come back under its own id with different text, both
    // from an edit and from a resync, and serving the old shaping would draw
    // the old message.
    void reshapingHappensWhenTheWordsChangeAndNotWhenTheChatDoes()
    {
        CollectionViewModel source;
        ProtocolMessageModel model(&source);

        QJsonObject item = message(QStringLiteral("m1"), 1'700'000'000);
        item.insert(QStringLiteral("text"), QStringLiteral("the original words"));
        source.onUpsert(QStringLiteral("0001"), item);
        QCOMPARE(role(model, 0, ProtocolMessageModel::TextRole).toString(),
                 QStringLiteral("the original words"));
        const qreal originalWidth = role(model, 0, ProtocolMessageModel::WidestLineWidthRole).toReal();
        QVERIFY(originalWidth > 0);

        // A reset and a refill with the same message: the cache should carry
        // over, and the row must still read correctly.
        source.onReset();
        source.onUpsert(QStringLiteral("0001"), item);
        QCOMPARE(role(model, 0, ProtocolMessageModel::TextRole).toString(),
                 QStringLiteral("the original words"));
        QCOMPARE(role(model, 0, ProtocolMessageModel::WidestLineWidthRole).toReal(), originalWidth);

        // Now the same id comes back saying something much longer. The measured
        // width has to follow it, which it cannot do from a stale entry.
        QJsonObject edited = item;
        edited.insert(QStringLiteral("text"),
                      QStringLiteral("the original words, plus a great many more of them so that "
                                     "the line is unmistakably wider than it was before"));
        edited.insert(QStringLiteral("edited"), true);
        source.onReset();
        source.onUpsert(QStringLiteral("0001"), edited);

        QCOMPARE(role(model, 0, ProtocolMessageModel::TextRole).toString(),
                 edited.value(QStringLiteral("text")).toString());
        QVERIFY2(role(model, 0, ProtocolMessageModel::WidestLineWidthRole).toReal() > originalWidth,
                 "the edited body was measured with the shaping of the text it replaced");
    }

    // The transcript is held newest-first so a BottomToTop view can pin its
    // live edge at row 0. Everything that reasons about neighbours has to read
    // the same way round afterwards, or a day changes on the wrong row and a
    // sender group opens at the wrong end.
    void newestFirstPutsTheLiveEdgeAtRowZero()
    {
        CollectionViewModel source;
        source.setReverseOrder(true);
        ProtocolMessageModel model(&source);

        // Three days, oldest to newest, delivered in wire order.
        const qint64 day = 24 * 60 * 60;
        source.onUpsert(QStringLiteral("0001"), message(QStringLiteral("old"), 1'700'000'000));
        source.onUpsert(QStringLiteral("0002"), message(QStringLiteral("mid"), 1'700'000'000 + day));
        source.onUpsert(QStringLiteral("0003"), message(QStringLiteral("new"), 1'700'000'000 + day * 2));

        QCOMPARE(model.rowCount(), 3);
        QCOMPARE(model.messageIdAt(0), QStringLiteral("new"));
        QCOMPARE(model.messageIdAt(2), QStringLiteral("old"));

        // The ends by time, not by index. These are what callers must use.
        QCOMPARE(model.oldestMessageId(), QStringLiteral("old"));
        QCOMPARE(model.newestMessageId(), QStringLiteral("new"));

        // Older is downward now, newer is upward, and both run off the end.
        QCOMPARE(model.olderRow(0), 1);
        QCOMPARE(model.newerRow(0), -1);
        QCOMPARE(model.olderRow(2), -1);
        QCOMPARE(model.newerRow(2), 1);

        // Every row here is a different day, so every row starts one. Read the
        // wrong way this would be true for two rows and false for the third.
        for (int row = 0; row < 3; ++row) {
            QVERIFY2(role(model, row, ProtocolMessageModel::DateSeparatorTextRole).toString().length() > 0,
                     qPrintable(QStringLiteral("row %1 carries no day separator").arg(row)));
        }

        // And a conversation still reads forwards when it leaves the model.
        QCOMPARE(model.allMessageIds(),
                 (QStringList{QStringLiteral("old"), QStringLiteral("mid"), QStringLiteral("new")}));
    }

    // The same rows, the same grouping questions, held the other way up. A
    // sender run must open at its oldest message and close at its newest
    // whichever end of the list that happens to be.
    void senderRunsOpenAtTheirOldestMessageEitherWayRound()
    {
        for (const bool reversed : {false, true}) {
            CollectionViewModel source;
            source.setReverseOrder(reversed);
            ProtocolMessageModel model(&source);

            // Two from Alice, then one from Bob, within the grouping window.
            QJsonObject second = message(QStringLiteral("a2"), 1'700'000'060);
            QJsonObject third = message(QStringLiteral("b1"), 1'700'000'120);
            third.insert(QStringLiteral("sender"), QJsonObject{
                {QStringLiteral("id"), QStringLiteral("bob@s.whatsapp.net")},
                {QStringLiteral("name"), QStringLiteral("Bob")},
            });
            source.onUpsert(QStringLiteral("0001"), message(QStringLiteral("a1"), 1'700'000'000));
            source.onUpsert(QStringLiteral("0002"), second);
            source.onUpsert(QStringLiteral("0003"), third);

            const auto rowOf = [&](const QString &id) { return model.indexOf(id); };
            const auto groupStart = [&](const QString &id) {
                return role(model, rowOf(id), ProtocolMessageModel::GroupStartRole).toBool();
            };
            const auto groupEnd = [&](const QString &id) {
                return role(model, rowOf(id), ProtocolMessageModel::GroupEndRole).toBool();
            };

            const QString what = reversed ? QStringLiteral("newest-first") : QStringLiteral("oldest-first");
            QVERIFY2(groupStart(QStringLiteral("a1")), qPrintable(what + QStringLiteral(": a1 should open Alice's run")));
            QVERIFY2(!groupStart(QStringLiteral("a2")), qPrintable(what + QStringLiteral(": a2 should continue it")));
            QVERIFY2(groupEnd(QStringLiteral("a2")), qPrintable(what + QStringLiteral(": a2 should close it")));
            QVERIFY2(groupStart(QStringLiteral("b1")), qPrintable(what + QStringLiteral(": b1 should open Bob's run")));
        }
    }
};

QTEST_MAIN(TestProtocolMessageModel)
#include "tst_protocolmessagemodel.moc"
