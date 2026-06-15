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
    Q_PROPERTY(Category chatCategory READ chatCategory WRITE setChatCategory NOTIFY chatCategoryChanged FINAL)

public:
    // Chat-type filter, layered on top of the text filter. All = no-op so the
    // text-only callers (e.g. the forward picker) keep their current behaviour.
    enum Category {
        All,
        DirectMessages,
        Groups,
    };
    Q_ENUM(Category)

    explicit ChatListFilterModel(QObject *parent = nullptr);

    QString filterText() const;
    void setFilterText(const QString &text);

    QString filterRoleName() const;
    void setFilterRoleName(const QString &roleName);

    Category chatCategory() const;
    void setChatCategory(Category category);

    void setSourceModel(QAbstractItemModel *sourceModel) override;

Q_SIGNALS:
    void filterTextChanged();
    void filterRoleNameChanged();
    void chatCategoryChanged();

protected:
    bool filterAcceptsRow(int sourceRow, const QModelIndex &sourceParent) const override;

private:
    void applyFilterRole();
    void resolveIsGroupRole();

    QString m_filterText;
    QString m_filterRoleName = QStringLiteral("name");
    Category m_chatCategory = All;
    int m_isGroupRole = -1;
};
