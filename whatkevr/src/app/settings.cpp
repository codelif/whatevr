#include "settings.h"

#include <algorithm>

#include <QAbstractItemModel>
#include <QDir>
#include <QDirIterator>
#include <QFileInfo>
#include <QLocale>
#include <QQmlEngine>
#include <QSettings>
#include <QStandardPaths>
#include <QUrl>
#include <QVariantMap>

#include <KColorSchemeManager>
#include <KColorSchemeModel>

namespace {
Settings *g_instance = nullptr;

constexpr auto kColorScheme = "settings/colorScheme";
constexpr auto kDensity = "settings/density";
// Legacy boolean replaced by kDensity; read once at load to migrate.
constexpr auto kCompactMode = "settings/compactMode";
constexpr auto kChatWallpaper = "settings/chatWallpaper";
constexpr auto kChatWallpaperPattern = "settings/chatWallpaperPattern";
constexpr auto kChatWallpaperPath = "settings/chatWallpaperPath";
constexpr auto kChatWallpaperScale = "settings/chatWallpaperScale";
constexpr auto kChatWallpaperOpacity = "settings/chatWallpaperOpacity";
constexpr auto kChatWallpaperTint = "settings/chatWallpaperTint";
constexpr auto kMessageFontSize = "settings/messageFontSize";
constexpr auto kShowAvatars = "settings/showAvatars";
constexpr auto kVideoBackend = "settings/videoBackend";
constexpr auto kHardwareDecoding = "settings/hardwareDecoding";
constexpr auto kGifPlayerLimit = "settings/gifPlayerLimit";
constexpr auto kPausePlaybackWhileScrolling = "settings/pausePlaybackWhileScrolling";
constexpr auto kStreamWhileDownloading = "settings/streamWhileDownloading";
constexpr auto kAutoplayInlineMedia = "settings/autoplayInlineMedia";
constexpr auto kLoopGifs = "settings/loopGifs";
constexpr auto kAdvanceVoiceMessages = "settings/advanceVoiceMessages";
constexpr auto kRememberPlaybackPosition = "settings/rememberPlaybackPosition";
constexpr auto kDefaultPlaybackSpeed = "settings/defaultPlaybackSpeed";
constexpr auto kMediaSaveDirectory = "settings/mediaSaveDirectory";
constexpr auto kPersistDrafts = "settings/persistDrafts";
constexpr auto kEnterToSend = "settings/enterToSend";
constexpr auto kSnapToBottomOnSend = "settings/snapToBottomOnSend";
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
    // Deliberately NOT applying the scheme here: at construction time no
    // QQuickWindow exists yet, and the org.kde.desktop QtQuick style re-reads
    // the system palette when the first window is created, clobbering an
    // early-applied scheme (visible as a half-applied light theme on restart).
    // main() calls applyColorScheme() once the engine has loaded the window.
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
    if (settings.contains(QLatin1String(kDensity))) {
        m_density = qBound(0, settings.value(QLatin1String(kDensity)).toInt(), 2);
    } else {
        // Migrate the legacy boolean: compact -> Compact, otherwise Standard.
        m_density = settings.value(QLatin1String(kCompactMode), false).toBool() ? DensityCompact : DensityStandard;
    }
    m_chatWallpaper = settings.value(QLatin1String(kChatWallpaper)).toString();
    m_chatWallpaperPattern = settings.value(QLatin1String(kChatWallpaperPattern)).toString();
    m_chatWallpaperPath = settings.value(QLatin1String(kChatWallpaperPath)).toString();
    m_chatWallpaperScale = qBound(50, settings.value(QLatin1String(kChatWallpaperScale), 100).toInt(), 300);
    m_chatWallpaperOpacity = qBound(0, settings.value(QLatin1String(kChatWallpaperOpacity), 10).toInt(), 25);
    m_chatWallpaperTint = settings.value(QLatin1String(kChatWallpaperTint)).toString();
    m_messageFontSize = settings.value(QLatin1String(kMessageFontSize), 0).toInt();
    m_showAvatars = settings.value(QLatin1String(kShowAvatars), true).toBool();
    m_videoBackend = settings.value(QLatin1String(kVideoBackend), QStringLiteral("auto")).toString();
    m_hardwareDecoding = settings.value(QLatin1String(kHardwareDecoding), true).toBool();
    m_gifPlayerLimit = std::clamp(settings.value(QLatin1String(kGifPlayerLimit), 3).toInt(), 0, 3);
    m_pausePlaybackWhileScrolling = settings.value(QLatin1String(kPausePlaybackWhileScrolling), true).toBool();
    m_streamWhileDownloading = settings.value(QLatin1String(kStreamWhileDownloading), true).toBool();
    m_autoplayInlineMedia = settings.value(QLatin1String(kAutoplayInlineMedia), true).toBool();
    m_loopGifs = settings.value(QLatin1String(kLoopGifs), true).toBool();
    m_advanceVoiceMessages = settings.value(QLatin1String(kAdvanceVoiceMessages), true).toBool();
    m_rememberPlaybackPosition = settings.value(QLatin1String(kRememberPlaybackPosition), true).toBool();
    m_defaultPlaybackSpeed = settings.value(QLatin1String(kDefaultPlaybackSpeed), 1.0).toDouble();
    m_mediaSaveDirectory = settings.value(QLatin1String(kMediaSaveDirectory), QString()).toString();
    m_persistDrafts = settings.value(QLatin1String(kPersistDrafts), true).toBool();
    m_enterToSend = settings.value(QLatin1String(kEnterToSend), true).toBool();
    m_snapToBottomOnSend = settings.value(QLatin1String(kSnapToBottomOnSend), true).toBool();
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
    // themeMode is derived from the active scheme, so it moves with it.
    Q_EMIT themeModeChanged();
}

