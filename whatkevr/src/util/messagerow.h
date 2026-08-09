#pragma once

#include <QString>
#include <QVariantMap>

namespace whatevr::util
{

// One-line preview of a daemon `messages`-shaped row (the shape shared by the
// `messages`, `pinned` and `starred` views and by `search.messages` results).
// Known kinds get the localized placeholder the gRPC UI used; anything else
// falls back to the row's mandatory `fallback` string (PROTOCOL.md rule 5),
// which the daemon guarantees is human-readable for kinds this frontend does
// not implement.
[[nodiscard]] QString messageRowPreview(const QVariantMap &item);

} // namespace whatevr::util
