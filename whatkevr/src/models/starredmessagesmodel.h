#pragma once

#include <QAbstractListModel>
#include <QList>
#include <QString>

#include <cstdint>

namespace whatevr::v1 {
class Message;
class StarredMessageItem;
}

// Flat list backing the "Starred messages" view. Populated on demand from
// ChatService.ListStarredMessages; rows carry enough display state (sender, the
// owning chat's name, a body preview) to render without touching the message
// model, plus the chat/message ids needed to jump into the conversation.
class StarredMessagesModel final : public QAbstractListModel
{
    Q_OBJECT

public:
    enum Role : std::uint16_t {
        MessageIdRole = Qt::UserRole + 1,
        ChatIdRole,
        ChatNameRole,
        SenderNameRole,
        PreviewRole,
        MediaKindRole,
        TimeTextRole,
        TimestampUnixRole,
        IsOutgoingRole,
    };
    Q_ENUM(Role)

    explicit StarredMessagesModel(QObject *parent = nullptr);

    [[nodiscard]] int rowCount(const QModelIndex &parent = QModelIndex()) const override;
    [[nodiscard]] QVariant data(const QModelIndex &index, int role = Qt::DisplayRole) const override;
    [[nodiscard]] QHash<int, QByteArray> roleNames() const override;

    void replace(const QList<whatevr::v1::StarredMessageItem> &items);
    void clear();
    // Keeps an open view honest: a message that gets unstarred (or deleted)
    // elsewhere drops out of the list live via the daemon's MessageUpdated.
    void applyMessage(const whatevr::v1::Message &message);
    bool removeMessage(const QString &messageId);

private:
    struct Item {
        QString messageId;
        QString chatId;
        QString chatName;
        QString senderName;
        QString preview;
        QString mediaKind;
        QString timeText;
        qint64 timestampUnix = 0;
        bool isOutgoing = false;
    };

    static Item fromProto(const whatevr::v1::Message &message, const QString &chatName);
    [[nodiscard]] int indexOfMessage(const QString &messageId) const;

    QList<Item> m_items;
};
