#include "protocolstickercontroller.h"

#include <QAbstractListModel>
#include <QByteArray>
#include <QHash>
#include <QJsonArray>
#include <QJsonObject>
#include <QJsonValue>
#include <QModelIndex>
#include <QTimer>
#include <QVariant>
#include <QVariantMap>

#include <utility>

#include <KLocalizedString>

#include "../protocol/collectionviewmodel.h"
#include "../protocol/protocolclient.h"

using whatevr::proto::CollectionViewModel;
using whatevr::proto::ProtocolClient;
using whatevr::proto::ProtocolError;
using whatevr::proto::Subscription;

namespace
{
constexpr int kStickerLimit = 200;
constexpr int kSearchDebounceMs = 180;
constexpr int kMaxInFlightDownloads = 6;

class StickerQueryListModel final : public QAbstractListModel
{
public:
    enum Role { ItemRole = Qt::UserRole + 1 };

    explicit StickerQueryListModel(QObject *parent)
        : QAbstractListModel(parent)
    {
    }

    [[nodiscard]] int rowCount(const QModelIndex &parent = QModelIndex()) const override
    {
        return parent.isValid() ? 0 : static_cast<int>(m_rows.size());
    }

    [[nodiscard]] QVariant data(const QModelIndex &index, int role) const override
    {
        if (!index.isValid() || index.row() < 0 || index.row() >= m_rows.size() || role != ItemRole) {
            return {};
        }
        return m_rows.at(index.row());
    }

    [[nodiscard]] QHash<int, QByteArray> roleNames() const override
    {
        return {{ItemRole, QByteArrayLiteral("item")}};
    }

    void setRows(const QJsonArray &rows)
    {
        QList<QVariantMap> next;
        next.reserve(static_cast<int>(rows.size()));
        for (const QJsonValue &value : rows) {
            if (value.isObject()) {
                next.append(value.toObject().toVariantMap());
            }
        }
        beginResetModel();
        m_rows = std::move(next);
        endResetModel();
    }

    void clear()
    {
        if (m_rows.isEmpty()) {
            return;
        }
        beginResetModel();
        m_rows.clear();
        endResetModel();
    }

    [[nodiscard]] QVariantMap itemById(const QString &id) const
    {
        for (const QVariantMap &row : m_rows) {
            if (row.value(QStringLiteral("id")).toString() == id) {
                return row;
            }
        }
        return {};
    }

private:
    QList<QVariantMap> m_rows;
};

QString responseError(const ProtocolError &error, const QString &fallback)
{
    return error.message.isEmpty() ? fallback : error.message;
}
} // namespace

ProtocolStickerController::ProtocolStickerController(ProtocolClient *client, QObject *parent)
    : QObject(parent)
    , m_client(client)
    , m_stickerViewModel(new CollectionViewModel(this))
    , m_searchModel(new StickerQueryListModel(this))
    , m_packModel(new CollectionViewModel(this))
    , m_favoriteModel(new CollectionViewModel(this))
    , m_searchTimer(new QTimer(this))
{
    m_searchTimer->setSingleShot(true);
    m_searchTimer->setInterval(kSearchDebounceMs);
    connect(m_searchTimer, &QTimer::timeout, this, &ProtocolStickerController::runSearch);

    connect(m_stickerViewModel, &CollectionViewModel::readyChanged, this, &ProtocolStickerController::viewChanged);
    connect(m_stickerViewModel, &CollectionViewModel::countChanged, this, &ProtocolStickerController::favoritesChanged);
    connect(m_stickerViewModel, &CollectionViewModel::dataChanged, this, &ProtocolStickerController::favoritesChanged);
    connect(m_stickerViewModel, &CollectionViewModel::modelReset, this, &ProtocolStickerController::favoritesChanged);

    connect(m_packModel, &CollectionViewModel::readyChanged, this, &ProtocolStickerController::viewChanged);
    connect(m_packModel, &CollectionViewModel::countChanged, this, &ProtocolStickerController::viewChanged);
    connect(m_packModel, &CollectionViewModel::dataChanged, this, &ProtocolStickerController::viewChanged);
    connect(m_packModel, &CollectionViewModel::modelReset, this, [this] {
        if (m_packSub) {
            m_packSubscriptionFailed = false;
        }
        Q_EMIT viewChanged();
    });

    connect(m_favoriteModel, &CollectionViewModel::countChanged, this, &ProtocolStickerController::favoritesChanged);
    connect(m_favoriteModel, &CollectionViewModel::dataChanged, this, &ProtocolStickerController::favoritesChanged);
    connect(m_favoriteModel, &CollectionViewModel::modelReset, this, &ProtocolStickerController::favoritesChanged);
}

