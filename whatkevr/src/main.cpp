#include <QApplication>
#include <QQmlApplicationEngine>
#include <QQmlContext>
#include <QQuickStyle>
#include <qqml.h>

#include <KAboutData>
#include <KLocalizedContext>
#include <KLocalizedString>

#include "app/appcontroller.h"

int main(int argc, char *argv[])
{
    KLocalizedString::setApplicationDomain("whatkevr");
    QQuickStyle::setStyle(QStringLiteral("org.kde.desktop"));

    QApplication app(argc, argv);
    app.setApplicationName(QStringLiteral("Whatevr"));
    app.setDesktopFileName(QStringLiteral("in.codelif.Whatevr"));
    app.setOrganizationDomain(QStringLiteral("codelif.in"));

    KAboutData aboutData(QStringLiteral("in.codelif.Whatevr"),
                         i18nc("@title", "Whatevr"),
                         QStringLiteral("0.1.0"));
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
    engine.rootContext()->setContextProperty(QStringLiteral("AppController"), &appController);
    qmlRegisterSingletonInstance("Whatevr", 1, 0, "AppController", &appController);

    QObject::connect(
        &engine,
        &QQmlApplicationEngine::objectCreationFailed,
        &app,
        [] {
            QCoreApplication::exit(EXIT_FAILURE);
        },
        Qt::QueuedConnection);

    engine.loadFromModule(QStringLiteral("Whatevr"), QStringLiteral("Main"));

    return app.exec();
}
