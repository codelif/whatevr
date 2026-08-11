// DN9 delegate-construction benchmark.
//
// The column slide hitches because materialising a `messages` window builds a
// ChatBubble per row, and a ChatBubble builds far more than the row shows. The
// numbers that matter are therefore per-row *object count* (what the scene
// graph and QML engine have to allocate, bind and lay out) and per-row
// construction wall time. Both are asserted against a budget so a future
// delegate regrowth fails the build instead of quietly returning the stutter.
//
// Object count is the primary metric: it is deterministic, machine-independent
// and is exactly the quantity Layer 1 of DN9 attacks. Wall time is recorded
// too, but its budget is deliberately loose — CI machines vary, and this test
// is built Debug (-O0) like the field-test build it exists to protect.

#include <QApplication>
#include <QElapsedTimer>
#include <QQmlComponent>
#include <QQmlEngine>
#include <QQuickItem>
#include <QQuickStyle>
#include <QQuickWindow>
#include <QRegularExpression>
#include <QDir>
#include <QStandardPaths>
#include <QTest>

#include "protocolcontroller.h"
#include "settings.h"

namespace
{

// One representative row per rendering path through the delegate.
struct Sample {
    const char *name;
    QVariantMap props;
    // Ceiling on the per-row object tree. Set just above what DN9 measured, so
    // the delegate regrowing is a build failure rather than a slow return of
    // the column-slide stutter. Raise deliberately, never reflexively.
    int maxObjects;
};

// ChatBubble declares every model role it renders as a `required property`, so
// a delegate cannot be built without all of them — exactly the contract
// ListView satisfies from the model. This mirrors that contract, which is also
// why the benchmark catches a role being renamed out from under the view.
QVariantMap baseProps()
{
    return {
        // View state (not model roles).
        {QStringLiteral("listWidth"), 900},
        {QStringLiteral("readMoreTextWidth"), 60},

        // Model roles.
        {QStringLiteral("messageId"), QStringLiteral("m0")},
        {QStringLiteral("timeText"), QStringLiteral("14:22")},
        {QStringLiteral("dateSeparatorText"), QString()},
        {QStringLiteral("status"), 0},
        {QStringLiteral("isOutgoing"), false},
        {QStringLiteral("senderName"), QStringLiteral("Aditi")},
        {QStringLiteral("senderAvatarLocalPath"), QString()},
        {QStringLiteral("senderInitials"), QStringLiteral("A")},
        {QStringLiteral("showSenderHeader"), false},
        {QStringLiteral("showSenderAvatar"), false},
        {QStringLiteral("showSenderGutter"), false},
        {QStringLiteral("groupStart"), true},
        {QStringLiteral("groupEnd"), true},
        {QStringLiteral("mediaKind"), QString()},
        {QStringLiteral("mediaMimeType"), QString()},
        {QStringLiteral("mediaLocalPath"), QString()},
        {QStringLiteral("mediaThumbnailLocalPath"), QString()},
        {QStringLiteral("mediaCacheKey"), QString()},
        {QStringLiteral("mediaWidth"), 0},
        {QStringLiteral("mediaHeight"), 0},
        {QStringLiteral("mediaAnimated"), false},
        {QStringLiteral("isRevoked"), false},
        {QStringLiteral("isEdited"), false},
        {QStringLiteral("isStarred"), false},
        {QStringLiteral("isPinned"), false},
        {QStringLiteral("mediaDownloading"), false},
        {QStringLiteral("mediaDownloadError"), QString()},
        {QStringLiteral("mediaDownloadProgress"), -1.0},
        {QStringLiteral("replyToMessageId"), QString()},
        {QStringLiteral("replyToSenderName"), QString()},
        {QStringLiteral("replyToText"), QString()},
        {QStringLiteral("replyToMediaKind"), QString()},
        {QStringLiteral("replyToMediaMimeType"), QString()},
        {QStringLiteral("replyToIsOutgoing"), false},
        {QStringLiteral("widestLineWidth"), 0.0},
        {QStringLiteral("lastLineWidth"), 0.0},
        {QStringLiteral("reactions"), QVariantList()},
        {QStringLiteral("text"), QString()},
        {QStringLiteral("textPreview"), QString()},
        {QStringLiteral("layoutText"), QString()},
        {QStringLiteral("layoutTextPreview"), QString()},
        {QStringLiteral("hasRichText"), false},
        {QStringLiteral("previewHasRichText"), false},
        {QStringLiteral("richText"), QString()},
        {QStringLiteral("previewRichText"), QString()},
        {QStringLiteral("emojiOnlyCount"), 0},
        {QStringLiteral("textTruncated"), false},
    };
}

QVariantMap withProps(QVariantMap base, const QVariantMap &extra)
{
    for (auto it = extra.cbegin(); it != extra.cend(); ++it) {
        base.insert(it.key(), it.value());
    }
    return base;
}

// Counts the whole object tree the delegate owns, including everything its
// Loaders instantiated.
int objectCount(QObject *root)
{
    return 1 + root->findChildren<QObject *>(Qt::FindChildrenRecursively).size();
}

} // namespace

