#include "emojimodel.h"

#include <QFile>
#include <QFontDatabase>
#include <QJsonArray>
#include <QJsonDocument>
#include <QJsonObject>
#include <QSettings>

#include <algorithm>

namespace {

constexpr qsizetype kMaximumRecentEmoji = 42;
const QString kRecentGroup = QStringLiteral("Recent");

QList<int> codepointsFromArray(const QJsonArray &array)
{
    QList<int> codepoints;
    codepoints.reserve(array.size());
    for (const QJsonValue &value : array) {
        if (value.isDouble()) {
            codepoints.append(value.toInt());
        }
    }
    return codepoints;
}

QStringList stringsFromArray(const QJsonArray &array)
{
    QStringList strings;
    strings.reserve(array.size());
    for (const QJsonValue &value : array) {
        if (value.isString()) {
            strings.append(value.toString());
        }
    }
    return strings;
}

}

EmojiModel::EmojiModel(QObject *parent)
    : QAbstractListModel(parent)
    , m_emojiFontFamily(resolveEmojiFontFamily())
{
    loadMetadata();
    loadRecents();
    rebuildRows();
}

int EmojiModel::rowCount(const QModelIndex &parent) const
{
    if (parent.isValid()) {
        return 0;
    }
    return m_rows.size();
}

QVariant EmojiModel::data(const QModelIndex &index, int role) const
{
    if (!index.isValid() || index.row() < 0 || index.row() >= m_rows.size()) {
        return {};
    }

    const int sourceRow = m_rows.at(index.row());
    if (sourceRow < 0) {
        return itemData(m_recentItems.at(-sourceRow - 1), role);
    }
    return itemData(m_items.at(sourceRow), role);
}

QHash<int, QByteArray> EmojiModel::roleNames() const
{
    return {
        {EmojiRole, "emoji"},
        {GroupRole, "group"},
        {ShortcodesRole, "shortcodes"},
        {EmoticonsRole, "emoticons"},
        {SearchTextRole, "searchText"},
        {AlternatesRole, "alternates"},
        {AnimatedRole, "animated"},
        {DirectionalRole, "directional"},
        {ToneGroupRole, "toneGroup"},
    };
}

QString EmojiModel::searchQuery() const
{
    return m_searchQuery;
}

void EmojiModel::setSearchQuery(const QString &query)
{
    setFilter(query, m_selectedGroup);
}

QString EmojiModel::selectedGroup() const
{
    return m_selectedGroup;
}

void EmojiModel::setSelectedGroup(const QString &group)
{
    setFilter(m_searchQuery, group);
}

QStringList EmojiModel::groups() const
{
    return m_groups;
}

QString EmojiModel::emojiFontFamily() const
{
    return m_emojiFontFamily;
}

bool EmojiModel::loaded() const
{
    return m_loaded;
}

void EmojiModel::addRecentEmoji(const QString &emoji)
{
    if (emoji.isEmpty()) {
        return;
    }

    m_recentEmoji.removeAll(emoji);
    m_recentEmoji.prepend(emoji);
    while (m_recentEmoji.size() > kMaximumRecentEmoji) {
        m_recentEmoji.removeLast();
    }

    loadRecents();
    saveRecents();
    rebuildRows();
    Q_EMIT loadedChanged();
}

QString EmojiModel::emojiAt(int row) const
{
    if (row < 0 || row >= m_rows.size()) {
        return {};
    }

    const int sourceRow = m_rows.at(row);
    if (sourceRow < 0) {
        return m_recentItems.at(-sourceRow - 1).emoji;
    }
    return m_items.at(sourceRow).emoji;
}

void EmojiModel::setFilter(const QString &query, const QString &group)
{
    const QString normalised = normalisedQuery(query);
    if (m_searchQuery == normalised && m_selectedGroup == group) {
        return;
    }

    m_searchQuery = normalised;
    m_selectedGroup = group;
    rebuildRows();
    Q_EMIT filterChanged();
}

