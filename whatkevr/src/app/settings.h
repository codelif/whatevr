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
    Q_PROPERTY(bool compactMode READ compactMode WRITE setCompactMode NOTIFY compactModeChanged FINAL)
    // Message-bubble font point size; 0 means "inherit the theme default".
    Q_PROPERTY(int messageFontSize READ messageFontSize WRITE setMessageFontSize NOTIFY messageFontSizeChanged FINAL)
    Q_PROPERTY(bool showAvatars READ showAvatars WRITE setShowAvatars NOTIFY showAvatarsChanged FINAL)

    // --- Behavior ---
    Q_PROPERTY(bool persistDrafts READ persistDrafts WRITE setPersistDrafts NOTIFY persistDraftsChanged FINAL)
    // true  => Enter sends, Shift+Enter inserts a newline (current behaviour).
    // false => Enter inserts a newline, Ctrl/Cmd+Enter sends.
    Q_PROPERTY(bool enterToSend READ enterToSend WRITE setEnterToSend NOTIFY enterToSendChanged FINAL)

    // --- Window & Layout ---
    Q_PROPERTY(bool rememberWindowGeometry READ rememberWindowGeometry WRITE setRememberWindowGeometry NOTIFY rememberWindowGeometryChanged FINAL)
    Q_PROPERTY(bool rememberColumnWidth READ rememberColumnWidth WRITE setRememberColumnWidth NOTIFY rememberColumnWidthChanged FINAL)
    // Persisted chat-list column width in pixels; 0 means "use the computed default".
    Q_PROPERTY(int chatListColumnWidth READ chatListColumnWidth WRITE setChatListColumnWidth NOTIFY chatListColumnWidthChanged FINAL)

    // --- Emoji ---
    // Default skin tone applied to tone-capable emoji; 0 == neutral, 1..5 == light..dark.
    Q_PROPERTY(int defaultSkinTone READ defaultSkinTone WRITE setDefaultSkinTone NOTIFY defaultSkinToneChanged FINAL)

public:
    static void setInstance(Settings *instance);
    static Settings *instance();
    static Settings *create(QQmlEngine *qmlEngine, QJSEngine *jsEngine);

    explicit Settings(QObject *parent = nullptr);
    ~Settings() override;

    [[nodiscard]] QString colorScheme() const;
    void setColorScheme(const QString &schemeId);
    [[nodiscard]] bool compactMode() const;
    void setCompactMode(bool compact);
    [[nodiscard]] int messageFontSize() const;
    void setMessageFontSize(int points);
    [[nodiscard]] bool showAvatars() const;
    void setShowAvatars(bool show);

    [[nodiscard]] bool persistDrafts() const;
    void setPersistDrafts(bool persist);
    [[nodiscard]] bool enterToSend() const;
    void setEnterToSend(bool enabled);

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

    // --- Storage & cache (frontend-owned; no daemon round-trip) ---
    // Daemon media cache directory ($XDG_CACHE_HOME/whatevrd/media).
    Q_INVOKABLE [[nodiscard]] QString mediaCachePath() const;
    Q_INVOKABLE [[nodiscard]] qint64 cacheSizeBytes() const;
    Q_INVOKABLE [[nodiscard]] QString formattedCacheSize() const;
    Q_INVOKABLE void clearMediaCache();

    // --- Window geometry persistence (driven from Main.qml) ---
    Q_INVOKABLE void saveWindowGeometry(int x, int y, int width, int height);
    Q_INVOKABLE [[nodiscard]] bool hasSavedWindowGeometry() const;
    Q_INVOKABLE [[nodiscard]] int savedWindowX() const;
    Q_INVOKABLE [[nodiscard]] int savedWindowY() const;
    Q_INVOKABLE [[nodiscard]] int savedWindowWidth() const;
    Q_INVOKABLE [[nodiscard]] int savedWindowHeight() const;

Q_SIGNALS:
    void colorSchemeChanged();
    void compactModeChanged();
    void messageFontSizeChanged();
    void showAvatarsChanged();
    void persistDraftsChanged();
    void enterToSendChanged();
    void rememberWindowGeometryChanged();
    void rememberColumnWidthChanged();
    void chatListColumnWidthChanged();
    void defaultSkinToneChanged();
    // Emitted after the media cache is cleared so QML re-queries the size.
    void cacheChanged();

private:
    void load();
    void applyColorScheme();
    static qint64 directorySize(const QString &path);

    KColorSchemeManager *m_schemeManager = nullptr;

    QString m_colorScheme;
    bool m_compactMode = false;
    int m_messageFontSize = 0;
    bool m_showAvatars = true;
    bool m_persistDrafts = true;
    bool m_enterToSend = true;
    bool m_rememberWindowGeometry = true;
    bool m_rememberColumnWidth = true;
    int m_chatListColumnWidth = 0;
    int m_defaultSkinTone = 0;
};
