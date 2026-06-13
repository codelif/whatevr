#pragma once

#include <QAbstractListModel>
#include <QList>
#include <QString>
#include <QVariantMap>

#include <cstdint>

namespace whatevr::v1 {
class Message;
}

// Backs a conversation's pinned-message banner. Holds the chat's currently
// pinned (unexpired) messages, oldest pin first, so the banner can step through
// them. Stays live off the daemon's MessageUpdated stream: a message becoming
// pinned is inserted, an unpinned/expired one removed.
class PinnedMessagesModel final : public QAbstractListModel
{
    Q_OBJECT
    Q_PROPERTY(int count READ count NOTIFY countChanged FINAL)

public:
    enum Role : std::uint16_t {
        MessageIdRole = Qt::UserRole + 1,
        ChatIdRole,
        SenderNameRole,
        PreviewRole,
        MediaKindRole,
        TimeTextRole,
        PinnedUntilUnixRole,
        IsOutgoingRole,
    };
    Q_ENUM(Role)

    explicit PinnedMessagesModel(QObject *parent = nullptr);

    [[nodiscard]] int rowCount(const QModelIndex &parent = QModelIndex()) const override;
    [[nodiscard]] QVariant data(const QModelIndex &index, int role = Qt::DisplayRole) const override;
    [[nodiscard]] QHash<int, QByteArray> roleNames() const override;

    [[nodiscard]] int count() const;
    // Row's display fields as a map, so the single-row pinned banner can show
    // any pin without instantiating a delegate per row.
    [[nodiscard]] Q_INVOKABLE QVariantMap get(int row) const;

    void replace(const QList<whatevr::v1::Message> &messages);
    void clear();
    // Insert/update when the message is pinned and unexpired, remove otherwise.
    void applyMessage(const whatevr::v1::Message &message);
    bool removeMessage(const QString &messageId);

Q_SIGNALS:
    void countChanged();

private:
    struct Item {
        QString messageId;
        QString chatId;
        QString senderName;
        QString preview;
        QString mediaKind;
        QString timeText;
        qint64 pinnedUntilUnix = 0;
        bool isOutgoing = false;
    };

    static Item fromProto(const whatevr::v1::Message &message);
    [[nodiscard]] int indexOfMessage(const QString &messageId) const;
    // Keeps the list ordered by pin expiry so banner navigation is stable.
    [[nodiscard]] int insertionPos(qint64 pinnedUntilUnix) const;

    QList<Item> m_items;
};