ProtocolStickerController::~ProtocolStickerController()
{
    delete m_stickerSub.data();
    delete m_packSub.data();
    delete m_favoriteSub.data();
}

QAbstractItemModel *ProtocolStickerController::stickerModel() const
{
    return m_source == Source::Search ? m_searchModel : m_stickerViewModel;
}

QAbstractItemModel *ProtocolStickerController::packModel() const
{
    return m_packModel;
}

bool ProtocolStickerController::loading() const
{
    if (!m_activated) {
        return false;
    }
    if (m_source == Source::Search) {
        return m_searchLoading;
    }
    return m_stickerSub && !m_stickerViewModel->isReady() && !m_activeSubscriptionFailed;
}

bool ProtocolStickerController::downloading() const
{
    return m_downloading;
}

bool ProtocolStickerController::packsLoading() const
{
    const bool subscriptionLoading = m_packSub && !m_packModel->isReady() && !m_packSubscriptionFailed;
    return m_refreshInFlight || subscriptionLoading;
}

QString ProtocolStickerController::errorText() const
{
    return m_errorText;
}

QString ProtocolStickerController::activeSource() const
{
    switch (m_source) {
    case Source::Recents:
        return QStringLiteral("recents");
    case Source::Favorites:
        return QStringLiteral("favorites");
    case Source::All:
        return QStringLiteral("all");
    case Source::Pack:
        return QStringLiteral("pack");
    case Source::Search:
        return QStringLiteral("search");
    }
    return QStringLiteral("recents");
}

QString ProtocolStickerController::activePackId() const
{
    return m_activePackId;
}

QString ProtocolStickerController::activePackName() const
{
    return m_packModel->itemById(m_activePackId).value(QStringLiteral("name")).toString();
}

bool ProtocolStickerController::activePackInstalled() const
{
    return m_packModel->itemById(m_activePackId).value(QStringLiteral("installed")).toBool();
}

void ProtocolStickerController::setChatId(QString chatId)
{
    m_chatId = chatId.trimmed();
}

void ProtocolStickerController::activate()
{
    if (m_activated || !m_client) {
        return;
    }
    m_activated = true;
    subscribePacks();
    if (m_source == Source::Search) {
        m_searchLoading = !m_pendingSearchQuery.isEmpty();
        if (m_searchLoading) {
            m_searchTimer->start();
        }
    } else {
        subscribeActiveView();
    }
    Q_EMIT viewChanged();
}

void ProtocolStickerController::deactivate()
{
    if (!m_activated) {
        return;
    }
    m_activated = false;
    ++m_searchGeneration;
    m_searchTimer->stop();
    delete m_stickerSub.data();
    delete m_packSub.data();
    delete m_favoriteSub.data();
    m_stickerSub = nullptr;
    m_packSub = nullptr;
    m_favoriteSub = nullptr;
    m_stickerViewModel->onReset();
    m_packModel->onReset();
    m_favoriteModel->onReset();
    static_cast<StickerQueryListModel *>(m_searchModel)->clear();
    m_searchLoading = false;
    if (m_source == Source::Search) {
        m_source = Source::Recents;
        m_pendingSearchQuery.clear();
    }
    Q_EMIT viewChanged();
}

void ProtocolStickerController::showRecents()
{
    switchSource(Source::Recents);
}

void ProtocolStickerController::showFavorites()
{
    switchSource(Source::Favorites);
}

void ProtocolStickerController::showAll()
{
    switchSource(Source::All);
}

void ProtocolStickerController::showPack(const QString &packId)
{
    const QString id = packId.trimmed();
    if (!id.isEmpty()) {
        switchSource(Source::Pack, id);
    }
}

