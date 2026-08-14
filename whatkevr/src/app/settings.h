#pragma once

#include <QObject>
#include <QString>
#include <QStringList>
#include <QVariantList>
#include <qqmlintegration.h>

#include <cstdint>

QT_BEGIN_NAMESPACE
class QQmlEngine;
class QJSEngine;
QT_END_NAMESPACE

class KColorSchemeManager;

// Application-wide user preferences, persisted with QSettings (the same backend
// the emoji recents already use). A single instance is shared between QML (as a
// QML_SINGLETON) and the C++ consumers that read settings directly
// (EmojiModel, ChatListModel), wired through setInstance()/instance() exactly
// like AppController.
//
// All keys live under the "settings/" group so they never collide with the
// existing "emoji/*" keys.
class Settings final : public QObject
{
    Q_OBJECT
    QML_NAMED_ELEMENT(Settings)
    QML_SINGLETON

    // --- Appearance ---
    // Active KColorScheme id; empty string means "follow the system scheme".
    Q_PROPERTY(QString colorScheme READ colorScheme WRITE setColorScheme NOTIFY colorSchemeChanged FINAL)
    // High-level theme selector layered over colorScheme: 0 System, 1 Light, 2 Dark.
    // Setting it picks an appropriate KColorScheme id; reading it derives the mode
    // back from the active scheme.
    Q_PROPERTY(int themeMode READ themeMode WRITE setThemeMode NOTIFY themeModeChanged FINAL)
    // List/conversation density: 0 Compact, 1 Standard, 2 Comfortable.
    Q_PROPERTY(int density READ density WRITE setDensity NOTIFY densityChanged FINAL)
    // Derived convenience kept for existing consumers: true when density == Compact.
    Q_PROPERTY(bool compactMode READ compactMode NOTIFY compactModeChanged FINAL)
    // Message-bubble font point size; 0 means "inherit the theme default".
    Q_PROPERTY(int messageFontSize READ messageFontSize WRITE setMessageFontSize NOTIFY messageFontSizeChanged FINAL)
    Q_PROPERTY(bool showAvatars READ showAvatars WRITE setShowAvatars NOTIFY showAvatarsChanged FINAL)

    // --- Media playback (Advanced) ---
    // How many GIFs may loop at once. Videos and video notes are not counted:
    // exactly one of those plays anywhere, which is not a tunable. 0 means GIFs
    // stay thumbnails and play only when opened full screen.
    Q_PROPERTY(int gifPlayerLimit READ gifPlayerLimit WRITE setGifPlayerLimit NOTIFY gifPlayerLimitChanged FINAL)
    // Suspend inline playback during a fast fling.
    Q_PROPERTY(bool pausePlaybackWhileScrolling READ pausePlaybackWhileScrolling WRITE setPausePlaybackWhileScrolling NOTIFY pausePlaybackWhileScrollingChanged FINAL)
    // Play from the daemon's loopback range server while a file is still being
    // fetched. Off forces the whole-file download path.
    Q_PROPERTY(bool streamWhileDownloading READ streamWhileDownloading WRITE setStreamWhileDownloading NOTIFY streamWhileDownloadingChanged FINAL)

