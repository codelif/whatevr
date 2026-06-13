#pragma once

#include <QSortFilterProxyModel>
#include <QString>

#include <QtQml/qqmlregistration.h>

// Case-insensitive name filter over ChatListModel (or any list model exposing a
// text role). Instantiable from QML so dialogs like the forward picker can wrap
// the shared chat list with a local search field without disturbing it. The
// filtered role is resolved by name from the source model's roleNames(), so the
// proxy doesn't need to know ChatListModel's numeric role values.
class ChatListFilterModel : public QSortFilterProxyModel
{
    Q_OBJECT
    QML_ELEMENT
    Q_PROPERTY(QString filterText READ filterText WRITE setFilterText NOTIFY filterTextChanged FINAL)
    Q_PROPERTY(QString filterRoleName READ filterRoleName WRITE setFilterRoleName NOTIFY filterRoleNameChanged FINAL)

public:
    explicit ChatListFilterModel(QObject *parent = nullptr);

    QString filterText() const;
    void setFilterText(const QString &text);

    QString filterRoleName() const;
    void setFilterRoleName(const QString &roleName);

    void setSourceModel(QAbstractItemModel *sourceModel) override;

Q_SIGNALS:
    void filterTextChanged();
    void filterRoleNameChanged();

private:
    void applyFilterRole();

    QString m_filterText;
    QString m_filterRoleName = QStringLiteral("name");
};
