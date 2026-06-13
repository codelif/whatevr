#pragma once

#include <QAbstractListModel>
#include <QDate>
#include <QHash>
#include <QList>
#include <QString>
#include <QStringList>
#include <QTimer>
#include <QVariant>
#include <QVariantList>

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
        LinksRole,
        HasLinksRole,
        MediaCacheKeyRole,
        IsRevokedRole,
        IsEditedRole,
        IsStarredRole,
        IsPinnedRole,
        PinnedUntilUnixRole,
        ReactionsRole,
        MediaDownloadProgressRole,
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
    bool removeMessage(const QString &messageId);
    bool updateSenderAvatar(const QString &senderId, const QString &avatarLocalPath);
    bool setMediaDownloadState(const QString &messageId, bool downloading, const QString &errorText = QString(),
                               qint64 receivedBytes = 0, qint64 totalBytes = 0);
    // Oldest message of the chat's unread region, derived by counting the
    // `unreadCount` most recent incoming, non-revoked messages (the daemon's
    // badge counts exactly those). `complete` reports whether the loaded
    // window actually covered unreadCount such messages; when it did not, the
    // returned id is the oldest qualifying message that is loaded.
    [[nodiscard]] QString unreadAnchorCandidate(int unreadCount, bool *complete = nullptr) const;
    // Optimistically apply the viewer's reaction to a message so the pill updates
    // before the daemon round-trips. Drops any existing fromMe reaction and, when
    // emoji is non-empty, appends a fromMe entry. Returns the previous reactions
    // list so the caller can revert with restoreReactions() if the call fails.
    QVariantList applyOptimisticReaction(const QString &messageId, const QString &emoji);
    void restoreReactions(const QString &messageId, const QVariantList &reactions);
    // Optimistically flip a message's star so the bubble's star icon updates
    // before the daemon round-trips. Returns the previous starred state so the
    // caller can revert with restoreStar() on failure.
    bool applyOptimisticStar(const QString &messageId, bool starred);
    void restoreStar(const QString &messageId, bool starred);
    // Optimistically apply an edit to a message's body so the bubble updates
    // before the daemon round-trips. Sets the new text, flags it edited, and
    // drops cached layout/markup so the new body re-renders. Returns the
    // previous text so the caller can revert with restoreText() on failure.
    QString applyOptimisticEdit(const QString &messageId, const QString &newText);
    void restoreText(const QString &messageId, const QString &oldText, bool wasEdited);
    [[nodiscard]] QStringList uniqueIncomingSenderIds() const;
    void setGroupChat(bool groupChat);
    [[nodiscard]] bool isEmpty() const;
    [[nodiscard]] int messageCount() const;
    [[nodiscard]] QString oldestMessageId() const;
    [[nodiscard]] Q_INVOKABLE int indexOf(const QString &messageId) const;
    [[nodiscard]] Q_INVOKABLE QString dateTextForRow(int row) const;
    Q_INVOKABLE bool expandMessageText(const QString &messageId);
    // WhatsApp-style multi-message copy: chronological "[date, time] Sender:
    // text" lines. A single message copies its bare full text.
    [[nodiscard]] Q_INVOKABLE QString copyTextForMessages(const QStringList &messageIds) const;
    // One message's menu-relevant fields as a map, so QML actions (context
    // menu, selection toolbar) don't need per-role model plumbing.
    [[nodiscard]] Q_INVOKABLE QVariantMap messageSnapshot(const QString &messageId) const;
    [[nodiscard]] Q_INVOKABLE QStringList allMessageIds() const;
    // All message ids sharing the same calendar day as the given message, used
    // to toggle a whole day's selection from its date separator pill.
    [[nodiscard]] Q_INVOKABLE QStringList messageIdsForDay(const QString &messageId) const;

Q_SIGNALS:
    // Emitted whenever replaceMessages() swaps in a new displayed set without going
    // through beginResetModel() (the incremental insert/remove and in-place update
    // paths). The view uses it to finalise a chat-open, since onModelReset only fires
    // on the rare full-reset path.
    void modelReplaced();

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
        // Clickable links in the full (not collapsed) text; filled in the same
        // lazy parse pass as the preview cache.
        mutable QStringList links = {};
        qint64 timestampUnix = 0;
        // Daemon-assigned monotonic insertion order; tiebreaker within a single
        // timestamp second so same-second messages keep their true arrival order.
        qint64 sortSeq = 0;
        int dayNumber = 0;
        QString timeText;
        int direction = 0;
        int status = 0;
        QString statusText;
        QString mediaKind;
        QString mediaMimeType;
        QString mediaLocalPath;
        QString mediaThumbnailLocalPath;
        QString mediaCacheKey;
        int mediaWidth = 0;
        int mediaHeight = 0;
        bool mediaAnimated = false;
        bool isRevoked = false;
        bool isEdited = false;
        bool isStarred = false;
        qint64 pinnedUntilUnix = 0;
        bool mediaDownloading = false;
        QString mediaDownloadError;
        // Streamed download progress; totalBytes 0 means unknown size and the
        // progress role reports -1 (indeterminate).
        qint64 mediaDownloadReceivedBytes = 0;
        qint64 mediaDownloadTotalBytes = 0;
        QString replyToMessageId;
        QString replyToSenderId;
        QString replyToSenderName;
        QString replyToText;
        QString replyToMediaKind;
        QString replyToMediaMimeType;
        int replyToDirection = 0;
        // Each entry is a QVariantMap {emoji, senderId, senderName, fromMe}.
        // Aggregation into pills is done in QML.
        QVariantList reactions;
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
    void scheduleParseWarmup(int fromRow);
    void parseWarmupTick();

    QList<MessageItem> m_messages;
    QHash<QString, int> m_messageIndexById;
    bool m_groupChat = false;
    // Idle warm-up of the lazy preview/markup cache: parses unparsed rows in
    // small time-budgeted slices between event-loop passes, so scrolling never
    // pays ensurePreviewParsed() inside a frame. Never emits signals.
    QTimer m_warmupTimer;
    int m_warmupCursor = 0;
    // Relative date strings ("Today", "Monday", …) memoized per local Julian
    // day; data() is hot during scrolling and formatRelativeDate allocates.
    // The cache resets when the calendar day rolls over.
    mutable QHash<int, QString> m_dateTextByDay;
    mutable QDate m_dateTextDay;
};
