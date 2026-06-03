import QtQuick
import QtQml

Item {
    id: root

    property var target: null
    property real wheelStep: 80
    property real pixelDeltaScale: 0.72
    property real angleDeltaScale: 0.85
    property real maximumVelocity: 9000
    property real timeConstant: 0.48
    property real launchThreshold: 12
    property real stopThreshold: 6
    property real inertiaMultiplier: 1.18
    property real pendingDelta: 0
    property real velocity: 0
    property bool kineticActive: false
    property bool interactionActive: false
    property double lastWheelTimestamp: 0
    property double lastImpulseTimestamp: 0
    property double lastInputTimestamp: 0
    property double lastRealDeltaTimestamp: 0
    property real gestureTotalDistance: 0
    property bool gestureActive: false
    property bool gestureHeld: false
    property int holdCancelTimeout: 95
    property int gestureIdleTimeout: 180
    property int launchMaxAge: 75
    property real flickLaunchThreshold: 520
    property real minimumFlickDistance: 24
    property real velocitySampleThreshold: 1.5
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

    function blocksAtBound(delta) {
        if (!scrollable() || Math.abs(delta) < 0.01) {
            return false
        }

        const y = target.contentY
        return (delta < 0 && y <= minimumY() + 0.5)
                || (delta > 0 && y >= maximumY() - 0.5)
    }

    function sameDirection(a, b) {
        return a === 0 || b === 0 || (a > 0) === (b > 0)
    }

    function launchInertia(now) {
        if (gestureHeld || lastRealDeltaTimestamp <= 0 || now - lastRealDeltaTimestamp > launchMaxAge
                || gestureTotalDistance < minimumFlickDistance) {
            velocity = 0
            return false
        }

        if (Math.abs(velocity) < flickLaunchThreshold || !canMove(velocity)) {
            velocity = 0
            return false
        }

        velocity = clamp(velocity * inertiaMultiplier, -maximumVelocity, maximumVelocity)
        kineticActive = true
        gestureActive = false
        lastInputTimestamp = now - 100
        lastImpulseTimestamp = 0
        if (!frameDriver.running) {
            frameDriver.restart()
        }
        return true
    }

    function wheelDelta(pixelY, angleY) {
        if (Math.abs(pixelY) > 0) {
            return -pixelY * pixelDeltaScale
        }

        if (Math.abs(angleY) > 0) {
            return -(angleY / 120) * wheelStep * angleDeltaScale
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
        lastRealDeltaTimestamp = 0
        gestureTotalDistance = 0
        gestureActive = false
        gestureHeld = false
        if (frameDriver.running) {
            frameDriver.stop()
        }
        finishInteraction()
    }

    function cancelHeldGesture() {
        velocity = 0
        gestureHeld = true
        lastWheelTimestamp = 0
        lastImpulseTimestamp = 0
    }

    function finishIdleGesture() {
        gestureActive = false
        gestureHeld = false
        kineticActive = false
        velocity = 0
        pendingDelta = 0
        lastWheelTimestamp = 0
        lastImpulseTimestamp = 0
        lastInputTimestamp = 0
        lastRealDeltaTimestamp = 0
        gestureTotalDistance = 0
        finishInteraction()
        if (frameDriver.running) {
            frameDriver.stop()
        }
    }

    WheelHandler {
        target: null
        acceptedDevices: PointerDevice.Mouse | PointerDevice.TouchPad

        onWheel: event => {
            if (event.modifiers & Qt.ControlModifier) {
                return
            }

            const pixelY = event.pixelDelta.y
            const angleY = event.angleDelta.y
            const phase = event.phase
            const delta = root.wheelDelta(pixelY, angleY)
            const now = Date.now()
            if (Math.abs(delta) < 0.01) {
                if (phase === root.scrollBeginPhase || phase === root.scrollEndPhase) {
                    event.accepted = true
                    if (phase === root.scrollBeginPhase) {
                        root.velocity = 0
                        root.lastWheelTimestamp = 0
                        root.lastImpulseTimestamp = 0
                        root.lastInputTimestamp = now
                        root.lastRealDeltaTimestamp = 0
                        root.gestureTotalDistance = 0
                        root.gestureActive = true
                        root.gestureHeld = false
                        root.kineticActive = false
                        if (frameDriver.running) {
                            frameDriver.stop()
                        }
                    }
                    if (phase === root.scrollEndPhase && !root.launchInertia(now)) {
                        root.gestureActive = false
                        root.finishInteraction()
                    }
                }
                return
            }

            if (!root.canMove(delta)) {
                if (root.blocksAtBound(delta)) {
                    event.accepted = true
                    root.velocity = 0
                    root.lastRealDeltaTimestamp = 0
                    root.gestureTotalDistance = 0
                    root.gestureHeld = true
                }
                root.stopKinetic()
                return
            }

            event.accepted = true
            root.beginInteraction()

            if (!root.gestureActive) {
                root.gestureActive = true
                root.gestureHeld = false
                root.gestureTotalDistance = 0
                root.lastRealDeltaTimestamp = 0
                root.lastWheelTimestamp = 0
                root.velocity = 0
            } else if (root.lastRealDeltaTimestamp > 0 && now - root.lastRealDeltaTimestamp > root.holdCancelTimeout) {
                root.cancelHeldGesture()
            }

            if (root.gestureHeld && Math.abs(delta) >= root.minimumFlickDistance) {
                root.gestureHeld = false
                root.gestureTotalDistance = 0
                root.velocity = 0
                root.lastWheelTimestamp = 0
            }

            const hasPixelDelta = Math.abs(pixelY) > 0
            let dt = root.lastWheelTimestamp > 0
                    ? (now - root.lastWheelTimestamp) / 1000
                    : (hasPixelDelta ? 1 / 60 : 0.12)
            dt = root.clamp(dt, hasPixelDelta ? 0.004 : 0.025, 0.12)
            root.lastWheelTimestamp = now

            const instantVelocity = delta / dt
            root.gestureTotalDistance += Math.abs(delta)
            if (!root.gestureHeld && Math.abs(delta) >= root.velocitySampleThreshold) {
                const inputWeight = hasPixelDelta ? 0.58 : 0.44
                const reversesHard = !root.sameDirection(root.velocity, instantVelocity)
                        && Math.abs(instantVelocity) > Math.max(180, Math.abs(root.velocity) * 0.45)
                const baseVelocity = root.sameDirection(root.velocity, instantVelocity) || !reversesHard
                        ? root.velocity
                        : 0
                root.velocity = root.clamp(baseVelocity * (1 - inputWeight) + instantVelocity * inputWeight,
                                           -root.maximumVelocity, root.maximumVelocity)
            }

            const actual = root.applyDelta(delta)
            if (Math.abs(actual) < Math.abs(delta) * 0.5) {
                root.velocity *= 0.35
            }
            root.kineticActive = true
            root.lastInputTimestamp = now
            root.lastRealDeltaTimestamp = now
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
            if (root.gestureActive) {
                if (root.lastRealDeltaTimestamp > 0 && now - root.lastRealDeltaTimestamp > root.holdCancelTimeout) {
                    root.cancelHeldGesture()
                }
                const idleTimestamp = Math.max(root.lastInputTimestamp, root.lastRealDeltaTimestamp)
                if (idleTimestamp > 0 && now - idleTimestamp > root.gestureIdleTimeout) {
                    root.finishIdleGesture()
                }
                return
            }

            if (!root.kineticActive) {
                if (Math.abs(root.pendingDelta) < 0.01) {
                    frameDriver.stop()
                }
                return
            }

            const dt = root.clamp(frameDriver.frameTime, 1 / 144, 1 / 30)
            const decay = Math.exp(-dt / root.timeConstant)
            if (!inputQuiet) {
                root.velocity *= decay
                return
            }

            const delta = root.velocity * root.timeConstant * (1 - decay)
            const actual = Math.abs(delta) >= 0.01 ? root.applyDelta(delta) : 0
            root.velocity *= decay

            if (Math.abs(root.velocity) < root.stopThreshold || (Math.abs(delta) >= 0.01 && Math.abs(actual) < 0.01)) {
                root.stopKinetic()
            }
        }
    }
}
