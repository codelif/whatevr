#include "settings.h"

#include <QAbstractItemModel>
#include <QDir>
#include <QDirIterator>
#include <QFileInfo>
#include <QLocale>
#include <QQmlEngine>
#include <QSettings>
#include <QStandardPaths>
#include <QVariantMap>

#include <KColorSchemeManager>
#include <KColorSchemeModel>

namespace {
Settings *g_instance = nullptr;

constexpr auto kColorScheme = "settings/colorScheme";
constexpr auto kCompactMode = "settings/compactMode";
constexpr auto kMessageFontSize = "settings/messageFontSize";
constexpr auto kShowAvatars = "settings/showAvatars";
constexpr auto kPersistDrafts = "settings/persistDrafts";
constexpr auto kEnterToSend = "settings/enterToSend";
constexpr auto kRememberWindowGeometry = "settings/rememberWindowGeometry";
constexpr auto kRememberColumnWidth = "settings/rememberColumnWidth";
constexpr auto kChatListColumnWidth = "settings/chatListColumnWidth";
constexpr auto kDefaultSkinTone = "settings/defaultSkinTone";
constexpr auto kWindowX = "settings/window/x";
constexpr auto kWindowY = "settings/window/y";
constexpr auto kWindowWidth = "settings/window/width";
constexpr auto kWindowHeight = "settings/window/height";
} // namespace

void Settings::setInstance(Settings *instance)
{
    g_instance = instance;
}

Settings *Settings::instance()
{
    return g_instance;
}

Settings *Settings::create(QQmlEngine *, QJSEngine *)
{
    // Hand QML the process-wide instance constructed in main.cpp, so the C++
    // consumers (EmojiModel, ChatListModel) and the QML singleton are the same
    // object. Mirrors AppController::create().
    Q_ASSERT(g_instance);
    // The engine takes ownership of the returned QObject; the instance is owned
    // by main()'s stack, so detach it from any C++ parent and tell QML not to
    // delete it.
    QQmlEngine::setObjectOwnership(g_instance, QQmlEngine::CppOwnership);
    return g_instance;
}

Settings::Settings(QObject *parent)
    : QObject(parent)
{
    // App-wide singleton (owned by Qt). The manager remembers/restores its own
    // choice via kdeglobals; we keep the user's preference in QSettings as the
    // single source of truth and re-apply it ourselves, so disable the
    // manager's autosave to avoid two competing stores.
    m_schemeManager = KColorSchemeManager::instance();
    m_schemeManager->setAutosaveChanges(false);

    load();
    applyColorScheme();
}

Settings::~Settings()
{
    if (g_instance == this) {
        g_instance = nullptr;
    }
}

void Settings::load()
{
    const QSettings settings;
    m_colorScheme = settings.value(QLatin1String(kColorScheme)).toString();
    m_compactMode = settings.value(QLatin1String(kCompactMode), false).toBool();
    m_messageFontSize = settings.value(QLatin1String(kMessageFontSize), 0).toInt();
    m_showAvatars = settings.value(QLatin1String(kShowAvatars), true).toBool();
    m_persistDrafts = settings.value(QLatin1String(kPersistDrafts), true).toBool();
    m_enterToSend = settings.value(QLatin1String(kEnterToSend), true).toBool();
    m_rememberWindowGeometry = settings.value(QLatin1String(kRememberWindowGeometry), true).toBool();
    m_rememberColumnWidth = settings.value(QLatin1String(kRememberColumnWidth), true).toBool();
    m_chatListColumnWidth = settings.value(QLatin1String(kChatListColumnWidth), 0).toInt();
    m_defaultSkinTone = settings.value(QLatin1String(kDefaultSkinTone), 0).toInt();
}

void Settings::applyColorScheme()
{
    if (!m_schemeManager) {
        return;
    }
    // An empty id activates the system scheme (documented behaviour).
    m_schemeManager->activateSchemeId(m_colorScheme);
}

QString Settings::colorScheme() const
{
    return m_colorScheme;
}

void Settings::setColorScheme(const QString &schemeId)
{
    if (m_colorScheme == schemeId) {
        return;
    }
    m_colorScheme = schemeId;
    QSettings().setValue(QLatin1String(kColorScheme), m_colorScheme);
    applyColorScheme();
    Q_EMIT colorSchemeChanged();
}

bool Settings::compactMode() const
{
    return m_compactMode;
}

void Settings::setCompactMode(bool compact)
{
    if (m_compactMode == compact) {
        return;
    }
    m_compactMode = compact;
    QSettings().setValue(QLatin1String(kCompactMode), m_compactMode);
    Q_EMIT compactModeChanged();
}

int Settings::messageFontSize() const
{
    return m_messageFontSize;
}

void Settings::setMessageFontSize(int points)
{
    if (m_messageFontSize == points) {
        return;
    }
    m_messageFontSize = points;
    QSettings().setValue(QLatin1String(kMessageFontSize), m_messageFontSize);
    Q_EMIT messageFontSizeChanged();
}

bool Settings::showAvatars() const
{
    return m_showAvatars;
}

void Settings::setShowAvatars(bool show)
{
    if (m_showAvatars == show) {
        return;
    }
    m_showAvatars = show;
    QSettings().setValue(QLatin1String(kShowAvatars), m_showAvatars);
    Q_EMIT showAvatarsChanged();
}

bool Settings::persistDrafts() const
{
    return m_persistDrafts;
}