    // --- Media playback (Chats) ---
    // GIFs start looping by themselves once on screen. Videos and video notes
    // never do: they carry sound and wait to be asked.
    Q_PROPERTY(bool autoplayInlineMedia READ autoplayInlineMedia WRITE setAutoplayInlineMedia NOTIFY autoplayInlineMediaChanged FINAL)
    Q_PROPERTY(bool loopGifs READ loopGifs WRITE setLoopGifs NOTIFY loopGifsChanged FINAL)
    // Play the next voice message in the chat when one finishes.
    Q_PROPERTY(bool advanceVoiceMessages READ advanceVoiceMessages WRITE setAdvanceVoiceMessages NOTIFY advanceVoiceMessagesChanged FINAL)
    // Resume a part-heard voice note where it stopped instead of at 0:00.
    Q_PROPERTY(bool rememberPlaybackPosition READ rememberPlaybackPosition WRITE setRememberPlaybackPosition NOTIFY rememberPlaybackPositionChanged FINAL)
    // Starting speed for voice notes and audio files (1.0, 1.5 or 2.0).
    Q_PROPERTY(double defaultPlaybackSpeed READ defaultPlaybackSpeed WRITE setDefaultPlaybackSpeed NOTIFY defaultPlaybackSpeedChanged FINAL)
    // Directory Save As starts in; empty means the per-kind XDG location.
    Q_PROPERTY(QString mediaSaveDirectory READ mediaSaveDirectory WRITE setMediaSaveDirectory NOTIFY mediaSaveDirectoryChanged FINAL)
    // Conversation wallpaper preset id; empty is the plain default background.
    Q_PROPERTY(QString chatWallpaper READ chatWallpaper WRITE setChatWallpaper NOTIFY chatWallpaperChanged FINAL)
    // Doodle pattern drawn over the wallpaper background: "" none, "doodle" the
    // bundled motif, "custom" the user's own SVG at chatWallpaperPath.
    Q_PROPERTY(QString chatWallpaperPattern READ chatWallpaperPattern WRITE setChatWallpaperPattern NOTIFY chatWallpaperPatternChanged FINAL)
    // Absolute path of the imported custom pattern SVG (copied into the app data dir).
    Q_PROPERTY(QString chatWallpaperPath READ chatWallpaperPath WRITE setChatWallpaperPath NOTIFY chatWallpaperPathChanged FINAL)
    // Pattern tile size as a percentage of the motif's natural size (50..300).
    Q_PROPERTY(int chatWallpaperScale READ chatWallpaperScale WRITE setChatWallpaperScale NOTIFY chatWallpaperScaleChanged FINAL)
    // Pattern opacity/intensity as a percentage (0..25); kept subtle by default.
    Q_PROPERTY(int chatWallpaperOpacity READ chatWallpaperOpacity WRITE setChatWallpaperOpacity NOTIFY chatWallpaperOpacityChanged FINAL)
    // Manual pattern tint as a "#rrggbb" string; empty means "auto" (derived from
    // the active background's lightness so the motif stays visible).
    Q_PROPERTY(QString chatWallpaperTint READ chatWallpaperTint WRITE setChatWallpaperTint NOTIFY chatWallpaperTintChanged FINAL)

    // --- Behavior ---
    Q_PROPERTY(bool persistDrafts READ persistDrafts WRITE setPersistDrafts NOTIFY persistDraftsChanged FINAL)
    // true  => Enter sends, Shift+Enter inserts a newline (current behaviour).
    // false => Enter inserts a newline, Ctrl/Cmd+Enter sends.
    Q_PROPERTY(bool enterToSend READ enterToSend WRITE setEnterToSend NOTIFY enterToSendChanged FINAL)
    // true => sending jumps the timeline to the new message even when the user
    // was reading further up; false leaves the viewport where it is.
    Q_PROPERTY(bool snapToBottomOnSend READ snapToBottomOnSend WRITE setSnapToBottomOnSend NOTIFY snapToBottomOnSendChanged FINAL)

    // --- Window & Layout ---
    Q_PROPERTY(bool rememberWindowGeometry READ rememberWindowGeometry WRITE setRememberWindowGeometry NOTIFY rememberWindowGeometryChanged FINAL)
    Q_PROPERTY(bool rememberColumnWidth READ rememberColumnWidth WRITE setRememberColumnWidth NOTIFY rememberColumnWidthChanged FINAL)
    // Persisted chat-list column width in pixels; 0 means "use the computed default".
    Q_PROPERTY(int chatListColumnWidth READ chatListColumnWidth WRITE setChatListColumnWidth NOTIFY chatListColumnWidthChanged FINAL)

    // --- Emoji ---
    // Default skin tone applied to tone-capable emoji; 0 == neutral, 1..5 == light..dark.
    Q_PROPERTY(int defaultSkinTone READ defaultSkinTone WRITE setDefaultSkinTone NOTIFY defaultSkinToneChanged FINAL)

public:
    enum Density {
        DensityCompact = 0,
        DensityStandard = 1,
        DensityComfortable = 2,
    };
    Q_ENUM(Density)