class ChatBubblePerf : public QObject
{
    Q_OBJECT

private Q_SLOTS:
    void initTestCase();
    void cleanupTestCase();
    void delegateCost_data();
    void delegateCost();

private:
    QQuickWindow *m_window = nullptr;
    QQuickItem *m_host = nullptr;
    QQmlEngine *m_engine = nullptr;
    std::unique_ptr<Settings> m_settings;
    std::unique_ptr<ProtocolController> m_controller;
};

void ChatBubblePerf::initTestCase()
{
    // The QML singletons assert on a live instance, and both must exist before
    // the first `Whatevr.Settings` / `Whatevr.ProtocolController` resolution.
    // The socket path is deliberately dead: the controller must not need a
    // daemon to hand the delegate its preference/emoji-font reads.
    m_settings = std::make_unique<Settings>(nullptr);
    Settings::setInstance(m_settings.get());

    const QString deadSocket =
        QDir::temp().filePath(QStringLiteral("whatevr-dn9-benchmark-absent.sock"));
    m_controller = std::make_unique<ProtocolController>(deadSocket, nullptr);
    ProtocolController::setInstance(m_controller.get());

    m_engine = new QQmlEngine(this);
    m_window = new QQuickWindow();
    m_window->resize(1000, 800);
    m_host = new QQuickItem(m_window->contentItem());
    m_host->setWidth(900);
    m_host->setHeight(700);
}

void ChatBubblePerf::cleanupTestCase()
{
    delete m_window;
    m_window = nullptr;
    ProtocolController::setInstance(nullptr);
    Settings::setInstance(nullptr);
}

void ChatBubblePerf::delegateCost_data()
{
    QTest::addColumn<QVariantMap>("props");
    // Budgets are ceilings, not targets: they exist to catch regrowth.
    QTest::addColumn<int>("maxObjects");

    const QString shortBody = QStringLiteral("see you then");
    const QString longBody = QStringLiteral(
        "the whole point of this row is that it wraps across several lines so "
        "the body text actually has to lay out more than one line of content");

    const QList<Sample> samples = {
        {"plain-text-incoming",
         withProps(baseProps(),
                   {{QStringLiteral("messageId"), QStringLiteral("m1")},
                    {QStringLiteral("text"), shortBody},
                    {QStringLiteral("layoutText"), shortBody},
                    {QStringLiteral("status"), 4}}), 60},
        {"plain-text-outgoing",
         withProps(baseProps(),
                   {{QStringLiteral("messageId"), QStringLiteral("m2")},
                    {QStringLiteral("text"), shortBody},
                    {QStringLiteral("layoutText"), shortBody},
                    {QStringLiteral("isOutgoing"), true},
                    {QStringLiteral("status"), 4}}), 68},
        {"multiline-text",
         withProps(baseProps(),
                   {{QStringLiteral("messageId"), QStringLiteral("m3")},
                    {QStringLiteral("text"), longBody},
                    {QStringLiteral("layoutText"), longBody},
                    {QStringLiteral("status"), 3}}), 60},
        {"text-with-reply",
         withProps(baseProps(),
                   {{QStringLiteral("messageId"), QStringLiteral("m4")},
                    {QStringLiteral("text"), shortBody},
                    {QStringLiteral("layoutText"), shortBody},
                    {QStringLiteral("replyToMessageId"), QStringLiteral("m1")},
                    {QStringLiteral("replyToSenderName"), QStringLiteral("Aditi")},
                    {QStringLiteral("replyToText"), shortBody}}), 90},
        {"text-with-sender-header",
         withProps(baseProps(),
                   {{QStringLiteral("messageId"), QStringLiteral("m5")},
                    {QStringLiteral("text"), shortBody},
                    {QStringLiteral("layoutText"), shortBody},
                    {QStringLiteral("showSenderHeader"), true},
                    {QStringLiteral("showSenderAvatar"), true},
                    {QStringLiteral("showSenderGutter"), true}}), 94},
        {"image",
         withProps(baseProps(),
                   {{QStringLiteral("messageId"), QStringLiteral("m6")},
                    {QStringLiteral("mediaKind"), QStringLiteral("image")},
                    {QStringLiteral("mediaMimeType"), QStringLiteral("image/jpeg")},
                    {QStringLiteral("mediaWidth"), 1280},
                    {QStringLiteral("mediaHeight"), 720}}), 122},
        {"sticker",
         withProps(baseProps(),
                   {{QStringLiteral("messageId"), QStringLiteral("m7")},
                    {QStringLiteral("mediaKind"), QStringLiteral("sticker")},
                    {QStringLiteral("mediaMimeType"), QStringLiteral("image/webp")}}), 152},
    };

    for (const Sample &s : samples) {
        QTest::newRow(s.name) << s.props << s.maxObjects;
    }
}

