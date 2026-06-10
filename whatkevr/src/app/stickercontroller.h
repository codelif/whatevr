#pragma once

#include <QAbstractItemModel>
#include <QHash>
#include <QObject>
#include <QString>

#include <memory>

#include "whatevr/v1/whatevr.qpb.h"

QT_BEGIN_NAMESPACE
class QGrpcCallReply;
class QAbstractGrpcChannel;
class QTimer;
QT_END_NAMESPACE

namespace whatevr::v1::StickerService {
class Client;
}

class AppController;
class StickerModel;
class StickerPackModel;

// Drives the sticker side of the expression picker: library views (recents /
// favorites / packs / search), the store browser, lazy file downloads and
// sending. Owned by AppController, which shares its gRPC channel and forwards
// the sticker daemon events here.
class StickerController final : public QObject
{
    Q_OBJECT
    Q_PROPERTY(QAbstractItemModel *stickerModel READ stickerModel CONSTANT FINAL)
    Q_PROPERTY(QAbstractItemModel *packModel READ packModel CONSTANT FINAL)
    Q_PROPERTY(QAbstractItemModel *installedPackModel READ installedPackModel CONSTANT FINAL)
    Q_PROPERTY(bool loading READ loading NOTIFY viewChanged FINAL)
    Q_PROPERTY(bool packsLoading READ packsLoading NOTIFY viewChanged FINAL)
    Q_PROPERTY(QString errorText READ errorText NOTIFY viewChanged FINAL)
    // "recents" | "favorites" | "all" | "pack" | "search"
    Q_PROPERTY(QString activeSource READ activeSource NOTIFY viewChanged FINAL)
    Q_PROPERTY(QString activePackId READ activePackId NOTIFY viewChanged FINAL)
    Q_PROPERTY(QString activePackName READ activePackName NOTIFY viewChanged FINAL)
    Q_PROPERTY(bool activePackInstalled READ activePackInstalled NOTIFY viewChanged FINAL)

public:
    explicit StickerController(AppController *appController);
    ~StickerController() override;

    void attachChannel(const std::shared_ptr<QAbstractGrpcChannel> &channel);
    void handleLibraryChanged(whatevr::v1::StickerSourceGadget::StickerSource source);
    void handleDownloadChanged(const whatevr::v1::Sticker &sticker, const QString &errorText);

    [[nodiscard]] QAbstractItemModel *stickerModel() const;
    [[nodiscard]] QAbstractItemModel *packModel() const;
    [[nodiscard]] QAbstractItemModel *installedPackModel() const;
    [[nodiscard]] bool loading() const;
    [[nodiscard]] bool packsLoading() const;
    [[nodiscard]] QString errorText() const;
    [[nodiscard]] QString activeSource() const;
    [[nodiscard]] QString activePackId() const;
    [[nodiscard]] QString activePackName() const;
    [[nodiscard]] bool activePackInstalled() const;

    // First-open hook: loads recents + the pack list once, lazily, so users
    // who never open the sticker tab pay nothing.
    Q_INVOKABLE void activate();
    Q_INVOKABLE void showRecents();
    Q_INVOKABLE void showFavorites();
    Q_INVOKABLE void showAll();
    Q_INVOKABLE void showPack(const QString &packId);
    Q_INVOKABLE void search(const QString &query);
    Q_INVOKABLE void refreshStore();
    Q_INVOKABLE void setPackInstalled(const QString &packId, bool installed);
    Q_INVOKABLE void requestDownload(const QString &cacheKey);
    Q_INVOKABLE void sendSticker(const QString &cacheKey, const QString &replyToMessageId = QString());

Q_SIGNALS:
    void viewChanged();
    // Relayed to AppController so the optimistic bubble lands in the message
    // list exactly like text/image sends.
    void messageSent(const whatevr::v1::Message &message);
    // Lets the picker close (or flash an error) after a click-to-send.
    void stickerSent(const QString &cacheKey);
    void stickerSendFailed(const QString &cacheKey, const QString &errorText);

private:
    enum class View { Recents, Favorites, All, Pack, Search };

    void requestList(View view, const QString &packId = QString(), const QString &query = QString());
    void requestPacks(bool forceRefresh);
    void applyPackList(const QList<whatevr::v1::StickerPack> &packs);
    [[nodiscard]] bool clientReady() const;

    AppController *m_appController = nullptr;
    StickerModel *m_stickerModel = nullptr;
    StickerPackModel *m_packModel = nullptr;
    StickerPackModel *m_installedPackModel = nullptr;

    std::unique_ptr<whatevr::v1::StickerService::Client> m_client;
    std::unique_ptr<QGrpcCallReply> m_listReply;
    std::unique_ptr<QGrpcCallReply> m_packsReply;
    QHash<QString, std::shared_ptr<QGrpcCallReply>> m_downloadReplies;
    QHash<QString, std::shared_ptr<QGrpcCallReply>> m_installReplies;
    QHash<QString, std::shared_ptr<QGrpcCallReply>> m_sendReplies;

    QTimer *m_searchTimer = nullptr;
    QString m_pendingSearchQuery;

    View m_view = View::Recents;
    QString m_activePackId;
    QString m_activePackName;
    bool m_activePackInstalled = false;
    QString m_searchQuery;
    QString m_errorText;
    bool m_loading = false;
    bool m_packsLoading = false;
    bool m_activated = false;
    // Bumped on every view switch so stale list replies are dropped.
    quint64 m_viewGeneration = 0;
};
