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
#include "version.h"

int main(int argc, char *argv[])
{
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