    enum ThemeMode {
        ThemeSystem = 0,
        ThemeLight = 1,
        ThemeDark = 2,
    };
    Q_ENUM(ThemeMode)

    static void setInstance(Settings *instance);
    static Settings *instance();
    static Settings *create(QQmlEngine *qmlEngine, QJSEngine *jsEngine);

    // Not defaulted, for the same reason as ProtocolController's: a
    // default-constructible QML_SINGLETON is built by the engine itself instead
    // of through create(), which would fork this off from main()'s instance.
    explicit Settings(QObject *parent);
    ~Settings() override;

    [[nodiscard]] QString colorScheme() const;
    void setColorScheme(const QString &schemeId);
    [[nodiscard]] int themeMode() const;
    void setThemeMode(int mode);
    [[nodiscard]] int density() const;
    void setDensity(int density);
    [[nodiscard]] bool compactMode() const;
    [[nodiscard]] int messageFontSize() const;
    void setMessageFontSize(int points);
    [[nodiscard]] bool showAvatars() const;
    void setShowAvatars(bool show);
    [[nodiscard]] int gifPlayerLimit() const;
    void setGifPlayerLimit(int limit);
    [[nodiscard]] bool pausePlaybackWhileScrolling() const;
    void setPausePlaybackWhileScrolling(bool enabled);
    [[nodiscard]] bool streamWhileDownloading() const;
    void setStreamWhileDownloading(bool enabled);
    [[nodiscard]] bool autoplayInlineMedia() const;
    void setAutoplayInlineMedia(bool enabled);
    [[nodiscard]] bool loopGifs() const;
    void setLoopGifs(bool enabled);
    [[nodiscard]] bool advanceVoiceMessages() const;
    void setAdvanceVoiceMessages(bool enabled);
    [[nodiscard]] bool rememberPlaybackPosition() const;
    void setRememberPlaybackPosition(bool enabled);
    [[nodiscard]] double defaultPlaybackSpeed() const;
    void setDefaultPlaybackSpeed(double speed);
    [[nodiscard]] QString mediaSaveDirectory() const;
    void setMediaSaveDirectory(const QString &path);
    [[nodiscard]] QString chatWallpaper() const;
    void setChatWallpaper(const QString &wallpaperId);
    [[nodiscard]] QString chatWallpaperPattern() const;
    void setChatWallpaperPattern(const QString &pattern);
    [[nodiscard]] QString chatWallpaperPath() const;
    void setChatWallpaperPath(const QString &path);
    [[nodiscard]] int chatWallpaperScale() const;
    void setChatWallpaperScale(int percent);
    [[nodiscard]] int chatWallpaperOpacity() const;
    void setChatWallpaperOpacity(int percent);
    [[nodiscard]] QString chatWallpaperTint() const;
    void setChatWallpaperTint(const QString &tint);

    [[nodiscard]] bool persistDrafts() const;
    void setPersistDrafts(bool persist);
    [[nodiscard]] bool enterToSend() const;
    void setEnterToSend(bool enabled);
    [[nodiscard]] bool snapToBottomOnSend() const;
    void setSnapToBottomOnSend(bool enabled);

    [[nodiscard]] bool rememberWindowGeometry() const;
    void setRememberWindowGeometry(bool remember);
    [[nodiscard]] bool rememberColumnWidth() const;
    void setRememberColumnWidth(bool remember);
    [[nodiscard]] int chatListColumnWidth() const;
    void setChatListColumnWidth(int width);

    [[nodiscard]] int defaultSkinTone() const;
    void setDefaultSkinTone(int tone);

    // Color-scheme list for the Appearance combo: each entry is a map with
    // "id" and "name". The system-default entry has an empty "id".
    Q_INVOKABLE [[nodiscard]] QVariantList availableColorSchemes() const;