void ChatBubblePerf::delegateCost()
{
    QFETCH(QVariantMap, props);
    QFETCH(int, maxObjects);

    QQmlComponent component(
        m_engine, QUrl(QStringLiteral("qrc:/qt/qml/Whatevr/qml/components/ChatBubble.qml")));
    QVERIFY2(!component.isError(), qPrintable(component.errorString()));

    // Warm the component cache and the shared type data so the measured run
    // times construction, not first-time compilation.
    {
        std::unique_ptr<QObject> warm(component.createWithInitialProperties(props));
        QVERIFY2(warm, qPrintable(component.errorString()));
        if (auto *item = qobject_cast<QQuickItem *>(warm.get())) {
            item->setParentItem(m_host);
            // Touch the geometry so every layout binding is forced to evaluate.
            (void)item->height();
        }
    }

    constexpr int kRows = 120;
    QList<QObject *> built;
    built.reserve(kRows);

    QElapsedTimer timer;
    timer.start();
    for (int i = 0; i < kRows; ++i) {
        QObject *obj = component.createWithInitialProperties(props);
        QVERIFY2(obj, qPrintable(component.errorString()));
        if (auto *item = qobject_cast<QQuickItem *>(obj)) {
            item->setParentItem(m_host);
            (void)item->height();
        }
        built.append(obj);
    }
    const qint64 elapsedUs = timer.nsecsElapsed() / 1000;

    const int objects = objectCount(built.first());
    const double usPerRow = double(elapsedUs) / kRows;

    qInfo("DN9 %-24s objects/row=%3d  construct=%7.1f us/row", QTest::currentDataTag(), objects,
          usPerRow);

    // WHATEVR_DN9_DUMP=1 prints the per-row object tree as a class histogram,
    // which is how you find what a row is actually paying for.
    if (qEnvironmentVariableIsSet("WHATEVR_DN9_DUMP")) {
        QMap<QString, int> histogram;
        const QList<QObject *> all =
            built.first()->findChildren<QObject *>(Qt::FindChildrenRecursively);
        for (const QObject *o : all) {
            QString cls = QString::fromLatin1(o->metaObject()->className());
            cls.remove(QLatin1String("QQuick"));
            cls.replace(QRegularExpression(QStringLiteral("_QMLTYPE_\\d+")), QString());
            cls.replace(QRegularExpression(QStringLiteral("_QML_\\d+")), QString());
            histogram[cls] += 1;
        }
        QStringList lines;
        for (auto it = histogram.cbegin(); it != histogram.cend(); ++it) {
            lines << QStringLiteral("%1x %2").arg(it.value(), 3).arg(it.key());
        }
        qInfo().noquote() << "DN9 tree" << QTest::currentDataTag() << "\n  "
                          << lines.join(QStringLiteral("\n  "));
    }

    qDeleteAll(built);

    QVERIFY2(objects <= maxObjects,
             qPrintable(QStringLiteral("%1: %2 objects per row exceeds the budget of %3 — the "
                                       "delegate regrew; see MIGRATION.md DN9")
                            .arg(QLatin1StringView(QTest::currentDataTag()))
                            .arg(objects)
                            .arg(maxObjects)));
}

int main(int argc, char *argv[])
{
    qputenv("QT_QPA_PLATFORM", "offscreen");
    QStandardPaths::setTestModeEnabled(true);
    QQuickStyle::setStyle(QStringLiteral("org.kde.desktop"));

    QApplication app(argc, argv);
    ChatBubblePerf tc;
    return QTest::qExec(&tc, argc, argv);
}

#include "tst_chatbubbleperf.moc"
