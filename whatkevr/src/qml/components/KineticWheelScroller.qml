import QtQuick
import QtQml
import QtQuick.Window

Item {
    id: root

    property var target: null
    property real wheelStep: 80
    property real maximumVelocity: 12000
    property real timeConstant: 0.58
    property real launchThreshold: 12
    property real stopThreshold: 6
    property real inertiaMultiplier: 1.55
    property real pendingDelta: 0
    property real velocity: 0
    property bool kineticActive: false
    property bool interactionActive: false
    property double lastWheelTimestamp: 0
    property double lastImpulseTimestamp: 0
    property double lastInputTimestamp: 0
    property real recentPeakVelocity: 0
    readonly property int scrollBeginPhase: 1
    readonly property int scrollUpdatePhase: 2
    readonly property int scrollEndPhase: 3

    signal scrollStarted()
    signal scrollEnded()
    signal scrolled(real delta)

    z: 1000
    enabled: target !== null

    function clamp(value, minimum, maximum) {
        return Math.max(minimum, Math.min(maximum, value))
    }

    function minimumY() {
        return target ? target.originY : 0
    }

    function maximumY() {
        if (!target) {
            return 0
        }
        return Math.max(minimumY(), target.originY + Math.max(0, target.contentHeight - target.height))
    }

    function scrollable() {
        return target && target.visible && target.enabled && target.interactive
                && target.contentHeight > target.height + 0.5
    }

    function canMove(delta) {
        if (!scrollable() || Math.abs(delta) < 0.01) {
            return false
        }

        const y = target.contentY
        if (delta < 0 && y <= minimumY() + 0.5) {
            return false
        }
        if (delta > 0 && y >= maximumY() - 0.5) {
            return false
        }
        return true
    }

    function sameDirection(a, b) {
        return a === 0 || b === 0 || (a > 0) === (b > 0)
    }

    function launchInertia(now) {
        const launchVelocity = Math.abs(recentPeakVelocity) > Math.abs(velocity)
                ? recentPeakVelocity
                : velocity
        if (Math.abs(launchVelocity) < launchThreshold || !canMove(launchVelocity)) {
            return false
        }

        velocity = clamp(launchVelocity * inertiaMultiplier, -maximumVelocity, maximumVelocity)
        recentPeakVelocity = velocity
        kineticActive = true
        lastInputTimestamp = now - 100
        lastImpulseTimestamp = 0
        if (!frameDriver.running) {
            frameDriver.restart()
        }
        return true
    }

    function containsGlobalPoint(globalX, globalY) {
        if (!visible || !enabled || !root.Window.window) {
            return false
        }

        const local = mapFromGlobal(Qt.point(globalX, globalY))
        return local.x >= 0 && local.y >= 0 && local.x <= width && local.y <= height
    }

    function wheelDelta(pixelY, angleY) {
        if (Math.abs(pixelY) > 0) {
            return -pixelY
        }

        if (Math.abs(angleY) > 0) {
            return -(angleY / 120) * wheelStep
        }

        return 0
    }

    function beginInteraction() {
        if (interactionActive) {
            return
        }
        interactionActive = true
        scrollStarted()
    }

    function finishInteraction() {
        if (!interactionActive) {
            return
        }
        interactionActive = false
        scrollEnded()
    }

    function applyDelta(delta) {
        if (!scrollable() || Math.abs(delta) < 0.01) {
            return 0
        }

        const oldY = target.contentY
        const nextY = clamp(oldY + delta, minimumY(), maximumY())
        if (Math.abs(nextY - oldY) < 0.01) {
            return 0
        }

        target.contentY = nextY
        const actual = nextY - oldY
        scrolled(actual)
        return actual
    }

    function stopKinetic() {
        kineticActive = false
        velocity = 0
        pendingDelta = 0
        lastWheelTimestamp = 0
        lastImpulseTimestamp = 0
        lastInputTimestamp = 0
        recentPeakVelocity = 0
        if (frameDriver.running) {
            frameDriver.stop()
        }
        finishInteraction()
    }

    Connections {
        target: WheelInputRouter

        function onWheel(globalX, globalY, pixelY, angleY, modifiers, phase) {
            if (WheelInputRouter.accepted || !root.containsGlobalPoint(globalX, globalY)) {
                return
            }

            if (modifiers & Qt.ControlModifier) {
                return
            }

            const delta = root.wheelDelta(pixelY, angleY)
            const now = Date.now()
            if (Math.abs(delta) < 0.01) {
                if (phase === root.scrollBeginPhase || phase === root.scrollEndPhase) {
                    WheelInputRouter.acceptWheel()
                    if (phase === root.scrollEndPhase && !root.launchInertia(now)) {
                        root.finishInteraction()
                    }
                }
                return
            }

            if (!root.canMove(delta)) {
                root.stopKinetic()
                return
            }

            WheelInputRouter.acceptWheel()
            root.beginInteraction()

            const hasPixelDelta = Math.abs(pixelY) > 0
            let dt = root.lastWheelTimestamp > 0
                    ? (now - root.lastWheelTimestamp) / 1000
                    : (hasPixelDelta ? 1 / 60 : 0.12)
            dt = root.clamp(dt, hasPixelDelta ? 0.004 : 0.025, 0.12)
            root.lastWheelTimestamp = now

            const instantVelocity = delta / dt
            const inputWeight = hasPixelDelta ? 0.70 : 0.52
            const reversesHard = !root.sameDirection(root.velocity, instantVelocity)
                    && Math.abs(instantVelocity) > Math.max(180, Math.abs(root.velocity) * 0.45)
            const baseVelocity = root.sameDirection(root.velocity, instantVelocity) || !reversesHard
                    ? root.velocity
                    : 0
            root.velocity = root.clamp(baseVelocity * (1 - inputWeight) + instantVelocity * inputWeight,
                                       -root.maximumVelocity, root.maximumVelocity)
            if (Math.abs(root.velocity) >= root.launchThreshold) {
                if (root.sameDirection(root.recentPeakVelocity, root.velocity)) {
                    if (Math.abs(root.velocity) > Math.abs(root.recentPeakVelocity)) {
                        root.recentPeakVelocity = root.velocity
                    }
                } else if (reversesHard || Math.abs(root.velocity) > Math.abs(root.recentPeakVelocity) * 0.6) {
                    root.recentPeakVelocity = root.velocity
                }
            }

            const actual = root.applyDelta(delta)
            if (Math.abs(actual) < Math.abs(delta) * 0.5) {
                root.velocity *= 0.35
            }
            root.kineticActive = true
            root.lastInputTimestamp = now
            root.lastImpulseTimestamp = now

            if (!frameDriver.running) {
                frameDriver.restart()
            }
        }
    }

    FrameAnimation {
        id: frameDriver

        onTriggered: {
            if (!root.scrollable()) {
                root.stopKinetic()
                return
            }

            const now = Date.now()
            const inputQuiet = now - root.lastInputTimestamp > 42
            const peakFresh = now - root.lastImpulseTimestamp < 180
            if (inputQuiet && peakFresh
                    && root.sameDirection(root.velocity, root.recentPeakVelocity)
                    && Math.abs(root.recentPeakVelocity) > Math.abs(root.velocity)) {
                root.velocity = root.clamp(root.recentPeakVelocity * root.inertiaMultiplier,
                                           -root.maximumVelocity, root.maximumVelocity)
                root.recentPeakVelocity = root.velocity
                root.lastImpulseTimestamp = 0
            }

            if (!root.kineticActive) {
                if (Math.abs(root.pendingDelta) < 0.01) {
                    frameDriver.stop()
                }
                return
            }

            const dt = root.clamp(frameDriver.frameTime, 1 / 144, 1 / 30)
            const decay = Math.exp(-dt / root.timeConstant)
            const kineticScale = inputQuiet ? 1.0 : 0.08
            const delta = root.velocity * root.timeConstant * (1 - decay) * kineticScale
            const actual = Math.abs(delta) >= 0.01 ? root.applyDelta(delta) : 0
            root.velocity *= decay

            if (!inputQuiet) {
                return
            }

            if (Math.abs(root.velocity) < root.stopThreshold || (Math.abs(delta) >= 0.01 && Math.abs(actual) < 0.01)) {
                root.stopKinetic()
            }
        }
    }
}
