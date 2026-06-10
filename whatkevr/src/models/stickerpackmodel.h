#pragma once

#include <QAbstractListModel>
#include <QList>
#include <QString>

#include <cstdint>

#include "whatevr/v1/whatevr.qpb.h"

// Sticker pack list. One instance holds the full store catalogue; a second,
// installed-only instance backs the picker's category row.
class StickerPackModel final : public QAbstractListModel
{
    Q_OBJECT
    Q_PROPERTY(int count READ rowCount NOTIFY countChanged FINAL)

public:
    enum Role : std::uint16_t {
        PackIdRole = Qt::UserRole + 1,
        NameRole,
        PublisherRole,
        DescriptionRole,
        TrayPathRole,
        InstalledRole,
        AnimatedRole,
        StickerCountRole,
    };
    Q_ENUM(Role)

    explicit StickerPackModel(QObject *parent = nullptr, bool installedOnly = false);

    [[nodiscard]] int rowCount(const QModelIndex &parent = QModelIndex()) const override;
    [[nodiscard]] QVariant data(const QModelIndex &index, int role = Qt::DisplayRole) const override;
    [[nodiscard]] QHash<int, QByteArray> roleNames() const override;

    void resetWith(const QList<whatevr::v1::StickerPack> &packs);

Q_SIGNALS:
    void countChanged();

private:
    QList<whatevr::v1::StickerPack> m_packs;
    bool m_installedOnly = false;
};
