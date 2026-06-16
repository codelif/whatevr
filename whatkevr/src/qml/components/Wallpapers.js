.pragma library

// Conversation wallpaper presets, shared by the settings preview selector and
// the conversation background. value "" is the plain theme background.
function presets() {
    return [
        { value: "", label: "Default" },
        { value: "gray", label: "Gray", bg: "#e9ecef" },
        { value: "warm", label: "Warm", bg: "#f3ece2" },
        { value: "mint", label: "Mint", bg: "#e4f1ec" },
        { value: "sky", label: "Sky", bg: "#e4ecf6" },
        { value: "graphite", label: "Graphite", bg: "#2b2f33" }
    ];
}

// Background color for a wallpaper id, or "" when it should fall back to the
// theme background.
function colorFor(id) {
    var list = presets();
    for (var i = 0; i < list.length; i++) {
        if (list[i].value === id) {
            return list[i].bg || "";
        }
    }
    return "";
}
