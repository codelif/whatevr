#pragma once

#include <QAbstractListModel>
#include <QHash>
#include <QList>
#include <QString>
#include <QStringList>

#include <cstdint>

namespace whatevr::v1 {
class Message;
}

class MessageListModel final : public QAbstractListModel
{
    Q_OBJECT

public:
    enum Role : std::uint16_t {
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
        MediaKindRole,
        MediaMimeTypeRole,
        MediaLocalPathRole,
        MediaThumbnailLocalPathRole,
        MediaWidthRole,
        MediaHeightRole,
        MediaAnimatedRole,
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
    bool updateSenderAvatar(const QString &senderId, const QString &avatarLocalPath);
    [[nodiscard]] QStringList uniqueIncomingSenderIds() const;
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
        QString senderDisplayName;
        QString senderInitials;
        QString senderAvatarLocalPath;
        QString text;
        qint64 timestampUnix = 0;
        QString timeText;
        int direction = 0;
        int status = 0;
        QString statusText;
        QString mediaKind;
        QString mediaMimeType;
        QString mediaLocalPath;
        QString mediaThumbnailLocalPath;
        int mediaWidth = 0;
        int mediaHeight = 0;
        bool mediaAnimated = false;
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
    void rebuildIndex();

    QList<MessageItem> m_messages;
    QHash<QString, int> m_messageIndexById;
    bool m_groupChat = false;
};
