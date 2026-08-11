import QtQuick

// A tiled, theme-aware doodle layer for the conversation background. Feed it a
// seamless single-colour-on-transparent SVG (`source`); it rasterises the motif
// once, repeats it with a Repeat wrap mode, and recolours it through
// `shaders/chatwallpaper.frag` using only the motif's alpha as a stencil.
//
// The tint defaults to "auto", deriving a near-black or near-white colour from
// `backgroundColor` so the motif stays faintly visible over any wallpaper colour
// or theme. `originX`/`originY` should be set to this layer's position in a
// container that does NOT move when banners/reply previews appear, so the tile
// grid stays pinned while the view slides over it. Keep it behind the message
// list (e.g. z: -1).
Item {
    id: root

    clip: true

    // Motif SVG url. Empty hides the layer entirely.
    property url source: ""
    // Whether to paint. Kept separate from `source` so a caller can hand over
    // the motif while the pane is still empty: rasterising a large SVG takes
    // long enough to be visible, and doing it during startup means the pattern
    // is ready the first time a chat opens instead of arriving seconds late.
    property bool active: true
    // Tile size as a percentage of the motif's natural display size (100 = base).
    property int scalePercent: 100
    // Overall motif opacity, 0..1. Kept low so it reads as a subtle texture.
    property real intensity: 0.10
    // Manual tint as a colour string ("" => auto-derive from backgroundColor).
    property string tint: ""
    // The conversation background colour the motif sits on; drives the auto tint.
    property color backgroundColor: "white"
    // Top-left of this layer in a stable coordinate space, used to pin the tile
    // grid so layout shifts don't drag the pattern. Usually the message view's
    // position within its container.
    property real originX: 0
    property real originY: 0

    readonly property bool hasSource: String(source).length > 0
    // The tile grid is only correct once the motif's own proportions are known:
    // until the SVG has rasterised, motifAspect falls back to 1 and every tile
    // would be drawn square, which visibly squashes any non-square motif. So the
    // pattern is held back rather than shown wrong and corrected a moment later.
    readonly property bool motifReady:
        motif.status === Image.Ready && motif.implicitWidth > 0 && motif.implicitHeight > 0
    visible: active && hasSource && intensity > 0

    // A ShaderEffectSource cannot capture an item that is not being rendered, so
    // a grab scheduled while this layer was hidden (during preload, or on another
    // page of the column view) yields an empty texture and — with live: false —
    // is never retried. Re-grabbing whenever the layer becomes visible makes that
    // self-healing instead of leaving the wallpaper blank until something else
    // happens to dirty the scene.
    onVisibleChanged: if (visible) scheduleTextureUpdate()

    // On-screen tile size (logical px) at 100%, scaled by scalePercent. Logical
    // pixels keep the motif a consistent physical size across display scales; the
    // texture below is rasterised at device resolution for crispness.
    readonly property real baseTile: 480
    readonly property real tilePx: Math.max(32, baseTile * scalePercent / 100)

    readonly property bool bgIsDark:
        (0.299 * backgroundColor.r + 0.587 * backgroundColor.g + 0.114 * backgroundColor.b) < 0.5
    readonly property color effectiveTint:
        root.tint.length > 0 ? root.tint : (bgIsDark ? Qt.rgba(1, 1, 1, 1) : Qt.rgba(0, 0, 0, 1))

    // The motif's height/width ratio, so tiles keep its aspect ratio instead of
    // being squished into squares. Derived from the rendered (implicit) size: we
    // only fix sourceSize.width, so Qt reports the aspect-correct height via
    // implicitHeight rather than back in sourceSize. Falls back to 1 until loaded.
    readonly property real motifAspect:
        (motif.implicitWidth > 0 && motif.implicitHeight > 0)
            ? motif.implicitHeight / motif.implicitWidth
            : 1

    // Keep slider feedback cheap: the shader's tileSize changes live, while SVG
    // re-rasterisation waits until movement settles and snaps to coarse texture
    // buckets. This avoids decoding a fresh SVG texture for every slider tick.
    readonly property int targetRasterWidth: bucketedRasterWidth(tilePx * Screen.devicePixelRatio)
    property int rasterWidth: targetRasterWidth

    function bucketedRasterWidth(rawWidth) {
        const wanted = Math.max(512, Math.ceil(rawWidth))
        const buckets = [512, 768, 1024, 1536, 2048, 3072, 4096]
        for (let i = 0; i < buckets.length; ++i) {
            if (wanted <= buckets[i]) {
                return buckets[i]
            }
        }
        return buckets[buckets.length - 1]
    }

    function scheduleTextureUpdate() {
        Qt.callLater(() => motifTexture.scheduleUpdate())
    }

    onTargetRasterWidthChanged: rasterDebounce.restart()

    Timer {
        id: rasterDebounce

        interval: 120
        repeat: false
        onTriggered: root.rasterWidth = root.targetRasterWidth
    }

    // Rasterise the motif at a crisp resolution, preserving its aspect ratio
    // (only the width is fixed; the height follows from the SVG's own ratio).
    Image {
        id: motif
        source: root.source
        sourceSize.width: root.rasterWidth
        smooth: true
        mipmap: true
        asynchronous: true
        cache: true
        onStatusChanged: if (status === Image.Ready) root.scheduleTextureUpdate()
        onImplicitWidthChanged: if (status === Image.Ready) root.scheduleTextureUpdate()
        onImplicitHeightChanged: if (status === Image.Ready) root.scheduleTextureUpdate()
    }

    // Repeat wrap lets the shader sample outside [0,1] to tile seamlessly.
    // hideSource keeps the motif out of the normal scene while still capturing
    // it (a visible: false source can yield an empty texture).
    ShaderEffectSource {
        id: motifTexture
        sourceItem: motif
        wrapMode: ShaderEffectSource.Repeat
        hideSource: true
        live: false
        smooth: true
    }

    ShaderEffect {
        anchors.fill: parent
        visible: root.visible && root.motifReady
        // Fade in on first rasterisation so the pattern arrives deliberately
        // rather than popping in at whatever moment the SVG finishes decoding.
        opacity: root.motifReady ? 1 : 0
        blending: true
        property variant source: motifTexture
        property color tint: root.effectiveTint
        property real intensity: root.intensity
        // Width set by the scale; height derived so each tile keeps the motif's
        // aspect ratio.
        property vector2d tileSize: Qt.vector2d(root.tilePx, root.tilePx * root.motifAspect)
        property vector2d resolution: Qt.vector2d(Math.max(1, width), Math.max(1, height))
        property vector2d origin: Qt.vector2d(root.originX, root.originY)
        fragmentShader: "qrc:/qt/qml/Whatevr/shaders/chatwallpaper.frag.qsb"

        Behavior on opacity {
            NumberAnimation {
                duration: 160
                easing.type: Easing.OutCubic
            }
        }
    }
}