void ProtocolStickerController::search(const QString &query)
{
    const QString trimmed = query.trimmed();
    if (trimmed.isEmpty()) {
        showRecents();
        return;
    }

    ++m_searchGeneration;
    m_pendingSearchQuery = trimmed;
    m_searchLoading = m_activated;
    m_errorText.clear();
    if (m_source != Source::Search) {
        delete m_stickerSub.data();
        m_stickerSub = nullptr;
        m_stickerViewModel->onReset();
        m_source = Source::Search;
        m_activePackId.clear();
    }
    static_cast<StickerQueryListModel *>(m_searchModel)->clear();
    if (m_activated) {
        m_searchTimer->start();
    }
    Q_EMIT viewChanged();
}

void ProtocolStickerController::refreshStore()
{
    if (!m_client || m_refreshInFlight) {
        return;
    }
    m_refreshInFlight = true;
    m_errorText.clear();
    Q_EMIT viewChanged();

    const QPointer<ProtocolStickerController> self(this);
    m_client->request(
        QStringLiteral("sticker_packs.refresh"), {}, [self](const QJsonObject &, const ProtocolError &error) {
            if (!self) {
                return;
            }
            self->m_refreshInFlight = false;
            if (error.isError()) {
                self->m_errorText = responseError(error, i18nc("@info", "Unable to refresh sticker packs"));
            }
            Q_EMIT self->viewChanged();
        });
}

void ProtocolStickerController::setPackInstalled(const QString &packId, bool installed)
{
    const QString id = packId.trimmed();
    if (!m_client || id.isEmpty() || m_installInFlight.contains(id)) {
        return;
    }
    m_installInFlight.insert(id);

    const QPointer<ProtocolStickerController> self(this);
    m_client->request(QStringLiteral("sticker_pack.install"),
                      {{QStringLiteral("pack_id"), id}, {QStringLiteral("installed"), installed}},
                      [self, id](const QJsonObject &, const ProtocolError &error) {
                          if (!self) {
                              return;
                          }
                          self->m_installInFlight.remove(id);
                          if (error.isError()) {
                              self->m_errorText = responseError(error, i18nc("@info", "Unable to update sticker pack"));
                              Q_EMIT self->viewChanged();
                          }
                      });
}

void ProtocolStickerController::requestDownload(const QString &cacheKey)
{
    const QString key = cacheKey.trimmed();
    if (!m_client || key.isEmpty() || m_downloadQueued.contains(key) || m_downloadInFlight.contains(key)) {
        return;
    }
    m_downloadQueue.append(key);
    m_downloadQueued.insert(key);
    pumpDownloads();
}

void ProtocolStickerController::sendSticker(const QString &cacheKey, const QString &replyToMessageId)
{
    const QString key = cacheKey.trimmed();
    if (!m_client || m_chatId.isEmpty() || key.isEmpty() || m_sendInFlight.contains(key)) {
        return;
    }
    m_sendInFlight.insert(key);

    const QPointer<ProtocolStickerController> self(this);
    m_client->request(QStringLiteral("send.sticker"),
                      {{QStringLiteral("chat_id"), m_chatId},
                       {QStringLiteral("cache_key"), key},
                       {QStringLiteral("reply_to"), replyToMessageId.trimmed()}},
                      [self, key](const QJsonObject &, const ProtocolError &error) {
                          if (!self) {
                              return;
                          }
                          self->m_sendInFlight.remove(key);
                          if (error.isError()) {
                              Q_EMIT self->stickerSendFailed(
                                  key, responseError(error, i18nc("@info", "Unable to send sticker")));
                              return;
                          }
                          Q_EMIT self->stickerSent(key);
                      });
}

