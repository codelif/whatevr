#include "objectviewmodel.h"

namespace whatevr::proto
{

ObjectViewModel::ObjectViewModel(QObject *parent)
    : QObject(parent)
{
}

void ObjectViewModel::onUpsert(const QString &sort, const QJsonObject &item)
{
    Q_UNUSED(sort) // a single-item view has nothing to order
    m_value = item.toVariantMap();
    m_present = true;
    Q_EMIT valueChanged();
}

void ObjectViewModel::onRemove(const QString &id)
{
    Q_UNUSED(id)
    if (!m_present && m_value.isEmpty()) {
        return;
    }
    m_value.clear();
    m_present = false;
    Q_EMIT valueChanged();
}

void ObjectViewModel::onReady(bool exhausted, bool hasExhausted)
{
    Q_UNUSED(exhausted)
    Q_UNUSED(hasExhausted)
    if (m_ready) {
        return;
    }
    m_ready = true;
    Q_EMIT readyChanged();
}

void ObjectViewModel::onReset()
{
    if (m_present || !m_value.isEmpty()) {
        m_value.clear();
        m_present = false;
        Q_EMIT valueChanged();
    }
    if (m_ready) {
        m_ready = false;
        Q_EMIT readyChanged();
    }
}

} // namespace whatevr::proto