namespace {
// Maps the high-level light/dark selector onto a concrete KColorScheme id.
//
// Two-pass so we land on the scheme the user means rather than the first that
// merely isn't its opposite: for light, "Breeze Light" beats "Breeze Classic"
// (both are light, but only one is named Light). Pass 1 prefers a scheme whose
// name carries the explicit keyword ("Light"/"Dark"); pass 2 falls back to the
// first scheme that isn't the opposite tone. No hard-coded ids.
QString schemeIdForMode(const QVariantList &schemes, bool wantDark)
{
    const QString wanted = wantDark ? QStringLiteral("Dark") : QStringLiteral("Light");
    const QString opposite = wantDark ? QStringLiteral("Light") : QStringLiteral("Dark");

    QString fallback;
    for (const QVariant &entry : schemes) {
        const QVariantMap map = entry.toMap();
        const QString id = map.value(QStringLiteral("id")).toString();
        if (id.isEmpty()) {
            continue; // the system-default entry
        }
        const QString name = map.value(QStringLiteral("name")).toString();
        if (name.contains(wanted, Qt::CaseInsensitive)) {
            return id; // explicit match wins outright
        }
        if (fallback.isEmpty() && !name.contains(opposite, Qt::CaseInsensitive)) {
            fallback = id; // first scheme of the right tone, kept as backup
        }
    }
    return fallback;
}
} // namespace

int Settings::themeMode() const
{
    if (m_colorScheme.isEmpty()) {
        return ThemeSystem;
    }
    // Derive light/dark from the active scheme's display name.
    const QVariantList schemes = availableColorSchemes();
    for (const QVariant &entry : schemes) {
        const QVariantMap map = entry.toMap();
        if (map.value(QStringLiteral("id")).toString() == m_colorScheme) {
            return map.value(QStringLiteral("name")).toString().contains(QStringLiteral("Dark"), Qt::CaseInsensitive)
                ? ThemeDark
                : ThemeLight;
        }
    }
    return ThemeLight;
}

void Settings::setThemeMode(int mode)
{
    switch (mode) {
    case ThemeSystem:
        setColorScheme(QString());
        break;
    case ThemeLight:
        setColorScheme(schemeIdForMode(availableColorSchemes(), false));
        break;
    case ThemeDark:
        setColorScheme(schemeIdForMode(availableColorSchemes(), true));
        break;
    default:
        break;
    }
}

int Settings::density() const
{
    return m_density;
}

void Settings::setDensity(int density)
{
    const int clamped = qBound(0, density, 2);
    if (m_density == clamped) {
        return;
    }
    const bool wasCompact = compactMode();
    m_density = clamped;
    QSettings().setValue(QLatin1String(kDensity), m_density);
    Q_EMIT densityChanged();
    if (wasCompact != compactMode()) {
        Q_EMIT compactModeChanged();
    }
}

bool Settings::compactMode() const
{
    return m_density == DensityCompact;
}

QString Settings::chatWallpaper() const
{
    return m_chatWallpaper;
}

void Settings::setChatWallpaper(const QString &wallpaperId)
{
    if (m_chatWallpaper == wallpaperId) {
        return;
    }
    m_chatWallpaper = wallpaperId;
    QSettings().setValue(QLatin1String(kChatWallpaper), m_chatWallpaper);
    Q_EMIT chatWallpaperChanged();
}

QString Settings::chatWallpaperPattern() const
{
    return m_chatWallpaperPattern;
}