QString EmojiModel::groupIconName(const QString &group) const
{
    if (group == kRecentGroup) {
        return QStringLiteral("document-open-recent-symbolic");
    }
    if (group.startsWith(QStringLiteral("Smileys"))) {
        return QStringLiteral("face-smile-symbolic");
    }
    if (group == QStringLiteral("People")) {
        return QStringLiteral("user-identity-symbolic");
    }
    if (group.contains(QStringLiteral("Animals"))) {
        return QStringLiteral("applications-games-symbolic");
    }
    if (group.contains(QStringLiteral("Food"))) {
        return QStringLiteral("applications-graphics-symbolic");
    }
    if (group.contains(QStringLiteral("Travel"))) {
        return QStringLiteral("go-jump-symbolic");
    }
    if (group.contains(QStringLiteral("Activities"))) {
        return QStringLiteral("applications-games-symbolic");
    }
    if (group.contains(QStringLiteral("Objects"))) {
        return QStringLiteral("document-properties-symbolic");
    }
    if (group.contains(QStringLiteral("Symbols"))) {
        return QStringLiteral("emblem-symbolic-link");
    }
    return QStringLiteral("flag-symbolic");
}

QString EmojiModel::emojiFromCodepoints(const QList<int> &codepoints)
{
    QString emoji;
    for (int codepoint : codepoints) {
        const char32_t ucs4 = static_cast<char32_t>(codepoint);
        emoji.append(QString::fromUcs4(&ucs4, 1));
    }
    return emoji;
}

QString EmojiModel::resolveEmojiFontFamily()
{
    const QStringList preferredFamilies = {
        QStringLiteral("Noto Color Emoji"),
        QStringLiteral("Segoe UI Emoji"),
        QStringLiteral("Twemoji Mozilla"),
        QStringLiteral("Apple Color Emoji"),
        QStringLiteral("EmojiOne Color"),
        QStringLiteral("Symbola"),
    };
    const QStringList installedFamilies = QFontDatabase::families();

    for (const QString &family : preferredFamilies) {
        if (installedFamilies.contains(family, Qt::CaseInsensitive)) {
            return family;
        }
    }

    return {};
}

QString EmojiModel::normalisedQuery(QString query)
{
    return query.trimmed().toLower();
}

void EmojiModel::loadMetadata()
{
    QFile file(QStringLiteral(":/data/emoji/emoji_17_0_ordering.json"));
    if (!file.open(QIODevice::ReadOnly)) {
        return;
    }

    const QJsonDocument document = QJsonDocument::fromJson(file.readAll());
    const QJsonArray groups = document.array();
    for (const QJsonValue &groupValue : groups) {
        const QJsonObject groupObject = groupValue.toObject();
        const QString group = groupObject.value(QStringLiteral("group")).toString();
        if (group.isEmpty()) {
            continue;
        }
        m_groups.append(group);

        const QJsonArray emojiArray = groupObject.value(QStringLiteral("emoji")).toArray();
        for (const QJsonValue &emojiValue : emojiArray) {
            const QJsonObject emojiObject = emojiValue.toObject();
            EmojiItem item;
            item.emoji = emojiFromCodepoints(codepointsFromArray(emojiObject.value(QStringLiteral("base")).toArray()));
            if (item.emoji.isEmpty()) {
                continue;
            }
            item.group = group;
            item.shortcodes = stringsFromArray(emojiObject.value(QStringLiteral("shortcodes")).toArray());
            item.emoticons = stringsFromArray(emojiObject.value(QStringLiteral("emoticons")).toArray());
            item.animated = emojiObject.value(QStringLiteral("animated")).toBool();
            item.directional = emojiObject.value(QStringLiteral("directional")).toBool();
            item.toneGroup = emojiObject.value(QStringLiteral("tone_group")).toInt();

            const QJsonArray alternates = emojiObject.value(QStringLiteral("alternates")).toArray();
            for (const QJsonValue &alternateValue : alternates) {
                const QString alternate = emojiFromCodepoints(codepointsFromArray(alternateValue.toArray()));
                if (!alternate.isEmpty() && !item.alternates.contains(alternate)) {
                    item.alternates.append(alternate);
                }
            }

            QStringList searchable = item.shortcodes + item.emoticons;
            searchable.append(item.emoji);
            searchable.append(item.group);
            item.searchText = searchable.join(QLatin1Char(' ')).toLower();
            const int sourceIndex = m_items.size();
            m_rowsByGroup[group].append(sourceIndex);
            m_items.append(std::move(item));
        }
    }

    m_loaded = !m_items.isEmpty();
    Q_EMIT loadedChanged();
}

