#pragma once

#include <QAbstractListModel>
#include <QHash>
#include <QList>
#include <QSortFilterProxyModel>
#include <QString>

#include <cstdint>

#include "whatevr/v1/whatevr.qpb.h"

// Grid model for the sticker picker. Rows are patched in place as files
// finish downloading so the GridView never resets mid-scroll.
class StickerModel final : public QAbstractListModel
{
    Q_OBJECT
    Q_PROPERTY(int count READ rowCount NOTIFY countChanged FINAL)

public:
    enum Role : std::uint16_t {
        CacheKeyRole = Qt::UserRole + 1,
        LocalPathRole,
        MimeTypeRole,
        AnimatedRole,
        DownloadedRole,
        EmojisRole,
        AccessTextRole,
        FavoriteRole,
    };
    Q_ENUM(Role)

    explicit StickerModel(QObject *parent = nullptr);

    [[nodiscard]] int rowCount(const QModelIndex &parent = QModelIndex()) const override;
    [[nodiscard]] QVariant data(const QModelIndex &index, int role = Qt::DisplayRole) const override;
    [[nodiscard]] QHash<int, QByteArray> roleNames() const override;

    void resetWith(const QList<whatevr::v1::Sticker> &stickers);
    // Patches one row; oldCacheKey handles downloads that canonicalized a
    // provisional key. Unknown keys are ignored.
    void updateSticker(const whatevr::v1::Sticker &sticker, const QString &oldCacheKey = QString());

Q_SIGNALS:
    void countChanged();

private:
    void rebuildIndex();

    QList<whatevr::v1::Sticker> m_stickers;
    QHash<QString, int> m_rowByKey;
};

// Shows only fully-downloaded stickers. The picker binds to this proxy so the
// grid never displays loading spinners or broken/failed tiles and never
// reflows as placeholders resolve — a sticker appears the moment its file is
// ready (StickerController prefetches the misses in the background). Rows that
// are still downloading or terminally failed simply stay hidden.
class StickerFilterModel final : public QSortFilterProxyModel
{
    Q_OBJECT

public:
    explicit StickerFilterModel(QObject *parent = nullptr);

protected:
    [[nodiscard]] bool filterAcceptsRow(int sourceRow, const QModelIndex &sourceParent) const override;
};
