import QtQuick

// A texture-sampling rounded rectangle. Feed it an Image (or any texture
// provider) via `source` and it draws that texture clipped to per-corner radii
// in a single shader pass -- no layer.enabled, no maskSource, no FBO. This
// replaces the MultiEffect mask stack that used to thrash framebuffers as image
// delegates scrolled in and out of the viewport.
ShaderEffect {
    id: root

    // The Image whose texture is sampled. Keep it `visible: false`; it stays a
    // texture provider regardless of visibility.
    property Item source

    property real topLeftRadius: 0
    property real topRightRadius: 0
    property real bottomRightRadius: 0
    property real bottomLeftRadius: 0

    // Edge antialiasing band, in device pixels.
    property real aa: Math.max(1, Screen.devicePixelRatio)

    readonly property vector2d resolution: Qt.vector2d(Math.max(1, width), Math.max(1, height))
    readonly property vector4d radii: Qt.vector4d(topLeftRadius, topRightRadius,
                                                  bottomRightRadius, bottomLeftRadius)

    fragmentShader: "qrc:/qt/qml/Whatevr/shaders/roundedimage.frag.qsb"
}