void ProtocolStickerController::setStickerFavorite(const QString &cacheKey, const QString &messageId, bool favorite)
{
    const QString key = cacheKey.trimmed();
    const QString message = messageId.trimmed();
    const QString requestKey = key.isEmpty() ? message : key;
    if (!m_client || requestKey.isEmpty() || m_favoriteInFlight.contains(requestKey)) {
        return;
    }
    m_favoriteInFlight.insert(requestKey);

    QJsonObject params{{QStringLiteral("favorite"), favorite}};
    if (!key.isEmpty()) {
        params.insert(QStringLiteral("cache_key"), key);
    }
    if (!message.isEmpty()) {
        params.insert(QStringLiteral("message_id"), message);
    }

    const QPointer<ProtocolStickerController> self(this);
    m_client->request(QStringLiteral("sticker.favorite"), params,
                      [self, requestKey, favorite](const QJsonObject &, const ProtocolError &error) {
                          if (!self) {
                              return;
                          }
                          self->m_favoriteInFlight.remove(requestKey);
                          if (error.isError()) {
                              Q_EMIT self->stickerFavoriteFailed(
                                  responseError(error, favorite ? i18nc("@info", "Unable to favorite the sticker")
                                                                : i18nc("@info", "Unable to unfavorite the sticker")));
                          }
                      });
}

bool ProtocolStickerController::isStickerFavorite(const QString &cacheKey)
{
    const QString key = cacheKey.trimmed();
    if (key.isEmpty()) {
        return false;
    }
    const bool transientSearch = m_source == Source::Search;
    const QVariantMap item = transientSearch ? static_cast<StickerQueryListModel *>(m_searchModel)->itemById(key)
                                             : m_stickerViewModel->itemById(key);
    // Live active rows are authoritative. Search rows are transient snapshots,
    // so prefer the dedicated favorite view once its initial fill has landed.
    if (!transientSearch && item.contains(QStringLiteral("is_favorite"))) {
        return item.value(QStringLiteral("is_favorite")).toBool();
    }
    if (m_favoriteModel->isReady()) {
        return m_favoriteModel->indexOfId(key) >= 0;
    }
    if (item.contains(QStringLiteral("is_favorite"))) {
        return item.value(QStringLiteral("is_favorite")).toBool();
    }
    return m_favoriteModel->indexOfId(key) >= 0;
}

void ProtocolStickerController::beginFavoriteTracking()
{
    ensureFavoriteSubscription();
}

void ProtocolStickerController::endFavoriteTracking()
{
    delete m_favoriteSub.data();
    m_favoriteSub = nullptr;
    m_favoriteModel->onReset();
    Q_EMIT favoritesChanged();
}

void ProtocolStickerController::switchSource(Source source, const QString &packId)
{
    ++m_searchGeneration;
    m_searchTimer->stop();
    m_pendingSearchQuery.clear();
    m_searchLoading = false;
    static_cast<StickerQueryListModel *>(m_searchModel)->clear();

    delete m_stickerSub.data();
    m_stickerSub = nullptr;
    m_stickerViewModel->onReset();
    m_source = source;
    m_activePackId = source == Source::Pack ? packId : QString();
    m_activeSubscriptionFailed = false;
    m_errorText.clear();
    if (m_activated) {
        subscribeActiveView();
    }
    Q_EMIT viewChanged();
}

void ProtocolStickerController::subscribeActiveView()
{
    if (!m_client || m_source == Source::Search) {
        return;
    }

    m_activeSubscriptionFailed = false;
    QJsonObject params;
    QString view;
    if (m_source == Source::Pack) {
        view = QStringLiteral("sticker_pack");
        params.insert(QStringLiteral("pack_id"), m_activePackId);
    } else {
        view = QStringLiteral("stickers");
        params.insert(QStringLiteral("limit"), kStickerLimit);
        switch (m_source) {
        case Source::Recents:
            params.insert(QStringLiteral("source"), QStringLiteral("recent"));
            break;
        case Source::Favorites:
            params.insert(QStringLiteral("source"), QStringLiteral("favorite"));
            break;
        case Source::All:
            params.insert(QStringLiteral("source"), QStringLiteral("all"));
            break;
        case Source::Pack:
        case Source::Search:
            break;
        }
    }

    const Source expectedSource = m_source;
    const QString expectedPack = m_activePackId;
    m_stickerSub = m_client->subscribe(view, params, m_stickerViewModel);
    connect(m_stickerSub, &Subscription::subscribed, this, [this, expectedSource, expectedPack] {
        if (m_source == expectedSource && m_activePackId == expectedPack) {
            m_activeSubscriptionFailed = false;
        }
    });
    connect(m_stickerSub, &Subscription::failed, this,
            [this, expectedSource, expectedPack](const QString &code, const QString &message) {
                if (m_source != expectedSource || m_activePackId != expectedPack) {
                    return;
                }
                if (code == QLatin1String("io") && m_stickerSub) {
                    return;
                }
                m_activeSubscriptionFailed = true;
                m_errorText = message.isEmpty() ? i18nc("@info", "Unable to load stickers") : message;
                Q_EMIT viewChanged();
            });
}