void EmojiModel::loadRecents()
{
    if (m_recentEmoji.isEmpty()) {
        QSettings settings;
        m_recentEmoji = settings.value(QStringLiteral("emoji/recent")).toStringList();
    }

    m_recentItems.clear();
    for (const QString &emoji : std::as_const(m_recentEmoji)) {
        auto it = std::find_if(m_items.cbegin(), m_items.cend(), [&emoji](const EmojiItem &item) {
            return item.emoji == emoji || item.alternates.contains(emoji);
        });
        if (it == m_items.cend()) {
            continue;
        }
        EmojiItem recent = *it;
        recent.emoji = emoji;
        recent.group = kRecentGroup;
        m_recentItems.append(std::move(recent));
    }

    const bool hasRecentGroup = m_groups.contains(kRecentGroup);
    if (!m_recentItems.isEmpty() && !hasRecentGroup) {
        m_groups.prepend(kRecentGroup);
    } else if (m_recentItems.isEmpty() && hasRecentGroup) {
        m_groups.removeAll(kRecentGroup);
    }
}

void EmojiModel::saveRecents() const
{
    QSettings settings;
    settings.setValue(QStringLiteral("emoji/recent"), m_recentEmoji);
}

void EmojiModel::rebuildRows()
{
    beginResetModel();
    m_rows.clear();

    if (m_searchQuery.isEmpty() && (m_selectedGroup.isEmpty() || m_selectedGroup == kRecentGroup)) {
        for (int i = 0; i < m_recentItems.size(); ++i) {
            m_rows.append(-i - 1);
        }
        if (m_selectedGroup == kRecentGroup) {
            endResetModel();
            return;
        }
    }

    if (m_searchQuery.isEmpty() && !m_selectedGroup.isEmpty() && m_selectedGroup != kRecentGroup) {
        m_rows = m_rowsByGroup.value(m_selectedGroup);
        endResetModel();
        return;
    }

    const QList<int> candidateRows = (!m_selectedGroup.isEmpty() && m_selectedGroup != kRecentGroup)
        ? m_rowsByGroup.value(m_selectedGroup)
        : QList<int>();
    const bool useCandidates = !candidateRows.isEmpty();
    const int count = useCandidates ? candidateRows.size() : m_items.size();
    for (int candidateIndex = 0; candidateIndex < count; ++candidateIndex) {
        const int i = useCandidates ? candidateRows.at(candidateIndex) : candidateIndex;
        const EmojiItem &item = m_items.at(i);
        if (!m_searchQuery.isEmpty() && !item.searchText.contains(m_searchQuery)) {
            continue;
        }
        m_rows.append(i);
    }

    endResetModel();
}

QVariant EmojiModel::itemData(const EmojiItem &item, int role) const
{
    switch (role) {
    case EmojiRole:
        return item.emoji;
    case GroupRole:
        return item.group;
    case ShortcodesRole:
        return item.shortcodes;
    case EmoticonsRole:
        return item.emoticons;
    case SearchTextRole:
        return item.searchText;
    case AlternatesRole:
        return item.alternates;
    case AnimatedRole:
        return item.animated;
    case DirectionalRole:
        return item.directional;
    case ToneGroupRole:
        return item.toneGroup;
    default:
        return {};
    }
}
