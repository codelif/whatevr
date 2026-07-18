#pragma once

#include <QObject>
#include <QVariantMap>

#include "protocolclient.h"

namespace whatevr::proto
{

// A generic wrapper for an object view — a view that delivers a single item
// (id "self" or similar) through the same upsert grammar (PROTOCOL.md "The
// view model"). It holds that one item as a QVariantMap property; QML binds
// `object.<field>` off `value`. Like the collection model it interprets
// nothing: an upsert replaces the held item wholesale, a remove or reset
// clears it.
class ObjectViewModel final : public QObject, public ViewSink
{
    Q_OBJECT
    Q_PROPERTY(QVariantMap value READ value NOTIFY valueChanged FINAL)
    Q_PROPERTY(bool present READ isPresent NOTIFY valueChanged FINAL)
    Q_PROPERTY(bool ready READ isReady NOTIFY readyChanged FINAL)

public:
    explicit ObjectViewModel(QObject *parent = nullptr);

    [[nodiscard]] QVariantMap value() const { return m_value; }
    [[nodiscard]] bool isPresent() const { return m_present; }
    [[nodiscard]] bool isReady() const { return m_ready; }

    // ViewSink
    void onUpsert(const QString &sort, const QJsonObject &item) override;
    void onRemove(const QString &id) override;
    void onReady(bool exhausted, bool hasExhausted) override;
    void onReset() override;

Q_SIGNALS:
    void valueChanged();
    void readyChanged();

private:
    QVariantMap m_value;
    bool m_present = false;
    bool m_ready = false;
};

} // namespace whatevr::proto