void ProtocolStickerController::subscribePacks()
{
    if (!m_client || m_packSub) {
        return;
    }
    m_packSubscriptionFailed = false;
    m_packSub = m_client->subscribe(QStringLiteral("sticker_packs"), {}, m_packModel);
    connect(m_packSub, &Subscription::subscribed, this, [this] { m_packSubscriptionFailed = false; });
    connect(m_packSub, &Subscription::failed, this, [this](const QString &code, const QString &message) {
        if (code == QLatin1String("io") && m_packSub) {
            return;
        }
        m_packSubscriptionFailed = true;
        if (m_errorText.isEmpty()) {
            m_errorText = message.isEmpty() ? i18nc("@info", "Unable to load sticker packs") : message;
        }
        Q_EMIT viewChanged();
    });
}

void ProtocolStickerController::ensureFavoriteSubscription()
{
    if (!m_client || m_favoriteSub) {
        return;
    }
    m_favoriteSub = m_client->subscribe(
        QStringLiteral("stickers"),
        {{QStringLiteral("source"), QStringLiteral("favorite")}},
        m_favoriteModel);
}

void ProtocolStickerController::runSearch()
{
    if (!m_client || m_source != Source::Search || m_pendingSearchQuery.isEmpty()) {
        return;
    }
    const quint64 generation = m_searchGeneration;
    const QString query = m_pendingSearchQuery;
    const QPointer<ProtocolStickerController> self(this);
    m_client->request(QStringLiteral("search.stickers"),
                      {{QStringLiteral("query"), query}, {QStringLiteral("limit"), kStickerLimit}},
                      [self, generation](const QJsonObject &result, const ProtocolError &error) {
                          if (!self || generation != self->m_searchGeneration || self->m_source != Source::Search) {
                              return;
                          }
                          self->m_searchLoading = false;
                          if (error.isError()) {
                              self->m_errorText = responseError(error, i18nc("@info", "Unable to search stickers"));
                          } else {
                              static_cast<StickerQueryListModel *>(self->m_searchModel)
                                  ->setRows(result.value(QStringLiteral("stickers")).toArray());
                          }
                          Q_EMIT self->viewChanged();
                      });
}

void ProtocolStickerController::pumpDownloads()
{
    while (m_client && m_downloadInFlight.size() < kMaxInFlightDownloads && !m_downloadQueue.isEmpty()) {
        const QString key = m_downloadQueue.takeFirst();
        m_downloadQueued.remove(key);
        m_downloadInFlight.insert(key);

        const QPointer<ProtocolStickerController> self(this);
        m_client->request(QStringLiteral("sticker.download"), {{QStringLiteral("cache_key"), key}},
                          [self, key](const QJsonObject &, const ProtocolError &error) {
                               if (!self) {
                                   return;
                               }
                               self->m_downloadInFlight.remove(key);
                               if (error.code == QLatin1String("io") && self->m_activated) {
                                   self->requestDownload(key);
                                   return;
                               }
                               // View-backed sources receive the new path as an
                               // upsert. Search is a transient snapshot, so
                               // refresh that query after a successful download.
                               if (!error.isError() && self->m_source == Source::Search
                                   && !self->m_pendingSearchQuery.isEmpty()) {
                                   self->runSearch();
                               } else if (error.isError()) {
                                   self->m_errorText = responseError(error, i18nc("@info", "Unable to download sticker"));
                                   Q_EMIT self->viewChanged();
                               }
                               self->pumpDownloads();
                           });
    }
    updateDownloading();
}

void ProtocolStickerController::updateDownloading()
{
    const bool downloading = !m_downloadQueue.isEmpty() || !m_downloadInFlight.isEmpty();
    if (m_downloading == downloading) {
        return;
    }
    m_downloading = downloading;
    Q_EMIT viewChanged();
}
