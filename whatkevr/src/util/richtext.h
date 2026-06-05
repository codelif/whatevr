#pragma once

#include <QString>

namespace whatevr::util {

// Convert a Qt rich-text (QTextEdit/QTextDocument) HTML document to plain text.
// Returns the input unchanged when it is not a Qt rich-text document, so it is
// safe to call on arbitrary message bodies.
QString plainTextFromQtRichText(const QString &text);

}
