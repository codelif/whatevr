#include "richtext.h"

#include <QTextDocument>

namespace whatevr::util {

QString plainTextFromQtRichText(const QString &text)
{
    const QString trimmed = text.trimmed();
    if (!trimmed.contains(QStringLiteral("qrichtext"), Qt::CaseInsensitive)
        || (!trimmed.startsWith(QStringLiteral("<!DOCTYPE HTML"), Qt::CaseInsensitive)
            && !trimmed.startsWith(QStringLiteral("<html"), Qt::CaseInsensitive))) {
        return text;
    }

    QTextDocument document;
    document.setHtml(trimmed);
    return document.toPlainText();
}

}
