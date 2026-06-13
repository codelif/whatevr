#include "chatlistfiltermodel.h"

ChatListFilterModel::ChatListFilterModel(QObject *parent)
    : QSortFilterProxyModel(parent)
{
    setFilterCaseSensitivity(Qt::CaseInsensitive);
    setDynamicSortFilter(true);
}

QString ChatListFilterModel::filterText() const
{
    return m_filterText;
}

void ChatListFilterModel::setFilterText(const QString &text)
{
    if (m_filterText == text) {
        return;
    }
    m_filterText = text;
    setFilterFixedString(text);
    Q_EMIT filterTextChanged();
}

QString ChatListFilterModel::filterRoleName() const
{
    return m_filterRoleName;
}

void ChatListFilterModel::setFilterRoleName(const QString &roleName)
{
    if (m_filterRoleName == roleName) {
        return;
    }
    m_filterRoleName = roleName;
    applyFilterRole();
    Q_EMIT filterRoleNameChanged();
}

void ChatListFilterModel::setSourceModel(QAbstractItemModel *sourceModel)
{
    QSortFilterProxyModel::setSourceModel(sourceModel);
    applyFilterRole();
}

void ChatListFilterModel::applyFilterRole()
{
    const QAbstractItemModel *source = sourceModel();
    if (source == nullptr) {
        return;
    }
    const QByteArray wanted = m_filterRoleName.toUtf8();
    const QHash<int, QByteArray> roles = source->roleNames();
    for (auto it = roles.constBegin(); it != roles.constEnd(); ++it) {
        if (it.value() == wanted) {
            setFilterRole(it.key());
            return;
        }
    }
}
