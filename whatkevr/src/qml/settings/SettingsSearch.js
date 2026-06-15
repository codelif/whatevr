.pragma library

// Scores how well `query` matches a settings row. Higher is better; -1 means no
// match. Mirrors the prefix-then-substring ranking used elsewhere in the app
// (EmojiModel/searchEmoji): exact/prefix label hits rank above substring hits,
// which rank above keyword hits, which rank above a loose subsequence match.
function score(query, label, keywords) {
    const q = (query || "").trim().toLowerCase()
    if (q.length === 0)
        return -1

    const l = (label || "").toLowerCase()
    if (l === q)
        return 1000
    if (l.startsWith(q))
        return 800
    const labelIdx = l.indexOf(q)
    if (labelIdx >= 0)
        return 600 - labelIdx

    const kws = keywords || []
    for (let i = 0; i < kws.length; ++i) {
        const k = kws[i].toLowerCase()
        if (k.startsWith(q))
            return 400
        if (k.indexOf(q) >= 0)
            return 300
    }

    // Loose subsequence: every character of the query appears in order in the
    // label. Catches "rmwd" -> "Remember window".
    if (isSubsequence(q, l))
        return 100

    return -1
}

function isSubsequence(needle, haystack) {
    let j = 0
    for (let i = 0; i < haystack.length && j < needle.length; ++i) {
        if (haystack[i] === needle[j])
            ++j
    }
    return j === needle.length
}

// Ranked matches for `query` over an index of
// { moduleId, moduleTitle, rowId, label, keywords } records.
function rank(query, index) {
    const out = []
    for (let i = 0; i < index.length; ++i) {
        const rec = index[i]
        const s = score(query, rec.label, rec.keywords)
        if (s >= 0)
            out.push({ rec: rec, s: s })
    }
    out.sort((a, b) => b.s - a.s)
    return out.map(e => e.rec)
}

// Depth-first search for a child Item carrying objectName === name, so a search
// result can scroll to / flash the exact delegate it points at.
function findRow(item, name) {
    if (!item || !name)
        return null
    if (item.objectName === name)
        return item

    const kids = item.children || []
    for (let i = 0; i < kids.length; ++i) {
        const found = findRow(kids[i], name)
        if (found)
            return found
    }

    // FormCard delegates live under contentItem/contentChildren for some types.
    const content = item.contentChildren || []
    for (let i = 0; i < content.length; ++i) {
        const found = findRow(content[i], name)
        if (found)
            return found
    }
    return null
}
