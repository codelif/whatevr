#pragma once

#include <QAbstractListModel>
#include <QHash>
#include <QList>
#include <QString>
#include <QStringList>
#include <QVariantList>

#include <cstdint>

class EmojiModel final : public QAbstractListModel
{
    Q_OBJECT
    Q_PROPERTY(QString searchQuery READ searchQuery WRITE setSearchQuery NOTIFY filterChanged FINAL)
    Q_PROPERTY(QString selectedGroup READ selectedGroup WRITE setSelectedGroup NOTIFY filterChanged FINAL)
    Q_PROPERTY(QStringList groups READ groups NOTIFY loadedChanged FINAL)
    Q_PROPERTY(QString emojiFontFamily READ emojiFontFamily CONSTANT FINAL)
    Q_PROPERTY(bool loaded READ loaded NOTIFY loadedChanged FINAL)

public:
    enum Role : std::uint16_t {
        EmojiRole = Qt::UserRole + 1,
        GroupRole,
        ShortcodesRole,
        EmoticonsRole,
        SearchTextRole,
        AlternatesRole,
        AnimatedRole,
        DirectionalRole,
        ToneGroupRole,
    };
    Q_ENUM(Role)

    explicit EmojiModel(QObject *parent = nullptr);

    [[nodiscard]] int rowCount(const QModelIndex &parent = QModelIndex()) const override;
    [[nodiscard]] QVariant data(const QModelIndex &index, int role = Qt::DisplayRole) const override;
    [[nodiscard]] QHash<int, QByteArray> roleNames() const override;

    [[nodiscard]] QString searchQuery() const;
    void setSearchQuery(const QString &query);
    [[nodiscard]] QString selectedGroup() const;
    void setSelectedGroup(const QString &group);
    [[nodiscard]] QStringList groups() const;
    [[nodiscard]] QString emojiFontFamily() const;
    [[nodiscard]] bool loaded() const;

    Q_INVOKABLE void addRecentEmoji(const QString &emoji);
    // Up to `limit` most-recently-used emoji, newest first; used to surface the
    // user's last-used reaction in the quick-reaction bar.
    Q_INVOKABLE [[nodiscard]] QStringList recentEmoji(int limit) const;
    Q_INVOKABLE [[nodiscard]] QString emojiAt(int row) const;
    Q_INVOKABLE void setFilter(const QString &query, const QString &group);
    Q_INVOKABLE QString groupIconName(const QString &group) const;
    // Non-mutating shortcode search for the composer's inline `:keyword` bar.
    // Returns up to `limit` maps of {emoji, shortcode}, prefix matches first.
    Q_INVOKABLE [[nodiscard]] QVariantList searchEmoji(const QString &query, int limit) const;

Q_SIGNALS:
    void filterChanged();
    void loadedChanged();

private:
    struct EmojiItem {
        QString emoji;
        QString group;
        QStringList shortcodes;
        QStringList emoticons;
        QStringList alternates;
        QStringList keywords;   // lowercased: cleaned shortcodes + emojilib keywords
        QString searchText;
        bool animated = false;
        bool directional = false;
        int toneGroup = 0;
    };

    static QString emojiFromCodepoints(const QList<int> &codepoints);
    static QString resolveEmojiFontFamily();
    static QString normalisedQuery(QString query);
    static QHash<QString, QStringList> loadKeywordIndex();

    void loadMetadata();
    void loadRecents();
    void saveRecents() const;
    void loadUsage();
    void saveUsage() const;
    void rebuildRows();
    [[nodiscard]] QVariant itemData(const EmojiItem &item, int role) const;

    QList<EmojiItem> m_items;
    QList<EmojiItem> m_recentItems;
    QList<int> m_rows;
    QHash<QString, QList<int>> m_rowsByGroup;
    QHash<QString, int> m_emojiUsage;
    QStringList m_groups;
    QStringList m_recentEmoji;
    QString m_emojiFontFamily;
    QString m_searchQuery;
    QString m_selectedGroup;
    bool m_loaded = false;
};
