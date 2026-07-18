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

    [[nodiscard]] bool isReady() const { return m_ready; }
    [[nodiscard]] bool isExhausted() const { return m_exhausted; }
    [[nodiscard]] int count() const { return static_cast<int>(m_items.size()); }

    // ViewSink
    void onUpsert(const QString &sort, const QJsonObject &item) override;
    void onRemove(const QString &id) override;
    void onReady(bool exhausted, bool hasExhausted) override;
    void onReset() override;

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
    static bool sortsBefore(const Item &lhs, const Item &rhs);
    [[nodiscard]] int lowerBound(const Item &item) const;
    void rebuildIndex(int fromRow);

    QList<Item> m_items;
    QHash<QString, int> m_indexById;
    bool m_ready = false;
    bool m_exhausted = false;
};

} // namespace whatevr::proto