void Settings::setPersistDrafts(bool persist)
{
    if (m_persistDrafts == persist) {
        return;
    }
    m_persistDrafts = persist;
    QSettings().setValue(QLatin1String(kPersistDrafts), m_persistDrafts);
    Q_EMIT persistDraftsChanged();
}

bool Settings::enterToSend() const
{
    return m_enterToSend;
}

void Settings::setEnterToSend(bool enabled)
{
    if (m_enterToSend == enabled) {
        return;
    }
    m_enterToSend = enabled;
    QSettings().setValue(QLatin1String(kEnterToSend), m_enterToSend);
    Q_EMIT enterToSendChanged();
}

bool Settings::rememberWindowGeometry() const
{
    return m_rememberWindowGeometry;
}

void Settings::setRememberWindowGeometry(bool remember)
{
    if (m_rememberWindowGeometry == remember) {
        return;
    }
    m_rememberWindowGeometry = remember;
    QSettings().setValue(QLatin1String(kRememberWindowGeometry), m_rememberWindowGeometry);
    Q_EMIT rememberWindowGeometryChanged();
}

bool Settings::rememberColumnWidth() const
{
    return m_rememberColumnWidth;
}

void Settings::setRememberColumnWidth(bool remember)
{
    if (m_rememberColumnWidth == remember) {
        return;
    }
    m_rememberColumnWidth = remember;
    QSettings().setValue(QLatin1String(kRememberColumnWidth), m_rememberColumnWidth);
    Q_EMIT rememberColumnWidthChanged();
}

int Settings::chatListColumnWidth() const
{
    return m_chatListColumnWidth;
}

void Settings::setChatListColumnWidth(int width)
{
    if (m_chatListColumnWidth == width) {
        return;
    }
    m_chatListColumnWidth = width;
    QSettings().setValue(QLatin1String(kChatListColumnWidth), m_chatListColumnWidth);
    Q_EMIT chatListColumnWidthChanged();
}

int Settings::defaultSkinTone() const
{
    return m_defaultSkinTone;
}

void Settings::setDefaultSkinTone(int tone)
{
    const int clamped = qBound(0, tone, 5);
    if (m_defaultSkinTone == clamped) {
        return;
    }
    m_defaultSkinTone = clamped;
    QSettings().setValue(QLatin1String(kDefaultSkinTone), m_defaultSkinTone);
    Q_EMIT defaultSkinToneChanged();
}

QVariantList Settings::availableColorSchemes() const
{
    QVariantList schemes;
    if (!m_schemeManager) {
        return schemes;
    }
    const QAbstractItemModel *model = m_schemeManager->model();
    for (int row = 0; row < model->rowCount(); ++row) {
        const QModelIndex index = model->index(row, 0);
        QVariantMap entry;
        entry.insert(QStringLiteral("id"), index.data(KColorSchemeModel::IdRole).toString());
        entry.insert(QStringLiteral("name"), index.data(Qt::DisplayRole).toString());
        schemes.append(entry);
    }
    return schemes;
}

QString Settings::mediaCachePath() const
{
    const QString base = QStandardPaths::writableLocation(QStandardPaths::GenericCacheLocation);
    return base + QStringLiteral("/whatevrd/media");
}

qint64 Settings::directorySize(const QString &path)
{
    qint64 total = 0;
    QDirIterator it(path, QDir::Files | QDir::NoSymLinks, QDirIterator::Subdirectories);
    while (it.hasNext()) {
        it.next();
        total += it.fileInfo().size();
    }
    return total;
}

qint64 Settings::cacheSizeBytes() const
{
    return directorySize(mediaCachePath());
}

QString Settings::formattedCacheSize() const
{
    return QLocale().formattedDataSize(cacheSizeBytes());
}

void Settings::clearMediaCache()
{
    QDir dir(mediaCachePath());
    if (!dir.exists()) {
        Q_EMIT cacheChanged();
        return;
    }
    // Remove the cache contents but keep the directory itself, so the daemon can
    // keep writing into it without re-creating the tree.
    const QFileInfoList entries =
        dir.entryInfoList(QDir::NoDotAndDotDot | QDir::Files | QDir::Dirs | QDir::Hidden);
    for (const QFileInfo &entry : entries) {
        if (entry.isDir()) {
            QDir(entry.absoluteFilePath()).removeRecursively();
        } else {
            QFile::remove(entry.absoluteFilePath());
        }
    }
    Q_EMIT cacheChanged();
}

void Settings::saveWindowGeometry(int x, int y, int width, int height)
{
    QSettings settings;
    settings.setValue(QLatin1String(kWindowX), x);
    settings.setValue(QLatin1String(kWindowY), y);
    settings.setValue(QLatin1String(kWindowWidth), width);
    settings.setValue(QLatin1String(kWindowHeight), height);
}

bool Settings::hasSavedWindowGeometry() const
{
    const QSettings settings;
    return settings.contains(QLatin1String(kWindowWidth))
        && settings.contains(QLatin1String(kWindowHeight));
}

int Settings::savedWindowX() const
{
    return QSettings().value(QLatin1String(kWindowX), 0).toInt();
}

int Settings::savedWindowY() const
{
    return QSettings().value(QLatin1String(kWindowY), 0).toInt();
}

int Settings::savedWindowWidth() const
{
    return QSettings().value(QLatin1String(kWindowWidth), 0).toInt();
}

int Settings::savedWindowHeight() const
{
    return QSettings().value(QLatin1String(kWindowHeight), 0).toInt();
}
