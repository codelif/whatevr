#include <QApplication>
#include <QLoggingCategory>
#include <QPixmapCache>
#include <QQmlApplicationEngine>
#include <QQmlContext>
#include <QQuickStyle>
#include <QTimer>

#include <KAboutData>
#include <KDBusService>
#include <KLocalizedContext>
#include <KLocalizedString>

#include "app/appcontroller.h"
#include "app/protocolcontroller.h"
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
        i18nc("@info", "Native WhatsApp client for Linux (Qt/Kirigami frontend)"));
    aboutData.setHomepage(QStringLiteral("https://github.com/codelif/whatevr"));
    aboutData.setBugAddress("https://github.com/codelif/whatevr/issues");
    // KAboutLicense::BSD_3_Clause ships no bundled full text, so it renders a
    // bare "see the SPDX website" stub. Point at the repo's LICENSE (embedded in
    // the qrc) so the About dialog shows the real terms.
    aboutData.setLicenseTextFile(QStringLiteral(":/LICENSE"));
    aboutData.setCopyrightStatement(i18nc("@info:credit", "Copyright (c) 2026 Harsh Sharma"));
    aboutData.addAuthor(QStringLiteral("Harsh Sharma"));

    // Third-party libraries and bundled data, surfaced in the About page's
    // Components section. The emoji data licenses (MIT / Apache-2.0) require
    // attribution; the rest are credited as a courtesy. Qt and KDE Frameworks
    // are injected automatically by the formcard AboutComponent, so they are
    // deliberately not added here.
    aboutData.addComponent(QStringLiteral("Kirigami Addons"),
                           i18nc("@info:credit", "Form, About and convergent UI components"),
                           QString(),
                           QStringLiteral("https://invent.kde.org/libraries/kirigami-addons"),
                           KAboutLicense::LGPL_V2_1);
    aboutData.addComponent(QStringLiteral("rlottie"),
                           i18nc("@info:credit", "Lottie animation rendering for animated stickers"),
                           QString(),
                           QStringLiteral("https://github.com/Samsung/rlottie"),
                           KAboutLicense::MIT);
    aboutData.addComponent(QStringLiteral("whatsmeow"),
                           i18nc("@info:credit", "WhatsApp Web multidevice protocol library (via the whatevrd daemon)"),
                           QString(),
                           QStringLiteral("https://github.com/tulir/whatsmeow"),
                           KAboutLicense::MPL_V2);
    aboutData.addComponent(QStringLiteral("emojilib"),
                           i18nc("@info:credit", "Emoji keyword / shortcode data — © 2014 Mu-An Chiou"),
                           QString(),
                           QStringLiteral("https://github.com/muan/emojilib"),
                           KAboutLicense::MIT);
    aboutData.addComponent(QStringLiteral("Google Fonts emoji metadata"),
                           i18nc("@info:credit", "Emoji ordering & grouping data"),
                           QStringLiteral("Emoji 17.0"),
                           QStringLiteral("https://github.com/googlefonts/emoji-metadata"),
                           KAboutLicense::Apache_V2);

    KAboutData::setApplicationData(aboutData);

    QQmlApplicationEngine engine;
    engine.rootContext()->setContextObject(new KLocalizedContext(&engine));

    // Constructed before AppController so the models it creates can read the
    // shared Settings instance (drafts persistence, default skin tone).
    Settings settings;
    Settings::setInstance(&settings);

    AppController appController;
    AppController::setInstance(&appController);

    // The whatevr-protocol connection lifecycle (D2a onward). Runs alongside the
    // still-gRPC AppController during the migration: it drives the status/login/
    // splash screens and the shell-visibility gate, while AppController keeps
    // serving the not-yet-ported chat shell until the D-phase port completes.
    ProtocolController protocolController;
    ProtocolController::setInstance(&protocolController);

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

    // Begin connecting the protocol client (async; signals arrive once the event
    // loop below is running).
    protocolController.start();

    // Assert the saved color scheme onto the live palette. The org.kde.desktop
    // platform integration resets qApp's palette to the *system* scheme when the
    // first window is exposed (which happens once the event loop runs), so a
    // synchronous apply here would be clobbered. Defer to the event loop so the
    // scheme wins, matching the working runtime path. A saved light scheme would
    // otherwise come up as the system dark theme on restart.
    QTimer::singleShot(0, &settings, [&settings] {
        settings.applyColorScheme();
    });

    // Process the URL this instance was launched with, if any.
    appController.handleCommandLine(app.arguments());

    return app.exec();
}
