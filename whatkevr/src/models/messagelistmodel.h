#pragma once

#include <QAbstractListModel>
#include <QList>
#include <QString>

namespace whatevr::v1 {
class Message;
}

class MessageListModel final : public QAbstractListModel
{
    Q_OBJECT

public:
    enum Role {
        IdRole = Qt::UserRole + 1,
        ChatIdRole,
        SenderIdRole,
        SenderNameRole,
        SenderAvatarLocalPathRole,
        SenderInitialsRole,
        TextRole,
        TimestampUnixRole,
        TimeTextRole,
        DirectionRole,
        StatusRole,
        StatusTextRole,
        IsOutgoingRole,
        MediaMimeTypeRole,
        MediaLocalPathRole,
        MediaThumbnailLocalPathRole,
        MediaWidthRole,
        MediaHeightRole,
        ShowSenderHeaderRole,
        ShowSenderAvatarRole,
        ShowSenderGutterRole,
        GroupStartRole,
        GroupEndRole,
    };
    Q_ENUM(Role)

    explicit MessageListModel(QObject *parent = nullptr);

    static constexpr int MaximumMessageCount = 80;

    [[nodiscard]] int rowCount(const QModelIndex &parent = QModelIndex()) const override;
    [[nodiscard]] QVariant data(const QModelIndex &index, int role = Qt::DisplayRole) const override;
    [[nodiscard]] QHash<int, QByteArray> roleNames() const override;

    void replaceMessages(const QList<whatevr::v1::Message> &messages);
    void prependMessages(const QList<whatevr::v1::Message> &messages);
    void clear();
    void upsertMessage(const whatevr::v1::Message &message);
    void setGroupChat(bool groupChat);
    [[nodiscard]] bool isEmpty() const;
    [[nodiscard]] int messageCount() const;
    [[nodiscard]] QString oldestMessageId() const;

private:
    struct MessageItem {
        QString id;
        QString chatId;
        QString senderId;
        QString senderName;
        QString senderAvatarLocalPath;
        QString text;
        qint64 timestampUnix = 0;
        int direction = 0;
        int status = 0;
        QString mediaMimeType;
        QString mediaLocalPath;
        QString mediaThumbnailLocalPath;
        int mediaWidth = 0;
        int mediaHeight = 0;
    };

    static MessageItem fromProto(const whatevr::v1::Message &message);
    static QString formatTime(qint64 timestampUnix);
    static QString statusText(int status);
    static QString displaySenderName(const MessageItem &message);
    static QString initialsForName(const QString &name);
    static bool sameMessages(const QList<MessageItem> &left, const QList<MessageItem> &right);
    static bool sameMessageOrder(const QList<MessageItem> &left, const QList<MessageItem> &right);
    static bool sameMessageData(const MessageItem &left, const MessageItem &right);
    [[nodiscard]] bool isOutgoing(const MessageItem &message) const;
    [[nodiscard]] bool startsSenderGroup(int row) const;
    [[nodiscard]] bool endsSenderGroup(int row) const;
    [[nodiscard]] int indexOf(const QString &messageId) const;

    QList<MessageItem> m_messages;
    bool m_groupChat = false;
};
