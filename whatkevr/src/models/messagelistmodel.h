#pragma once

#include <QAbstractListModel>
#include <QDate>
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
        LayoutTextRole,
        EmojiOnlyCountRole,
        HasRichTextRole,
        RichTextRole,
        TextPreviewRole,
        LayoutTextPreviewRole,
        PreviewHasRichTextRole,
        PreviewRichTextRole,
        TextTruncatedRole,
        TimestampUnixRole,
        TimeTextRole,
        DateSeparatorTextRole,
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
        MediaDownloadingRole,
        MediaDownloadErrorRole,
        ReplyToMessageIdRole,
        ReplyToSenderNameRole,
        ReplyToTextRole,
        ReplyToMediaKindRole,
        ReplyToMediaMimeTypeRole,
        ReplyToIsOutgoingRole,
        WidestLineWidthRole,
        LastLineWidthRole,
    };
    Q_ENUM(Role)

    explicit MessageListModel(QObject *parent = nullptr);

    static constexpr int MaximumMessageCount = 80;

    [[nodiscard]] int rowCount(const QModelIndex &parent = QModelIndex()) const override;
    [[nodiscard]] QVariant data(const QModelIndex &index, int role = Qt::DisplayRole) const override;
    [[nodiscard]] QHash<int, QByteArray> roleNames() const override;

    void replaceMessages(const QList<whatevr::v1::Message> &messages);
    // Messages are stored newest-first (index 0 == newest). Older history pages
    // are therefore appended at the end of the list.
    void appendOlderMessages(const QList<whatevr::v1::Message> &messages);
    void clear();
    void upsertMessage(const whatevr::v1::Message &message);
    bool updateSenderAvatar(const QString &senderId, const QString &avatarLocalPath);
    bool setMediaDownloadState(const QString &messageId, bool downloading, const QString &errorText = QString());
    [[nodiscard]] QStringList uniqueIncomingSenderIds() const;
    void setGroupChat(bool groupChat);
    [[nodiscard]] bool isEmpty() const;
    [[nodiscard]] int messageCount() const;
    [[nodiscard]] QString oldestMessageId() const;
    [[nodiscard]] Q_INVOKABLE int indexOf(const QString &messageId) const;
    [[nodiscard]] Q_INVOKABLE QString dateTextForRow(int row) const;
    Q_INVOKABLE bool expandMessageText(const QString &messageId);

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
        // Derived text state (preview collapse, markup/emoji parse) is computed
        // lazily on first access from data(): parsing every message eagerly in
        // fromProto() made chat switches O(cached messages) on the GUI thread.
        // The fields are mutable so the const data() path can fill the cache;
        // it must never emit signals.
        mutable QString layoutText;
        mutable int emojiOnlyCount = 0;
        mutable bool hasRichText = false;
        mutable QString richText;
        mutable bool fullMarkupParsed = false;
        mutable bool previewParsed = false;
        mutable QString textPreview;
        mutable QString layoutTextPreview;
        mutable bool previewHasRichText = false;
        mutable QString previewRichText;
        mutable bool textTruncated = false;
        // Unwrapped advance width of the widest and last line of the displayed
        // body text, measured with the application font. Replaces per-delegate
        // TextMetrics + JS line splitting in ChatBubble (it ran for every
        // delegate created during scrolling).
        mutable qreal widestLineWidth = 0;
        mutable qreal lastLineWidth = 0;
        qint64 timestampUnix = 0;
        int dayNumber = 0;
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
        bool mediaDownloading = false;
        QString mediaDownloadError;
        QString replyToMessageId;
        QString replyToSenderId;
        QString replyToSenderName;
        QString replyToText;
        QString replyToMediaKind;
        QString replyToMediaMimeType;
        int replyToDirection = 0;
    };

    static MessageItem fromProto(const whatevr::v1::Message &message);
    static void ensurePreviewParsed(const MessageItem &item);
    static void transplantParsedState(const MessageItem &target, const MessageItem &source);
    static QString formatTime(qint64 timestampUnix);
    static QString formatRelativeDate(qint64 timestampUnix);
    static QString statusText(int status);
    static QString displaySenderName(const MessageItem &message);
    static QString initialsForName(const QString &name);
    static bool sameMessages(const QList<MessageItem> &left, const QList<MessageItem> &right);
    static bool sameMessageOrder(const QList<MessageItem> &left, const QList<MessageItem> &right);
    static bool sameMessageData(const MessageItem &left, const MessageItem &right);
    [[nodiscard]] bool isOutgoing(const MessageItem &message) const;
    [[nodiscard]] bool startsSenderGroup(int row) const;
    [[nodiscard]] bool endsSenderGroup(int row) const;
    [[nodiscard]] bool startsDayGroup(int row) const;
    [[nodiscard]] QString cachedRelativeDate(const MessageItem &message) const;
    void rebuildIndex();
    void emitGroupingRolesChanged(int firstRow, int lastRow);

    QList<MessageItem> m_messages;
    QHash<QString, int> m_messageIndexById;
    bool m_groupChat = false;
    // Relative date strings ("Today", "Monday", …) memoized per local Julian
    // day; data() is hot during scrolling and formatRelativeDate allocates.
    // The cache resets when the calendar day rolls over.
    mutable QHash<int, QString> m_dateTextByDay;
    mutable QDate m_dateTextDay;
};