    // Re-assert the saved color scheme onto the live application palette. Must
    // be called once the first QQuickWindow exists (the org.kde.desktop style
    // re-reads system colors at window creation, which otherwise leaves a saved
    // light scheme half-applied on startup).
    void applyColorScheme();

    // --- Storage & cache (frontend-owned; no daemon round-trip) ---
    // Daemon media cache directory ($XDG_CACHE_HOME/whatevrd/media).
    Q_INVOKABLE [[nodiscard]] QString mediaCachePath() const;
    Q_INVOKABLE [[nodiscard]] qint64 cacheSizeBytes() const;
    Q_INVOKABLE [[nodiscard]] QString formattedCacheSize() const;
    Q_INVOKABLE void clearMediaCache();

    // Copy a user-picked SVG (passed as a file path or "file://" URL) into the
    // app data dir so it survives the original being moved/deleted, and return
    // the new absolute path. Returns an empty string on failure.
    Q_INVOKABLE [[nodiscard]] QString importWallpaperSvg(const QString &sourceUrl);

    // --- Window geometry persistence (driven from Main.qml) ---
    Q_INVOKABLE void saveWindowGeometry(int x, int y, int width, int height);
    Q_INVOKABLE [[nodiscard]] bool hasSavedWindowGeometry() const;
    Q_INVOKABLE [[nodiscard]] int savedWindowX() const;
    Q_INVOKABLE [[nodiscard]] int savedWindowY() const;
    Q_INVOKABLE [[nodiscard]] int savedWindowWidth() const;
    Q_INVOKABLE [[nodiscard]] int savedWindowHeight() const;

Q_SIGNALS:
    void colorSchemeChanged();
    void themeModeChanged();
    void densityChanged();
    void compactModeChanged();
    void messageFontSizeChanged();
    void showAvatarsChanged();
    void gifPlayerLimitChanged();
    void pausePlaybackWhileScrollingChanged();
    void streamWhileDownloadingChanged();
    void autoplayInlineMediaChanged();
    void loopGifsChanged();
    void advanceVoiceMessagesChanged();
    void rememberPlaybackPositionChanged();
    void defaultPlaybackSpeedChanged();
    void mediaSaveDirectoryChanged();
    void chatWallpaperChanged();
    void chatWallpaperPatternChanged();
    void chatWallpaperPathChanged();
    void chatWallpaperScaleChanged();
    void chatWallpaperOpacityChanged();
    void chatWallpaperTintChanged();
    void persistDraftsChanged();
    void enterToSendChanged();
    void snapToBottomOnSendChanged();
    void rememberWindowGeometryChanged();
    void rememberColumnWidthChanged();
    void chatListColumnWidthChanged();
    void defaultSkinToneChanged();
    // Emitted after the media cache is cleared so QML re-queries the size.
    void cacheChanged();

private:
    void load();
    static qint64 directorySize(const QString &path);

    KColorSchemeManager *m_schemeManager = nullptr;

    QString m_colorScheme;
    int m_density = DensityStandard;
    int m_messageFontSize = 0;
    bool m_showAvatars = true;
    int m_gifPlayerLimit = 3;
    bool m_pausePlaybackWhileScrolling = true;
    bool m_streamWhileDownloading = true;
    bool m_autoplayInlineMedia = true;
    bool m_loopGifs = true;
    bool m_advanceVoiceMessages = true;
    bool m_rememberPlaybackPosition = true;
    double m_defaultPlaybackSpeed = 1.0;
    QString m_mediaSaveDirectory;
    QString m_chatWallpaper;
    QString m_chatWallpaperPattern;
    QString m_chatWallpaperPath;
    int m_chatWallpaperScale = 100;
    int m_chatWallpaperOpacity = 10;
    QString m_chatWallpaperTint;
    bool m_persistDrafts = true;
    bool m_enterToSend = true;
    bool m_snapToBottomOnSend = true;
    bool m_rememberWindowGeometry = true;
    bool m_rememberColumnWidth = true;
    int m_chatListColumnWidth = 0;
    int m_defaultSkinTone = 0;
};
