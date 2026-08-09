#include "messagerow.h"

#include <KLocalizedString>

namespace whatevr::util
{

QString messageRowPreview(const QVariantMap &item)
{
    if (item.value(QStringLiteral("revoked")).toBool()) {
        return i18nc("@item:intext message preview", "This message was deleted");
    }
    const QString text = item.value(QStringLiteral("text")).toString();
    if (!text.isEmpty()) {
        return text;
    }
    const QString kind = item.value(QStringLiteral("kind")).toString();
    if (kind == QLatin1String("image")) {
        return i18nc("@item:intext media placeholder", "Photo");
    }
    if (kind == QLatin1String("sticker")) {
        return i18nc("@item:intext media placeholder", "Sticker");
    }
    return item.value(QStringLiteral("fallback")).toString();
}

} // namespace whatevr::util
