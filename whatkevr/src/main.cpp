#include <QApplication>
#include <QLoggingCategory>
#include <QPixmapCache>
#include <QQmlApplicationEngine>
#include <QQmlContext>
#include <QQuickStyle>

#include <KAboutData>
#include <KDBusService>
#include <KLocalizedContext>
#include <KLocalizedString>

#include "app/appcontroller.h"
#include "app/settings.h"
#include "version.h"

namespace
{
QtMessageHandler g_previousMessageHandler = nullptr;

// KirigamiAddons' ConfigurationView builds its module pages (FormCard
// ScrollablePages, the formcard AboutPage's license sheets, MessageDialog)
// before the settings window's overlay/flickable are resolved, so a handful of
// framework bindings transiently read a null `parent`/`flickable`. These are
// upstream warnings we cannot fix in QML; drop only these specific Kirigami /
// KirigamiAddons null-property TypeErrors and pass everything else through
// untouched — our own QML errors still surface.
void filterKirigamiNullPropertyWarnings(QtMsgType type, const QMessageLogContext &context, const QString &message)
{
    if (type == QtWarningMsg
        && message.contains(QLatin1String("Cannot read property"))
        && message.contains(QLatin1String("of null"))
        && message.contains(QLatin1String("org/kde/kirigami"))) {
        return;
    }
    if (g_previousMessageHandler) {
        g_previousMessageHandler(type, context, message);
    }
}
}

int main(int argc, char *argv[])
{
    g_previousMessageHandler = qInstallMessageHandler(filterKirigamiNullPropertyWarnings);
    KLocalizedString::setApplicationDomain("whatkevr");
    QQuickStyle::setStyle(QStringLiteral("org.kde.desktop"));
    // Some incoming media carries malformed ICC profile descriptions; Qt warns
    // on every decode and there is nothing we can do about the files.
    QLoggingCategory::setFilterRules(QStringLiteral("qt.gui.icc.warning=false"));

    QApplication app(argc, argv);
    // The default 10 MB pixmap cache is easily exhausted by a screenful of
    // stickers and image thumbnails, which would evict and force re-decodes
    // while scrolling. Give the on-screen media working set room to stay warm.
    QPixmapCache::setCacheLimit(128 * 1024);
    app.setApplicationName(QStringLiteral("Whatevr"));
    app.setDesktopFileName(QStringLiteral("in.codelif.Whatevr"));
    app.setOrganizationDomain(QStringLiteral("codelif.in"));

    KAboutData aboutData(QStringLiteral("in.codelif.Whatevr"),
                         i18nc("@title", "Whatevr"),
                         QStringLiteral(WHATEVR_VERSION_STRING));
    aboutData.setDesktopFileName(QStringLiteral("in.codelif.Whatevr"));
    aboutData.setShortDescription(
        i18nc("@info", "Kirigami frontend bootstrap for the whatevrd background daemon."));
    aboutData.setHomepage(QStringLiteral("https://github.com/codelif/whatevr"));
    aboutData.setBugAddress("https://github.com/codelif/whatevr/issues");
    aboutData.setLicense(KAboutLicense::BSD_3_Clause);
    aboutData.setCopyrightStatement(i18nc("@info:credit", "Copyright (c) 2026 Harsh Sharma"));
    aboutData.addAuthor(QStringLiteral("Harsh Sharma"));
    KAboutData::setApplicationData(aboutData);

    QQmlApplicationEngine engine;
    engine.rootContext()->setContextObject(new KLocalizedContext(&engine));

    // Constructed before AppController so the models it creates can read the
    // shared Settings instance (drafts persistence, default skin tone).
    Settings settings;
    Settings::setInstance(&settings);

    AppController appController;
    AppController::setInstance(&appController);

    // Single-instance: a second launch (e.g. clicking a notification, which runs
    // `whatkevr whatevr://chat/<id>` via the desktop scheme handler) forwards its
    // command line to the running instance through activateRequested instead of
    // starting a new window.
    KDBusService service(KDBusService::Unique);
    QObject::connect(&service,
                     &KDBusService::activateRequested,
                     &appController,
                     [&appController](const QStringList &arguments, const QString &) {
                         appController.handleCommandLine(arguments);
                     });

    QObject::connect(
        &engine,
        &QQmlApplicationEngine::objectCreationFailed,
        &app,
        [] {
            QCoreApplication::exit(EXIT_FAILURE);
        },
        Qt::QueuedConnection);

    engine.loadFromModule(QStringLiteral("Whatevr"), QStringLiteral("Main"));

    // Process the URL this instance was launched with, if any.
    appController.handleCommandLine(app.arguments());

    return app.exec();
}
