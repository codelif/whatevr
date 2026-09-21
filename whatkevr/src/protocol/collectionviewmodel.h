#pragma once

#include <QAbstractListModel>
#include <QByteArray>
#include <QHash>
#include <QList>
#include <QString>
#include <QVariantMap>

#include <cstdint>

#include "protocolclient.h"

namespace whatevr::proto
{

// A generic, keyed, sorted list model over any collection view. It implements
// PROTOCOL.md's "universal client algorithm" and nothing else: keep a map of
// items by `id`, ordered by the opaque `sort` key, apply upserts and removes,
// render. It never sorts by any field, merges, deduplicates, or caches — the
// daemon owns all of that; the only ordering here is a bytewise comparison of
// the daemon-supplied `sort` string (rule 3).
//
// Every row exposes its whole item as `ItemRole` (a QVariantMap), so a QML
// delegate binds `model.item.<field>` for whatever fields the view carries;
// `IdRole` and `SortRole` are available for keying/diagnostics.
class CollectionViewModel final : public QAbstractListModel, public ViewSink
{
    Q_OBJECT
    Q_PROPERTY(bool ready READ isReady NOTIFY readyChanged FINAL)
    Q_PROPERTY(bool exhausted READ isExhausted NOTIFY readyChanged FINAL)
    Q_PROPERTY(int count READ count NOTIFY countChanged FINAL)

public:
    enum Role : std::uint16_t {
        ItemRole = Qt::UserRole + 1, // the whole item as a QVariantMap
        IdRole,
        SortRole,
    };
    Q_ENUM(Role)

    explicit CollectionViewModel(QObject *parent = nullptr);

    [[nodiscard]] int rowCount(const QModelIndex &parent = QModelIndex()) const override;
    [[nodiscard]] QVariant data(const QModelIndex &index, int role = Qt::DisplayRole) const override;
    [[nodiscard]] QHash<int, QByteArray> roleNames() const override;

    // Look up a row's current item by id (empty map if absent).
    [[nodiscard]] Q_INVOKABLE QVariantMap itemById(const QString &id) const;
    [[nodiscard]] Q_INVOKABLE int indexOfId(const QString &id) const;
    // The id at a sorted row ("" if out of range). Lets QML enumerate the
    // model in order — e.g. to group status rows per contact — without any
    // C++-side copy of the rows.
    [[nodiscard]] Q_INVOKABLE QString idAt(int index) const;

    [[nodiscard]] bool isReady() const { return m_ready; }
    [[nodiscard]] bool isExhausted() const { return m_exhausted; }
    // Like itemById/indexOfId, this settles any batch still open in the current
    // socket drain so every reader sees the same list. Outside a drain the
    // buffer is always empty, so the check costs nothing.
    [[nodiscard]] int count() const;

    // Hold the rows in the mirror of the daemon's order, newest first, so a
    // view whose live edge is the *bottom* of the screen can put that edge at
    // row 0 and let a BottomToTop ListView draw it there.
    //
    // This is a presentation choice about which end of the list is the fixed
    // one, not a reinterpretation of `sort`: the key is still opaque and still
    // compared bytewise (rule 3), the comparison simply runs the other way. The
    // transcript is the only view that wants it, because it is the only view
    // that is read from its newest end and grows away from it.
    //
    // Must be set before the first item arrives; reordering a populated view
    // would mean reissuing every row's position for no reason.
    void setReverseOrder(bool reverse);
    [[nodiscard]] bool reverseOrder() const { return m_reverseOrder; }

    // ViewSink
    void onUpsert(const QString &sort, const QJsonObject &item) override;
    void onRemove(const QString &id) override;
    void onReady(bool exhausted, bool hasExhausted) override;
    void onReset() override;
    void onBatchBegin() override;
    void onBatchEnd() override;

Q_SIGNALS:
    void readyChanged();
    // Emitted for every wire `ready`, including repeated completions whose
    // ready/exhausted property values did not change.
    void readyReceived(bool exhausted);
    void countChanged();

private:
    struct Item {
        QString id;
        QString sortRaw; // as received, for SortRole
        QByteArray sortKey; // UTF-8 bytes, for bytewise ordering
        QVariantMap data;
    };

    // Strict-weak ordering: bytewise on the sort key, id as a stable tiebreak.
    // `ascends` is the protocol's own direction; `sortsBefore` is that or its
    // mirror, depending on setReverseOrder().
    static bool ascends(const Item &lhs, const Item &rhs);
    [[nodiscard]] bool sortsBefore(const Item &lhs, const Item &rhs) const;
    [[nodiscard]] int lowerBound(const Item &item) const;
    void rebuildIndex(int fromRow);

    // Applies everything buffered since the last boundary. Splits the pending
    // work into removes, in-place replacements, moves, and new rows, and emits
    // one insert transaction per contiguous run of destination rows — so a
    // fill, a prepended history page, and an appended one are each a single
    // model change instead of one per item.
    void flushBatch();
    // Applies one upsert immediately (the pre-batching path); flushBatch uses
    // it for the handful of shapes that cannot be coalesced.
    void applyUpsert(Item next);
    void insertSortedRun(QList<Item> fresh);

    QList<Item> m_items;
    QHash<QString, int> m_indexById;
    bool m_ready = false;
    bool m_exhausted = false;

    // Events buffered between batch boundaries. pendingOrder preserves arrival
    // order so a remove followed by a re-upsert of the same id resolves the way
    // the wire meant it to. Only ever non-empty while m_batching.
    struct PendingOp {
        bool remove = false;
        Item item;
    };
    QHash<QString, PendingOp> m_pending;
    QList<QString> m_pendingOrder;
    bool m_batching = false;
    bool m_reverseOrder = false;
};

} // namespace whatevr::proto
