#include "wheelinputrouter.h"

#include <QEvent>
#include <QWheelEvent>

WheelInputRouter::WheelInputRouter(QObject *parent)
    : QObject(parent)
{
}

bool WheelInputRouter::accepted() const
{
    return m_accepted;
}

void WheelInputRouter::acceptWheel()
{
    setAccepted(true);
}

bool WheelInputRouter::eventFilter(QObject *watched, QEvent *event)
{
    Q_UNUSED(watched)

    if (event->type() != QEvent::Wheel) {
        return false;
    }

    auto *wheelEvent = static_cast<QWheelEvent *>(event);
    setAccepted(false);

    const QPoint globalPosition = wheelEvent->globalPosition().toPoint();
    Q_EMIT wheel(globalPosition.x(),
                 globalPosition.y(),
                 wheelEvent->pixelDelta().y(),
                 wheelEvent->angleDelta().y(),
                 int(wheelEvent->modifiers()),
                 int(wheelEvent->phase()));

    if (!m_accepted) {
        return false;
    }

    wheelEvent->accept();
    return true;
}

void WheelInputRouter::setAccepted(bool accepted)
{
    if (m_accepted == accepted) {
        return;
    }

    m_accepted = accepted;
    Q_EMIT acceptedChanged();
}
