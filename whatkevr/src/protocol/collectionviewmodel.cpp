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
    // Callers read these synchronously from signal handlers, so a batch left
    // open by the current drain has to be visible. Flushing here (rather than
    // consulting the buffer) keeps every reader looking at one source of truth.
    const_cast<CollectionViewModel *>(this)->flushBatch();
    const int row = m_indexById.value(id, -1);
    if (row < 0 || row >= m_items.size()) {
        return {};
    }
    return m_items.at(row).data;
}

int CollectionViewModel::count() const
{
    const_cast<CollectionViewModel *>(this)->flushBatch();
    return static_cast<int>(m_items.size());
}

int CollectionViewModel::indexOfId(const QString &id) const
{
    const_cast<CollectionViewModel *>(this)->flushBatch();
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

// Re-key rows from `fromRow` onward after an insert/remove/move shifted them.
// This must run *before* the matching endInsertRows/endRemoveRows/endMoveRows:
// those emit synchronously, and an observer that reacts by calling itemById()
// (ProtocolMessageModel does, for every transfer change) would otherwise read
// this map while it still describes the pre-mutation list.
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

    // Inside a batch, buffer until it closes. A subscribe fill delivers one
    // upsert per item; applying each on arrival meant a begin/endInsertRows —
    // and a full QML layout pass — per message.
    if (m_batching) {
        if (!m_pendingOrder.contains(id)) {
            m_pendingOrder.append(id);
        }
        m_pending[id] = PendingOp{false, std::move(next)};
        return;
    }
    applyUpsert(std::move(next));
}

// applyUpsert places one item and emits its own signals, countChanged included
// when it grows the list. flushBatch drives it only for ids already present
// (replaces and moves), reporting the size change for a batch itself.
void CollectionViewModel::applyUpsert(Item next)
{
    const QString id = next.id;
    const int existing = m_indexById.value(id, -1);
    if (existing < 0) {
        // New item: insert at its sorted position.
        const int row = lowerBound(next);
        beginInsertRows(QModelIndex(), row, row);
        m_items.insert(row, next);
        rebuildIndex(row);
        endInsertRows();
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

    // Sort key changed: compute its destination as if the existing row were
    // absent, but do not mutate until beginMoveRows has notified observers.
    int dest = 0;
    for (int row = 0; row < m_items.size(); ++row) {
        if (row != existing && sortsBefore(m_items.at(row), next)) {
            ++dest;
        }
    }
    if (dest == existing) {
        m_items[existing] = next;
        const QModelIndex idx = index(dest);
        Q_EMIT dataChanged(idx, idx, {ItemRole, SortRole});
        return;
    }

    // beginMoveRows destination is expressed in pre-move row coordinates: when
    // moving downward, the target index is one past the visual landing slot.
    const int moveTo = dest > existing ? dest + 1 : dest;
    beginMoveRows(QModelIndex(), existing, existing, QModelIndex(), moveTo);
    m_items.removeAt(existing);
    m_items.insert(dest, next);
    rebuildIndex(std::min(existing, dest));
    endMoveRows();
    const QModelIndex idx = index(dest);
    Q_EMIT dataChanged(idx, idx, {ItemRole, SortRole});
}

void CollectionViewModel::onRemove(const QString &id)
{
    if (m_batching) {
        if (!m_pendingOrder.contains(id)) {
            m_pendingOrder.append(id);
        }
        m_pending[id] = PendingOp{true, Item{}};
        return;
    }
    const int row = m_indexById.value(id, -1);
    if (row < 0) {
        return;
    }
    beginRemoveRows(QModelIndex(), row, row);
    m_items.removeAt(row);
    m_indexById.remove(id);
    rebuildIndex(row);
    endRemoveRows();
    Q_EMIT countChanged();
}

void CollectionViewModel::onBatchBegin()
{
    m_batching = true;
}

// flushBatch turns everything buffered since the last boundary into the fewest
// model transactions that describe it. The shapes that matter are a subscribe
// fill (all new rows, contiguous), a history page (all new rows, contiguous at
// one end) and a live trickle (one or two rows) — the first two collapse to a
// single insert, which is the whole point.
void CollectionViewModel::flushBatch()
{
    if (m_pendingOrder.isEmpty()) {
        return;
    }
    const QList<QString> order = std::move(m_pendingOrder);
    m_pendingOrder.clear();
    QHash<QString, PendingOp> pending = std::move(m_pending);
    m_pending.clear();

    const int countBefore = static_cast<int>(m_items.size());
    QList<Item> fresh;
    fresh.reserve(order.size());

    for (const QString &id : order) {
        const auto it = pending.constFind(id);
        if (it == pending.constEnd()) {
            continue;
        }
        const PendingOp &op = it.value();
        if (op.remove) {
            const int row = m_indexById.value(id, -1);
            if (row < 0) {
                continue;
            }
            beginRemoveRows(QModelIndex(), row, row);
            m_items.removeAt(row);
            m_indexById.remove(id);
            rebuildIndex(row);
            endRemoveRows();
            continue;
        }
        if (m_indexById.contains(id)) {
            // Already present: a replace or a move, neither of which merges
            // with the new-row run. These are rare inside a batch (a live
            // status change, a chat-list reorder), so apply them one by one.
            applyUpsert(op.item);
            continue;
        }
        fresh.append(op.item);
    }

    insertSortedRun(std::move(fresh));

    if (static_cast<int>(m_items.size()) != countBefore) {
        Q_EMIT countChanged();
    }
}

// insertSortedRun merges brand-new rows into the list, emitting one insert
// transaction per contiguous run of destination rows. A fill or a page is one
// run; interleaved arrivals degrade gracefully to a few.
void CollectionViewModel::insertSortedRun(QList<Item> fresh)
{
    if (fresh.isEmpty()) {
        return;
    }
    std::sort(fresh.begin(), fresh.end(), sortsBefore);

    int i = 0;
    while (i < fresh.size()) {
        const int row = lowerBound(fresh.at(i));
        // Extend the run while the next item also belongs immediately after the
        // previous one — i.e. still before the row currently at `row`.
        int j = i + 1;
        while (j < fresh.size()
               && (row >= m_items.size() || sortsBefore(fresh.at(j), m_items.at(row)))) {
            ++j;
        }
        const int runLength = j - i;
        beginInsertRows(QModelIndex(), row, row + runLength - 1);
        for (int k = 0; k < runLength; ++k) {
            m_items.insert(row + k, fresh.at(i + k));
        }
        rebuildIndex(row);
        endInsertRows();
        i = j;
    }
}

void CollectionViewModel::onBatchEnd()
{
    flushBatch();
    m_batching = false;
}

void CollectionViewModel::onReady(bool exhausted, bool hasExhausted)
{
    // `ready` closes a fill, so its items must already be in the model: a
    // frontend reacting to readyReceived reads rowCount immediately.
    flushBatch();
    // A `ready` without the flag leaves exhaustion unknown; treat as not
    // exhausted so a frontend never wrongly stops extending.
    const bool nextExhausted = hasExhausted && exhausted;
    if (!m_ready || m_exhausted != nextExhausted) {
        m_ready = true;
        m_exhausted = nextExhausted;
        Q_EMIT readyChanged();
    }
    Q_EMIT readyReceived(nextExhausted);
}

void CollectionViewModel::onReset()
{
    // Anything buffered belongs to the copy being discarded. The batch itself
    // stays open: upserts rebuilding the copy follow in the same drain.
    m_pending.clear();
    m_pendingOrder.clear();
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
