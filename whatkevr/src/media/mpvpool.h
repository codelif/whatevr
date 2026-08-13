// SPDX-License-Identifier: BSD-3-Clause
#pragma once

#include <QList>
#include <QObject>

class MpvCore;
class MpvVideoItem;

/**
 * MpvPool hands out a bounded number of video decoders.
 *
 * An mpv core is several threads and tens of megabytes, so a conversation full
 * of GIFs must not get one each. The pool caps how many exist, reuses them by
 * loading a new file rather than destroying and recreating, and when every core
 * is busy simply says no: the bubble that missed out keeps showing its
 * thumbnail, which is the same thing it shows while scrolling anyway.
 *
 * One core is held back for the full-screen viewer, so opening a video full
 * screen never has to evict an inline one.
 */
class MpvPool : public QObject
{
    Q_OBJECT

public:
    static MpvPool *instance();

    /// Hard ceiling on cores available to inline bubbles. The live limit is the
    /// user's setting, clamped to this.
    static constexpr int inlineCoreLimit = 3;

    /**
     * Claims a core for an item, or returns nullptr when none is free.
     * Reserved items (the full-screen viewer) draw from a dedicated slot.
     */
    MpvCore *acquire(MpvVideoItem *item);
    void release(MpvVideoItem *item, MpvCore *core);

    /// Marks an item as the priority consumer, so it always gets a core.
    void setReservedItem(MpvVideoItem *item);

    /// Applies the user's hardware-decoding preference to every core.
    void setHardwareDecoding(bool enabled);

private:
    explicit MpvPool(QObject *parent = nullptr);

    MpvCore *takeFreeCore();

    struct Slot {
        MpvCore *core = nullptr;
        MpvVideoItem *owner = nullptr;
    };

    QList<Slot> m_slots;
    MpvVideoItem *m_reservedItem = nullptr;
    MpvCore *m_reservedCore = nullptr;
    bool m_hardwareDecoding = true;
};

