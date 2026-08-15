import QtQuick
import QtQml

Item {
    id: root

    property var target: null
    property real wheelStep: 80
    // 1:1 finger tracking for touchpad pixel deltas and full notch steps for
    // wheels (the wheel path is smoothed per-frame instead of dampened).
    property real pixelDeltaScale: 1.0
    property real angleDeltaScale: 1.0
    property real maximumVelocity: 9000
    property real timeConstant: 0.48
    // Glide length scales with launch velocity (Firefox feel): gentle flicks
    // settle quickly, hard flicks coast visibly longer. activeTimeConstant is
    // picked per launch and drives the deceleration integration.
    property real minTimeConstant: 0.6
    property real maxTimeConstant: 1.6
    property real activeTimeConstant: timeConstant
    property real launchThreshold: 12
    property real stopThreshold: 6
    property real inertiaMultiplier: 1.15
    // When false (older history can still load into a BottomToTop ListView),
    // target.originY is only an estimate that the view revises as delegates
    // materialise; allow scrolling past it by one viewport instead of
    // hard-stopping at a stale edge that is about to move.
    property bool clampAtOrigin: true
    property real pendingDelta: 0
    // When the accumulator last took a notch, so an undeliverable one is given
    // a short grace before being abandoned rather than dropped on the spot.
    property double pendingDeltaSince: 0
    // How long a notch may wait for the view's geometry to catch up. Long
    // enough to cover a layout pass, short enough that a genuinely unscrollable
    // view does not hold a stale scroll and apply it much later.
    property int pendingDeltaGrace: 150
    property real velocity: 0
    property bool kineticActive: false
    // contentY as we last set it; used to detect the ListView repositioning the
    // viewport underneath an active fling (origin/position fixups).
    property real lastAppliedContentY: 0
    property bool hasAppliedContentY: false
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
    property int launchMaxAge: 90
    property real flickLaunchThreshold: 120
    property real minimumFlickDistance: 10
    property real velocitySampleThreshold: 1.5
    // Phase-less scroll streams (X11/synaptics, some libinput stacks) never
    // deliver a ScrollEnd, so the only lift-off signal is the deltas stopping.
    // After this much quiet, a smooth-delta gesture is treated as ended and
    // inertia may launch. Must stay below holdCancelTimeout (which zeroes
    // velocity) and at most launchMaxAge (launchInertia's own age check).
    property int phantomEndTimeout: 65
    // True once any event carried a scroll phase: the platform will tell us
    // when gestures end, so phantom-end detection stays disabled. Sticky for
    // the component lifetime to avoid misclassifying a gesture's first event.
    property bool sawPhaseEvents: false
    // True while the current gesture produced smooth deltas (pixel deltas, or
    // fractional angle deltas as X11 touchpads send); discrete mouse-wheel
    // notches never set it, so they can never phantom-launch inertia.
    property bool gestureHasSmoothDeltas: false
    // Smoothing window for discrete wheel notches: each frame applies this
    // fraction of the remaining accumulated step (exponential ease-out).
    property real wheelSmoothTime: 0.09
    // Sliding-window velocity tracker for smooth gestures (browser-style).
    // The per-event EMA (`velocity`) stays the *live* value — fastFlicking
    // detection, bound damping, reversal tests — but flings launch from the
    // window estimate: displacement over the last ~120 ms. The EMA weights
    // recent events, so the small trailing deltas fingers emit while
    // decelerating just before lift-off crush it far below the true flick
    // speed; a windowed sum is immune to that.
    property int flickWindowMs: 120
    property int flickWindowMinSpanMs: 30
    property int flickWindowMinSamples: 2
    // [{t, d}] pruned to flickWindowMs; internal only — never bind to it.
    property var flickSamples: []
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
        if (!target) {
            return 0
        }
        return clampAtOrigin ? target.originY : target.originY - target.height
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

    function pruneFlickSamples(now) {
        const cutoff = now - flickWindowMs
        while (flickSamples.length > 0 && flickSamples[0].t < cutoff) {
            flickSamples.shift()
        }
    }

    function pushFlickSample(now, delta) {
        pruneFlickSamples(now)
        flickSamples.push({ t: now, d: delta })
    }

    function clearFlickSamples() {
        if (flickSamples.length > 0) {
            flickSamples.length = 0
        }
    }

    function windowVelocity(now) {
        pruneFlickSamples(now)
        if (flickSamples.length < flickWindowMinSamples) {
            return 0
        }
        // Exclude the oldest sample's delta: its accumulation interval
        // predates the measured span (standard velocity-tracker bias fix).
        // The span floor keeps two near-simultaneous samples from exploding
        // the estimate.
        let sum = 0
        for (let i = 1; i < flickSamples.length; ++i) {
            sum += flickSamples[i].d
        }
        const span = Math.max(flickWindowMinSpanMs,
                              flickSamples[flickSamples.length - 1].t - flickSamples[0].t)
        return clamp(sum / (span / 1000), -maximumVelocity, maximumVelocity)
    }

    function launchInertia(now) {
        if (gestureHeld || lastRealDeltaTimestamp <= 0 || now - lastRealDeltaTimestamp > launchMaxAge
                || gestureTotalDistance < minimumFlickDistance) {
            velocity = 0
            return false
        }

        // Launch from the window velocity when available; fall back to the
        // EMA for gestures too short to fill the window.
        const wv = windowVelocity(now)
        if (wv !== 0) {
            velocity = wv
        }

        if (Math.abs(velocity) < flickLaunchThreshold || !canMove(velocity)) {
            velocity = 0
            return false
        }

        velocity = clamp(velocity * inertiaMultiplier, -maximumVelocity, maximumVelocity)
        activeTimeConstant = clamp(minTimeConstant
                                   + Math.abs(velocity) / 9000 * (maxTimeConstant - minTimeConstant),
                                   minTimeConstant, maxTimeConstant)
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
        lastAppliedContentY = nextY
        hasAppliedContentY = true
        const actual = nextY - oldY
        scrolled(actual)
        return actual
    }

    function stopKinetic() {
        kineticActive = false
        velocity = 0
        pendingDelta = 0
        pendingDeltaSince = 0
        hasAppliedContentY = false
        lastWheelTimestamp = 0
        lastImpulseTimestamp = 0
        lastInputTimestamp = 0
        lastRealDeltaTimestamp = 0
        gestureTotalDistance = 0
        gestureActive = false
        gestureHeld = false
        gestureHasSmoothDeltas = false
        activeTimeConstant = timeConstant
        clearFlickSamples()
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
        clearFlickSamples()
    }

    function finishIdleGesture() {
        gestureActive = false
        gestureHeld = false
        gestureHasSmoothDeltas = false
        kineticActive = false
        velocity = 0
        pendingDelta = 0
        pendingDeltaSince = 0
        lastWheelTimestamp = 0
        lastImpulseTimestamp = 0
        lastInputTimestamp = 0
        lastRealDeltaTimestamp = 0
        gestureTotalDistance = 0
        activeTimeConstant = timeConstant
        clearFlickSamples()
        finishInteraction()
        if (frameDriver.running) {
            frameDriver.stop()
        }
    }

    // The two devices are routed by what they *are*, not by what their deltas
    // happen to look like. A high-resolution mouse wheel emits pixel deltas on
    // Wayland, so the delta-shape test alone put a mouse on the touchpad path:
    // its phantom-end detection launched an inertia fling 65 ms after every
    // notch, which the next notch then had to fight. That is the "resists once
    // or twice" the user sees. The shape test survives as the tie-breaker
    // within the non-touchpad path, for devices that report as a mouse but
    // stream like a trackpad.
    WheelHandler {
        target: null
        acceptedDevices: PointerDevice.Mouse

        onWheel: event => root.handleWheel(event, true)
    }

    WheelHandler {
        target: null
        acceptedDevices: PointerDevice.TouchPad

        onWheel: event => root.handleWheel(event, false)
    }

    function handleWheel(event, fromMouse) {
        if (event.modifiers & Qt.ControlModifier) {
            return
        }

        const pixelY = event.pixelDelta.y
        const angleY = event.angleDelta.y
        const phase = event.phase
        const delta = root.wheelDelta(pixelY, angleY)
        const now = Date.now()
        if (phase !== 0) {
            root.sawPhaseEvents = true
        }
        const hasPixelDelta = Math.abs(pixelY) > 0
        // Discrete wheel notches: angle-only, phase-less, whole multiples
        // of 120. Everything else (pixel deltas, phased streams, fractional
        // angle deltas from X11 touchpads) is a smooth gesture and keeps
        // exact 1:1 tracking plus flick inertia. A smooth gesture stays
        // smooth even if one of its deltas happens to land on a multiple
        // of 120. A wheel that arrives with a phase is a genuine gesture
        // stream whatever the device claims to be, so it keeps the smooth
        // path even on a mouse.
        const discreteWheel = (fromMouse
                               ? phase === 0
                               : (!hasPixelDelta && phase === 0 && Math.abs(angleY) % 120 === 0))
                && !(root.gestureActive && root.gestureHasSmoothDeltas)
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
                    root.gestureHasSmoothDeltas = false
                    root.kineticActive = false
                    root.activeTimeConstant = root.timeConstant
                    root.pendingDelta = 0
                    root.clearFlickSamples()
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

        if (discreteWheel) {
            // Wheel notches are large discrete jumps; instead of applying
            // them instantly (or feeding them through the touchpad EMA),
            // accumulate and let the frame driver ease the remainder out
            // exponentially — Firefox-style smooth wheel scrolling. Rapid
            // notches stack into one continuous glide.
            //
            // Deliberately *not* gated on canMove(): the geometry it asks
            // about is an estimate that trails reality by an event-loop turn
            // (the list's height is a delayed Binding, and a variable-height
            // ListView revises contentHeight as delegates materialise).
            // Discarding a notch on a stale answer is how the wheel came to
            // swallow one every so often. The accumulator is re-checked against
            // live geometry every frame instead, so a notch that cannot move
            // yet arrives a frame late rather than never; frameDriver drops it
            // if the view really is stuck.
            if (root.blocksAtBound(delta)) {
                event.accepted = true
                root.pendingDelta = 0
                return
            }
            event.accepted = true
            root.pendingDeltaSince = now
            root.beginInteraction()
            // A notch against an active fling's direction stops the fling;
            // same-direction notches add on top of it.
            if (root.kineticActive && !root.sameDirection(root.velocity, delta)) {
                root.kineticActive = false
                root.velocity = 0
            }
            if (!root.sameDirection(root.pendingDelta, delta)) {
                root.pendingDelta = 0
            }
            root.pendingDelta = root.clamp(root.pendingDelta + delta,
                                           -root.target.height, root.target.height)
            if (!frameDriver.running) {
                frameDriver.restart()
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
            root.gestureHasSmoothDeltas = false
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
            root.clearFlickSamples()
        }

        root.gestureHasSmoothDeltas = true
        // Smooth streams (pixel deltas, or fractional angle deltas on X11)
        // arrive at device/frame rate; a tight dt floor keeps the velocity
        // estimate honest. Discrete notches never reach this path.
        let dt = root.lastWheelTimestamp > 0
                ? (now - root.lastWheelTimestamp) / 1000
                : 1 / 60
        dt = root.clamp(dt, hasPixelDelta ? 0.004 : 0.008, 0.12)
        root.lastWheelTimestamp = now

        const instantVelocity = delta / dt
        root.gestureTotalDistance += Math.abs(delta)
        if (!root.gestureHeld && Math.abs(delta) >= root.velocitySampleThreshold) {
            // High weight so the EMA tracks the gesture's peak speed
            // closely; a flick then launches at roughly its true velocity.
            const inputWeight = hasPixelDelta ? 0.7 : 0.5
            const reversesHard = !root.sameDirection(root.velocity, instantVelocity)
                    && Math.abs(instantVelocity) > Math.max(180, Math.abs(root.velocity) * 0.45)
            if (reversesHard) {
                // A hard direction reversal invalidates the window too.
                root.clearFlickSamples()
            }
            const baseVelocity = root.sameDirection(root.velocity, instantVelocity) || !reversesHard
                    ? root.velocity
                    : 0
            root.velocity = root.clamp(baseVelocity * (1 - inputWeight) + instantVelocity * inputWeight,
                                       -root.maximumVelocity, root.maximumVelocity)
        }
        if (!root.gestureHeld) {
            // No magnitude gate: trailing micro-deltas must be in the
            // window so the measured span stays honest — they cannot
            // dominate a 120 ms displacement sum.
            root.pushFlickSample(now, delta)
        }

        const actual = root.applyDelta(delta)
        if (Math.abs(actual) < Math.abs(delta) * 0.5) {
            root.velocity *= 0.35
            root.clearFlickSamples()
        }
        root.kineticActive = true
        root.lastInputTimestamp = now
        root.lastRealDeltaTimestamp = now
        root.lastImpulseTimestamp = now

        if (!frameDriver.running) {
            frameDriver.restart()
        }
    }

    FrameAnimation {
        id: frameDriver

        onTriggered: {
            const now = Date.now()
            if (!root.scrollable()) {
                // Not necessarily true: the list's height is a delayed Binding
                // and its contentHeight an estimate delegates keep revising, so
                // "not scrollable" is regularly a stale answer that a layout
                // pass is about to correct. Hold an accumulated notch across
                // that rather than throwing it away, and give up only once the
                // view has stayed unscrollable for the whole grace.
                if (Math.abs(root.pendingDelta) >= 0.01
                        && now - root.pendingDeltaSince <= root.pendingDeltaGrace) {
                    return
                }
                root.stopKinetic()
                return
            }

            const inputQuiet = now - root.lastInputTimestamp > 42

            // Ease out the wheel-notch accumulator: a fixed fraction of the
            // remainder per frame (exponential ease-out), so single notches
            // glide briefly and rapid notches merge into one smooth run.
            if (Math.abs(root.pendingDelta) >= 0.5) {
                const wheelDt = root.clamp(frameDriver.frameTime, 1 / 144, 1 / 30)
                const ease = 1 - Math.exp(-wheelDt / root.wheelSmoothTime)
                const step = root.pendingDelta * ease
                const applied = root.applyDelta(step)
                root.pendingDelta -= step
                if (Math.abs(step) >= 0.01 && Math.abs(applied) < 0.01) {
                    // Nothing moved. At a bound that is real, drop the rest
                    // instead of grinding at it. At one that is only an
                    // estimate (the top of a list still loading older history,
                    // where originY is revised as rows materialise), hold it
                    // for the grace and try again as the geometry settles.
                    if (root.blocksAtBound(step)
                            || now - root.pendingDeltaSince > root.pendingDeltaGrace) {
                        root.pendingDelta = 0
                    }
                }
            } else if (root.pendingDelta !== 0) {
                root.applyDelta(root.pendingDelta)
                root.pendingDelta = 0
            }

            if (root.gestureActive) {
                // Phase-less streams never deliver a ScrollEnd: when smooth
                // deltas go quiet while the tracked velocity is still above
                // the launch threshold, that *was* the lift-off — coast.
                if (!root.sawPhaseEvents && root.gestureHasSmoothDeltas
                        && root.lastRealDeltaTimestamp > 0
                        && now - root.lastRealDeltaTimestamp >= root.phantomEndTimeout
                        && Math.max(Math.abs(root.velocity), Math.abs(root.windowVelocity(now)))
                                >= root.flickLaunchThreshold
                        && root.launchInertia(now)) {
                    return
                }
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
                    root.finishInteraction()
                }
                return
            }

            // The ListView moved the viewport underneath us (origin/position
            // fixup after delegates resized or materialised). Integrating the
            // fling against the new position would look like a jump-then-drag;
            // stop and let the user resume from where the view settled.
            if (root.hasAppliedContentY
                    && Math.abs(root.target.contentY - root.lastAppliedContentY) > root.target.height / 2) {
                root.stopKinetic()
                return
            }

            const dt = root.clamp(frameDriver.frameTime, 1 / 144, 1 / 30)
            const decay = Math.exp(-dt / root.activeTimeConstant)
            if (!inputQuiet) {
                root.velocity *= decay
                return
            }

            const delta = root.velocity * root.activeTimeConstant * (1 - decay)
            const actual = Math.abs(delta) >= 0.01 ? root.applyDelta(delta) : 0
            root.velocity *= decay

            if (Math.abs(root.velocity) < root.stopThreshold || (Math.abs(delta) >= 0.01 && Math.abs(actual) < 0.01)) {
                root.stopKinetic()
            }
        }
    }
}