void Settings::setChatWallpaperPattern(const QString &pattern)
{
    if (m_chatWallpaperPattern == pattern) {
        return;
    }
    m_chatWallpaperPattern = pattern;
    QSettings().setValue(QLatin1String(kChatWallpaperPattern), m_chatWallpaperPattern);
    Q_EMIT chatWallpaperPatternChanged();
}

QString Settings::chatWallpaperPath() const
{
    return m_chatWallpaperPath;
}

void Settings::setChatWallpaperPath(const QString &path)
{
    if (m_chatWallpaperPath == path) {
        return;
    }
    m_chatWallpaperPath = path;
    QSettings().setValue(QLatin1String(kChatWallpaperPath), m_chatWallpaperPath);
    Q_EMIT chatWallpaperPathChanged();
}

int Settings::chatWallpaperScale() const
{
    return m_chatWallpaperScale;
}

void Settings::setChatWallpaperScale(int percent)
{
    const int clamped = qBound(50, percent, 300);
    if (m_chatWallpaperScale == clamped) {
        return;
    }
    m_chatWallpaperScale = clamped;
    QSettings().setValue(QLatin1String(kChatWallpaperScale), m_chatWallpaperScale);
    Q_EMIT chatWallpaperScaleChanged();
}

int Settings::chatWallpaperOpacity() const
{
    return m_chatWallpaperOpacity;
}

void Settings::setChatWallpaperOpacity(int percent)
{
    const int clamped = qBound(0, percent, 25);
    if (m_chatWallpaperOpacity == clamped) {
        return;
    }
    m_chatWallpaperOpacity = clamped;
    QSettings().setValue(QLatin1String(kChatWallpaperOpacity), m_chatWallpaperOpacity);
    Q_EMIT chatWallpaperOpacityChanged();
}

QString Settings::chatWallpaperTint() const
{
    return m_chatWallpaperTint;
}

