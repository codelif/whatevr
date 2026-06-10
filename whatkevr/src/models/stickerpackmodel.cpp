#include "stickerpackmodel.h"

StickerPackModel::StickerPackModel(QObject *parent, bool installedOnly)
    : QAbstractListModel(parent)
    , m_installedOnly(installedOnly)
{
}

int StickerPackModel::rowCount(const QModelIndex &parent) const
{
    if (parent.isValid()) {
        return 0;
    }
    return static_cast<int>(m_packs.size());
}

QVariant StickerPackModel::data(const QModelIndex &index, int role) const
{
    if (!index.isValid() || index.row() < 0 || index.row() >= m_packs.size()) {
        return {};
    }
    const auto &pack = m_packs.at(index.row());
    switch (role) {
    case PackIdRole:
        return pack.id_proto();
    case NameRole:
        return pack.name();
    case PublisherRole:
        return pack.publisher();
    case DescriptionRole:
        return pack.description();
    case TrayPathRole:
        return pack.trayLocalPath();
    case InstalledRole:
        return pack.installed();
    case AnimatedRole:
        return pack.animated() || pack.lottie();
    case StickerCountRole:
        return static_cast<int>(pack.stickerCount());
    default:
        return {};
    }
}

QHash<int, QByteArray> StickerPackModel::roleNames() const
{
    return {
        {PackIdRole, QByteArrayLiteral("packId")},
        {NameRole, QByteArrayLiteral("name")},
        {PublisherRole, QByteArrayLiteral("publisher")},
        {DescriptionRole, QByteArrayLiteral("description")},
        {TrayPathRole, QByteArrayLiteral("trayPath")},
        {InstalledRole, QByteArrayLiteral("installed")},
        {AnimatedRole, QByteArrayLiteral("animated")},
        {StickerCountRole, QByteArrayLiteral("stickerCount")},
    };
}

void StickerPackModel::resetWith(const QList<whatevr::v1::StickerPack> &packs)
{
    beginResetModel();
    if (m_installedOnly) {
        m_packs.clear();
        for (const auto &pack : packs) {
            if (pack.installed()) {
                m_packs.append(pack);
            }
        }
    } else {
        m_packs = packs;
    }
    endResetModel();
    Q_EMIT countChanged();
}
