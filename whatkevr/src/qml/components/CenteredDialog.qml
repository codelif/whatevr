import QtQuick
import org.kde.kirigami as Kirigami

// Kirigami.Dialog centres `y` on the popup's *clamped, reactive* `height` and
// adds an opacity-coupled slide offset (see Dialog.qml's `y` binding).
// QQuickPopup's keep-on-screen fitting can adjust `height` in response to the
// position, so `y → height → y` closes a binding loop that fires on every
// open-animation frame. Centre on the stable `implicitHeight` instead (and drop
// the slide term) so `y` never reads the reactive height back. Every in-app
// dialog derives from this to stay loop-free.
Kirigami.Dialog {
    y: parent ? Math.round((parent.height - implicitHeight) / 2) : 0
}
