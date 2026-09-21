.pragma library
// Shared avatar-initials helpers. Two spellings grew across the QML tree:
// first+last (chat lists) and first-two-words (status/contact dialogs).
// Both live here so the rule has one home; call sites keep their variant.

// First + last word initials, e.g. "Ada Lovelace" -> "AL".
function firstLast(name) {
    const parts = (name || "").trim().split(/\s+/).filter(p => p.length > 0)
    if (parts.length === 0)
        return "?"
    let initials = parts[0].charAt(0)
    if (parts.length > 1)
        initials += parts[parts.length - 1].charAt(0)
    return initials.toUpperCase()
}

// First-two-words initials, e.g. "Ada Lovelace Byron" -> "AL".
function firstTwo(name) {
    const parts = String(name || "").trim().split(/\s+/)
    let initials = ""
    for (const part of parts) {
        if (part.length > 0) {
            initials += part.charAt(0).toUpperCase()
        }
        if (initials.length >= 2) {
            break
        }
    }
    return initials.length > 0 ? initials : "?"
}
