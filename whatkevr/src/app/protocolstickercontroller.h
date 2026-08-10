#pragma once

#include <QAbstractItemModel>
#include <QList>
#include <QObject>
#include <QPointer>
#include <QSet>
#include <QString>
#include <QtTypes>

QT_BEGIN_NAMESPACE
class QTimer;
QT_END_NAMESPACE

namespace whatevr::proto
{
class CollectionViewModel;
class ProtocolClient;
class Subscription;
} // namespace whatevr::proto

// Protocol-backed sticker picker surface. ProtocolController will own this
// object and supply its shared ProtocolClient and selected chat id.
class ProtocolStickerController final : public QObject
{
    Q_OBJECT
    Q_PROPERTY(QAbstractItemModel *stickerModel READ stickerModel NOTIFY viewChanged FINAL)
    Q_PROPERTY(QAbstractItemModel *packModel READ packModel CONSTANT FINAL)
    Q_PROPERTY(bool loading READ loading NOTIFY viewChanged FINAL)
    Q_PROPERTY(bool downloading READ downloading NOTIFY viewChanged FINAL)
    Q_PROPERTY(bool packsLoading READ packsLoading NOTIFY viewChanged FINAL)
    Q_PROPERTY(QString errorText READ errorText NOTIFY viewChanged FINAL)
    Q_PROPERTY(QString activeSource READ activeSource NOTIFY viewChanged FINAL)
    Q_PROPERTY(QString activePackId READ activePackId NOTIFY viewChanged FINAL)
    Q_PROPERTY(QString activePackName READ activePackName NOTIFY viewChanged FINAL)
    Q_PROPERTY(bool activePackInstalled READ activePackInstalled NOTIFY viewChanged FINAL)

public:
    explicit ProtocolStickerController(whatevr::proto::ProtocolClient *client, QObject *parent = nullptr);
    ~ProtocolStickerController() override;

    [[nodiscard]] QAbstractItemModel *stickerModel() const;
    [[nodiscard]] QAbstractItemModel *packModel() const;
    [[nodiscard]] bool loading() const;
    [[nodiscard]] bool downloading() const;
    [[nodiscard]] bool packsLoading() const;
    [[nodiscard]] QString errorText() const;
    [[nodiscard]] QString activeSource() const;
    [[nodiscard]] QString activePackId() const;
    [[nodiscard]] QString activePackName() const;
    [[nodiscard]] bool activePackInstalled() const;

    // ProtocolController calls this whenever conversation selection changes.
    void setChatId(QString chatId);

    Q_INVOKABLE void activate();
    Q_INVOKABLE void deactivate();
    Q_INVOKABLE void showRecents();
    Q_INVOKABLE void showFavorites();
    Q_INVOKABLE void showAll();
    Q_INVOKABLE void showPack(const QString &packId);
    Q_INVOKABLE void search(const QString &query);
    Q_INVOKABLE void refreshStore();
    Q_INVOKABLE void setPackInstalled(const QString &packId, bool installed);
    Q_INVOKABLE void requestDownload(const QString &cacheKey);
    Q_INVOKABLE void sendSticker(const QString &cacheKey, const QString &replyToMessageId = QString());
    Q_INVOKABLE void setStickerFavorite(const QString &cacheKey, const QString &messageId, bool favorite);
    Q_INVOKABLE void beginFavoriteTracking();
    Q_INVOKABLE void endFavoriteTracking();
    Q_INVOKABLE [[nodiscard]] bool isStickerFavorite(const QString &cacheKey);

Q_SIGNALS:
    void favoritesChanged();
    void stickerFavoriteFailed(const QString &errorText);
    void viewChanged();
    void stickerSent(const QString &cacheKey);
    void stickerSendFailed(const QString &cacheKey, const QString &errorText);

private:
    enum class Source { Recents, Favorites, All, Pack, Search };

    void switchSource(Source source, const QString &packId = QString());
    void subscribeActiveView();
    void subscribePacks();
    void ensureFavoriteSubscription();
    void runSearch();
    void pumpDownloads();
    void updateDownloading();

    QPointer<whatevr::proto::ProtocolClient> m_client;
    whatevr::proto::CollectionViewModel *m_stickerViewModel = nullptr;
    QAbstractItemModel *m_searchModel = nullptr;
    whatevr::proto::CollectionViewModel *m_packModel = nullptr;
    whatevr::proto::CollectionViewModel *m_favoriteModel = nullptr;
    QPointer<whatevr::proto::Subscription> m_stickerSub;
    QPointer<whatevr::proto::Subscription> m_packSub;
    QPointer<whatevr::proto::Subscription> m_favoriteSub;
    QTimer *m_searchTimer = nullptr;

    Source m_source = Source::Recents;
    QString m_activePackId;
    QString m_pendingSearchQuery;
    QString m_chatId;
    QString m_errorText;
    bool m_activated = false;
    bool m_activeSubscriptionFailed = false;
    bool m_packSubscriptionFailed = false;
    bool m_searchLoading = false;
    bool m_refreshInFlight = false;
    bool m_downloading = false;
    quint64 m_searchGeneration = 0;

    QList<QString> m_downloadQueue;
    QSet<QString> m_downloadQueued;
    QSet<QString> m_downloadInFlight;
    QSet<QString> m_installInFlight;
    QSet<QString> m_favoriteInFlight;
    QSet<QString> m_sendInFlight;
};
