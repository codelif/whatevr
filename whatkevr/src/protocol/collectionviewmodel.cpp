#include "collectionviewmodel.h"

#include <algorithm>

namespace whatevr::proto
{

CollectionViewModel::CollectionViewModel(QObject *parent)
    : QAbstractListModel(parent)
{
}

int CollectionViewModel::rowCount(const QModelIndex &parent) const
{
    if (parent.isValid()) {
        return 0;
    }
    return static_cast<int>(m_items.size());
}

QVariant CollectionViewModel::data(const QModelIndex &index, int role) const
{
    if (!index.isValid() || index.row() < 0 || index.row() >= m_items.size()) {
        return {};
    }
    const Item &item = m_items.at(index.row());
    switch (role) {
    case ItemRole:
        return item.data;
    case IdRole:
        return item.id;
    case SortRole:
        return item.sortRaw;
    default:
        return {};
    }
}

QHash<int, QByteArray> CollectionViewModel::roleNames() const
{
    return {
        {ItemRole, QByteArrayLiteral("item")},
        {IdRole, QByteArrayLiteral("itemId")},
        {SortRole, QByteArrayLiteral("sortKey")},
    };
}

QVariantMap CollectionViewModel::itemById(const QString &id) const
{
    const int row = m_indexById.value(id, -1);
    if (row < 0) {
        return {};
    }
    return m_items.at(row).data;
}

int CollectionViewModel::indexOfId(const QString &id) const
{
    return m_indexById.value(id, -1);
}

bool CollectionViewModel::sortsBefore(const Item &lhs, const Item &rhs)
{
    if (lhs.sortKey != rhs.sortKey) {
        return lhs.sortKey < rhs.sortKey;
    }
    return lhs.id < rhs.id;
}

int CollectionViewModel::lowerBound(const Item &item) const
{
    const auto it = std::lower_bound(m_items.cbegin(), m_items.cend(), item, sortsBefore);
    return static_cast<int>(it - m_items.cbegin());
}

void CollectionViewModel::rebuildIndex(int fromRow)
{
    for (int row = fromRow; row < m_items.size(); ++row) {
        m_indexById.insert(m_items.at(row).id, row);
    }
}

void CollectionViewModel::onUpsert(const QString &sort, const QJsonObject &item)
{
    const QString id = item.value(QStringLiteral("id")).toString();
    if (id.isEmpty()) {
        return; // rule 3: every item carries an id; a keyless item is unusable
    }

    Item next;
    next.id = id;
    next.sortRaw = sort;
    next.sortKey = sort.toUtf8();
    next.data = item.toVariantMap();

    const int existing = m_indexById.value(id, -1);
    if (existing < 0) {
        // New item: insert at its sorted position.
        const int row = lowerBound(next);
        beginInsertRows(QModelIndex(), row, row);
        m_items.insert(row, next);
        endInsertRows();
        rebuildIndex(row);
        Q_EMIT countChanged();
        return;
    }

    const bool moved = m_items.at(existing).sortKey != next.sortKey;
    if (!moved) {
        // Same position: replace data in place.
        m_items[existing] = next;
        const QModelIndex idx = index(existing);
        Q_EMIT dataChanged(idx, idx, {ItemRole, SortRole});
        return;
    }

    // Sort key changed: the item moves. Compute its destination as if it were
    // absent, then emit a move (or a same-slot replace when nothing shifts).
    m_items.removeAt(existing);
    int dest = lowerBound(next);
    if (dest == existing) {
        m_items.insert(dest, next);
        rebuildIndex(dest);
        const QModelIndex idx = index(dest);
        Q_EMIT dataChanged(idx, idx, {ItemRole, SortRole});
        return;
    }

    // beginMoveRows destination is expressed in pre-move row coordinates: when
    // moving downward, the target index is one past the visual landing slot.
    const int moveTo = dest > existing ? dest + 1 : dest;
    beginMoveRows(QModelIndex(), existing, existing, QModelIndex(), moveTo);
    m_items.insert(dest, next);
    endMoveRows();
    rebuildIndex(std::min(existing, dest));
    const QModelIndex idx = index(dest);
    Q_EMIT dataChanged(idx, idx, {ItemRole, SortRole});
}

void CollectionViewModel::onRemove(const QString &id)
{
    const int row = m_indexById.value(id, -1);
    if (row < 0) {
        return;
    }
    beginRemoveRows(QModelIndex(), row, row);
    m_items.removeAt(row);
    endRemoveRows();
    m_indexById.remove(id);
    rebuildIndex(row);
    Q_EMIT countChanged();
}

void CollectionViewModel::onReady(bool exhausted, bool hasExhausted)
{
    // A `ready` without the flag leaves exhaustion unknown; treat as not
    // exhausted so a frontend never wrongly stops extending.
    const bool nextExhausted = hasExhausted && exhausted;
    if (m_ready && m_exhausted == nextExhausted) {
        return;
    }
    m_ready = true;
    m_exhausted = nextExhausted;
    Q_EMIT readyChanged();
}

void CollectionViewModel::onReset()
{
    beginResetModel();
    m_items.clear();
    m_indexById.clear();
    endResetModel();
    if (m_ready || m_exhausted) {
        m_ready = false;
        m_exhausted = false;
        Q_EMIT readyChanged();
    }
    Q_EMIT countChanged();
}

} // namespace whatevr::proto