void Settings::setChatWallpaperTint(const QString &tint)
{
    if (m_chatWallpaperTint == tint) {
        return;
    }
    m_chatWallpaperTint = tint;
    QSettings().setValue(QLatin1String(kChatWallpaperTint), m_chatWallpaperTint);
    Q_EMIT chatWallpaperTintChanged();
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

QString Settings::videoBackend() const
{
    return m_videoBackend;
}

void Settings::setVideoBackend(const QString &backend)
{
    // Anything unrecognised means automatic, so a hand-edited config cannot
    // leave the app with no way to play video.
    const QString normalized = (backend == QLatin1String("mpv") || backend == QLatin1String("qt")) ? backend : QStringLiteral("auto");
    if (m_videoBackend == normalized) {
        return;
    }
    m_videoBackend = normalized;
    QSettings().setValue(QLatin1String(kVideoBackend), m_videoBackend);
    Q_EMIT videoBackendChanged();
}

bool Settings::hardwareDecoding() const
{
    return m_hardwareDecoding;
}

void Settings::setHardwareDecoding(bool enabled)
{
    if (m_hardwareDecoding == enabled) {
        return;
    }
    m_hardwareDecoding = enabled;
    QSettings().setValue(QLatin1String(kHardwareDecoding), m_hardwareDecoding);
    Q_EMIT hardwareDecodingChanged();
}

int Settings::gifPlayerLimit() const
{
    return m_gifPlayerLimit;
}

void Settings::setGifPlayerLimit(int limit)
{
    // Above three, a chat full of clips spends more on decoders than on the
    // conversation; below zero is meaningless.
    const int clamped = std::clamp(limit, 0, 3);
    if (m_gifPlayerLimit == clamped) {
        return;
    }
    m_gifPlayerLimit = clamped;
    QSettings().setValue(QLatin1String(kGifPlayerLimit), m_gifPlayerLimit);
    Q_EMIT gifPlayerLimitChanged();
}

bool Settings::pausePlaybackWhileScrolling() const
{
    return m_pausePlaybackWhileScrolling;
}

void Settings::setPausePlaybackWhileScrolling(bool enabled)
{
    if (m_pausePlaybackWhileScrolling == enabled) {
        return;
    }
    m_pausePlaybackWhileScrolling = enabled;
    QSettings().setValue(QLatin1String(kPausePlaybackWhileScrolling), m_pausePlaybackWhileScrolling);
    Q_EMIT pausePlaybackWhileScrollingChanged();
}

bool Settings::streamWhileDownloading() const
{
    return m_streamWhileDownloading;
}

void Settings::setStreamWhileDownloading(bool enabled)
{
    if (m_streamWhileDownloading == enabled) {
        return;
    }
    m_streamWhileDownloading = enabled;
    QSettings().setValue(QLatin1String(kStreamWhileDownloading), m_streamWhileDownloading);
    Q_EMIT streamWhileDownloadingChanged();
}

bool Settings::autoplayInlineMedia() const
{
    return m_autoplayInlineMedia;
}

void Settings::setAutoplayInlineMedia(bool enabled)
{
    if (m_autoplayInlineMedia == enabled) {
        return;
    }
    m_autoplayInlineMedia = enabled;
    QSettings().setValue(QLatin1String(kAutoplayInlineMedia), m_autoplayInlineMedia);
    Q_EMIT autoplayInlineMediaChanged();
}

bool Settings::loopGifs() const
{
    return m_loopGifs;
}

void Settings::setLoopGifs(bool enabled)
{
    if (m_loopGifs == enabled) {
        return;
    }
    m_loopGifs = enabled;
    QSettings().setValue(QLatin1String(kLoopGifs), m_loopGifs);
    Q_EMIT loopGifsChanged();
}

bool Settings::advanceVoiceMessages() const
{
    return m_advanceVoiceMessages;
}

void Settings::setAdvanceVoiceMessages(bool enabled)
{
    if (m_advanceVoiceMessages == enabled) {
        return;
    }
    m_advanceVoiceMessages = enabled;
    QSettings().setValue(QLatin1String(kAdvanceVoiceMessages), m_advanceVoiceMessages);
    Q_EMIT advanceVoiceMessagesChanged();
}

bool Settings::rememberPlaybackPosition() const
{
    return m_rememberPlaybackPosition;
}

void Settings::setRememberPlaybackPosition(bool enabled)
{
    if (m_rememberPlaybackPosition == enabled) {
        return;
    }
    m_rememberPlaybackPosition = enabled;
    QSettings().setValue(QLatin1String(kRememberPlaybackPosition), m_rememberPlaybackPosition);
    Q_EMIT rememberPlaybackPositionChanged();
}

double Settings::defaultPlaybackSpeed() const
{
    return m_defaultPlaybackSpeed;
}

void Settings::setDefaultPlaybackSpeed(double speed)
{
    // The three speeds the pill cycles; anything else means 1x.
    double normalized = 1.0;
    if (qFuzzyCompare(speed, 1.5) || qFuzzyCompare(speed, 2.0)) {
        normalized = speed;
    }
    if (qFuzzyCompare(m_defaultPlaybackSpeed, normalized)) {
        return;
    }
    m_defaultPlaybackSpeed = normalized;
    QSettings().setValue(QLatin1String(kDefaultPlaybackSpeed), m_defaultPlaybackSpeed);
    Q_EMIT defaultPlaybackSpeedChanged();
}

QString Settings::mediaSaveDirectory() const
{
    return m_mediaSaveDirectory;
}

void Settings::setMediaSaveDirectory(const QString &path)
{
    if (m_mediaSaveDirectory == path) {
        return;
    }
    m_mediaSaveDirectory = path;
    QSettings().setValue(QLatin1String(kMediaSaveDirectory), m_mediaSaveDirectory);
    Q_EMIT mediaSaveDirectoryChanged();
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

bool Settings::snapToBottomOnSend() const
{
    return m_snapToBottomOnSend;
}

void Settings::setSnapToBottomOnSend(bool enabled)
{
    if (m_snapToBottomOnSend == enabled) {
        return;
    }
    m_snapToBottomOnSend = enabled;
    QSettings().setValue(QLatin1String(kSnapToBottomOnSend), m_snapToBottomOnSend);
    Q_EMIT snapToBottomOnSendChanged();
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

QString Settings::importWallpaperSvg(const QString &sourceUrl)
{
    // Accept either a bare path or a "file://" URL (the FileDialog hands back URLs).
    const QUrl url(sourceUrl);
    const QString source = url.isLocalFile() ? url.toLocalFile() : sourceUrl;
    const QFileInfo info(source);
    if (!info.exists() || !info.isFile()) {
        return QString();
    }

    const QString destDir =
        QStandardPaths::writableLocation(QStandardPaths::AppDataLocation) + QStringLiteral("/wallpapers");
    if (!QDir().mkpath(destDir)) {
        return QString();
    }

    // Use a stable name so re-importing the same file overwrites in place rather
    // than accumulating copies; suffix-prefixed by base name keeps it recognisable.
    const QString dest = destDir + QStringLiteral("/") + info.fileName();
    QFile::remove(dest); // QFile::copy won't overwrite an existing file
    if (!QFile::copy(source, dest)) {
        return QString();
    }
    return dest;
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
