#pragma once

#include <QObject>

class QEvent;

class WheelInputRouter final : public QObject
{
    Q_OBJECT
    Q_PROPERTY(bool accepted READ accepted NOTIFY acceptedChanged)

public:
    explicit WheelInputRouter(QObject *parent = nullptr);

    bool accepted() const;
    Q_INVOKABLE void acceptWheel();

Q_SIGNALS:
    void wheel(int globalX, int globalY, int pixelY, int angleY, int modifiers, int phase);
    void acceptedChanged();

protected:
    bool eventFilter(QObject *watched, QEvent *event) override;

private:
    void setAccepted(bool accepted);

    bool m_accepted = false;
};
