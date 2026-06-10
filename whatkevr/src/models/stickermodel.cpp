#include "stickermodel.h"

StickerModel::StickerModel(QObject *parent)
    : QAbstractListModel(parent)
{
}

int StickerModel::rowCount(const QModelIndex &parent) const
{
    if (parent.isValid()) {
        return 0;
    }
    return static_cast<int>(m_stickers.size());
}

QVariant StickerModel::data(const QModelIndex &index, int role) const
{
    if (!index.isValid() || index.row() < 0 || index.row() >= m_stickers.size()) {
        return {};
    }
    const auto &sticker = m_stickers.at(index.row());
    switch (role) {
    case CacheKeyRole:
        return sticker.cacheKey();
    case LocalPathRole:
        return sticker.localPath();
    case AnimatedRole:
        return sticker.isAnimated();
    case DownloadedRole:
        return !sticker.localPath().isEmpty();
    case EmojisRole:
        return sticker.emojis().join(QLatin1Char(' '));
    case AccessTextRole:
        return sticker.accessibilityText();
    case FavoriteRole:
        return sticker.isFavorite();
    default:
        return {};
    }
}

QHash<int, QByteArray> StickerModel::roleNames() const
{
    return {
        {CacheKeyRole, QByteArrayLiteral("cacheKey")},
        {LocalPathRole, QByteArrayLiteral("localPath")},
        {AnimatedRole, QByteArrayLiteral("animated")},
        {DownloadedRole, QByteArrayLiteral("downloaded")},
        {EmojisRole, QByteArrayLiteral("emojis")},
        {AccessTextRole, QByteArrayLiteral("accessText")},
        {FavoriteRole, QByteArrayLiteral("favorite")},
    };
}

void StickerModel::resetWith(const QList<whatevr::v1::Sticker> &stickers)
{
    beginResetModel();
    m_stickers = stickers;
    rebuildIndex();
    endResetModel();
    Q_EMIT countChanged();
}

void StickerModel::updateSticker(const whatevr::v1::Sticker &sticker, const QString &oldCacheKey)
{
    int row = m_rowByKey.value(sticker.cacheKey(), -1);
    if (row < 0 && !oldCacheKey.isEmpty()) {
        row = m_rowByKey.value(oldCacheKey, -1);
    }
    if (row < 0 || row >= m_stickers.size()) {
        return;
    }
    const QString previousKey = m_stickers.at(row).cacheKey();
    m_stickers[row] = sticker;
    if (previousKey != sticker.cacheKey()) {
        m_rowByKey.remove(previousKey);
        m_rowByKey.insert(sticker.cacheKey(), row);
    }
    const QModelIndex modelIndex = index(row);
    Q_EMIT dataChanged(modelIndex, modelIndex);
}

void StickerModel::rebuildIndex()
{
    m_rowByKey.clear();
    m_rowByKey.reserve(m_stickers.size());
    for (int i = 0; i < m_stickers.size(); ++i) {
        m_rowByKey.insert(m_stickers.at(i).cacheKey(), i);
    }
}
