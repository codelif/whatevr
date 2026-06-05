#pragma once

#include <QStringView>

namespace whatevr::util {

[[nodiscard]] bool isKnownIanaTld(QStringView tld);

}
