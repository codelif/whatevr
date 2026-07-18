#pragma once

#include <QAbstractListModel>
#include <QDate>
#include <QFont>
#include <QFontMetricsF>
#include <QHash>
#include <QStringList>
#include <QVariantMap>

#include <cstdint>

#include "collectionviewmodel.h"
#include "messagemarkup.h"

// Presentation adapter for protocol `messages` items. The source collection
// remains the sole owner of keys and ordering; this model mirrors its rows
// exactly and only derives strings, grouping, markup, and nested-field roles
// consumed by the conversation UI.
class ProtocolMessageModel final : public QAbstractListModel
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

    explicit ProtocolMessageModel(whatevr::proto::CollectionViewModel *source, QObject *parent = nullptr);

    [[nodiscard]] int rowCount(const QModelIndex &parent = QModelIndex()) const override;
    [[nodiscard]] QVariant data(const QModelIndex &index, int role = Qt::DisplayRole) const override;
    [[nodiscard]] QHash<int, QByteArray> roleNames() const override;

    [[nodiscard]] Q_INVOKABLE int indexOf(const QString &messageId) const;
    [[nodiscard]] Q_INVOKABLE QString messageIdAt(int row) const;
    [[nodiscard]] Q_INVOKABLE QString dateTextForRow(int row) const;
    Q_INVOKABLE void setBodyMetricsFont(const QFont &font);
    Q_INVOKABLE bool expandMessageText(const QString &messageId);
    [[nodiscard]] Q_INVOKABLE QString copyTextForMessages(const QStringList &messageIds) const;
    [[nodiscard]] Q_INVOKABLE QVariantMap messageSnapshot(const QString &messageId) const;
    [[nodiscard]] Q_INVOKABLE QStringList allMessageIds() const;
    [[nodiscard]] Q_INVOKABLE QStringList messageIdsForDay(const QString &messageId) const;

private:
    struct TextPresentation {
        QString sourceText;
        QString previewText;
        whatevr::util::MessageMarkup previewMarkup;
        whatevr::util::MessageMarkup fullMarkup;
        QStringList links;
        qreal previewWidest = 0;
        qreal previewLast = 0;
        qreal fullWidest = 0;
        qreal fullLast = 0;
        bool truncated = false;
        bool fullParsed = false;
    };

    [[nodiscard]] QVariantMap wireItem(int row) const;
    [[nodiscard]] static QString displayText(const QVariantMap &item);
    [[nodiscard]] static QVariantMap sender(const QVariantMap &item);
    [[nodiscard]] static QVariantMap media(const QVariantMap &item);
    [[nodiscard]] static QVariantMap reply(const QVariantMap &item);
    [[nodiscard]] static QString senderDisplayName(const QVariantMap &item);
    [[nodiscard]] static QString initialsForName(const QString &name);
    [[nodiscard]] static int directionValue(const QString &direction);
    [[nodiscard]] static int statusValue(const QString &status);
    [[nodiscard]] static QString statusText(const QString &status);
    [[nodiscard]] static QString mediaKind(const QVariantMap &item);
    [[nodiscard]] static QVariantList reactions(const QVariantMap &item);
    [[nodiscard]] static QList<whatevr::util::MessageMention> mentions(const QVariantMap &item);
    [[nodiscard]] static int dayNumber(const QVariantMap &item);
    [[nodiscard]] static QString formatTime(qint64 timestampUnix);
    [[nodiscard]] static QString formatRelativeDate(qint64 timestampUnix);
    [[nodiscard]] QString cachedRelativeDate(const QVariantMap &item) const;
    [[nodiscard]] bool startsSenderGroup(int row) const;
    [[nodiscard]] bool endsSenderGroup(int row) const;
    [[nodiscard]] bool startsDayGroup(int row) const;
    [[nodiscard]] TextPresentation &ensureTextPresentation(const QVariantMap &item) const;
    void ensureFullTextPresentation(TextPresentation &presentation, const QVariantMap &item) const;
    void remeasure(TextPresentation &presentation) const;
    void invalidateRows(int first, int last);
    void emitAllRolesChanged(int first, int last);

    whatevr::proto::CollectionViewModel *m_source;
    mutable QHash<QString, TextPresentation> m_textById;
    QFont m_bodyFont;
    QFontMetricsF m_bodyMetrics;
    mutable QHash<int, QString> m_dateTextByDay;
    mutable QDate m_dateTextDay;
};
