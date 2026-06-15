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

ChatListFilterModel::Category ChatListFilterModel::chatCategory() const
{
    return m_chatCategory;
}

void ChatListFilterModel::setChatCategory(Category category)
{
    if (m_chatCategory == category) {
        return;
    }
    m_chatCategory = category;
    invalidateFilter();
    Q_EMIT chatCategoryChanged();
}

void ChatListFilterModel::setSourceModel(QAbstractItemModel *sourceModel)
{
    QSortFilterProxyModel::setSourceModel(sourceModel);
    applyFilterRole();
    resolveIsGroupRole();
}

bool ChatListFilterModel::filterAcceptsRow(int sourceRow, const QModelIndex &sourceParent) const
{
    if (!QSortFilterProxyModel::filterAcceptsRow(sourceRow, sourceParent)) {
        return false;
    }
    if (m_chatCategory == All || m_isGroupRole < 0) {
        return true;
    }
    const QModelIndex index = sourceModel()->index(sourceRow, 0, sourceParent);
    const bool isGroup = index.data(m_isGroupRole).toBool();
    return m_chatCategory == Groups ? isGroup : !isGroup;
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

void ChatListFilterModel::resolveIsGroupRole()
{
    m_isGroupRole = -1;
    const QAbstractItemModel *source = sourceModel();
    if (source == nullptr) {
        return;
    }
    const QHash<int, QByteArray> roles = source->roleNames();
    for (auto it = roles.constBegin(); it != roles.constEnd(); ++it) {
        if (it.value() == QByteArrayLiteral("isGroup")) {
            m_isGroupRole = it.key();
            return;
        }
    }
}
