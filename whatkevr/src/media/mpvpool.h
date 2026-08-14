// SPDX-License-Identifier: BSD-3-Clause
#pragma once

#include <QList>
#include <QObject>

class MpvCore;
class MpvVideoItem;

/**
 * MpvPool keeps mpv cores warm and hands them round.
 *
 * An mpv core is several threads and tens of megabytes, so creating one per
 * bubble is out of the question and destroying one the moment a clip stops
 * means paying that cost again on the next tap. The pool keeps a small number
 * alive and reloads them with a new file instead.
 *
 * It no longer decides *what may play*: that is VideoPlaybackArbiter, which
 * sits above the engine seam because the rule has to hold on the Qt Multimedia
 * path too. With the arbiter capping concurrency at one exclusive clip plus the
 * user's GIF limit, the ceiling here is a safety net rather than a policy, and
 * the reservation and waiting-queue machinery this class used to carry has gone
 * with it.
 */
class MpvPool : public QObject
{
    Q_OBJECT

public:
    static MpvPool *instance();

    /// Hard ceiling on live cores: one exclusive clip plus the largest GIF
    /// limit the settings allow. The arbiter never lets more than this ask.
    static constexpr int coreLimit = 4;

    /// Claims a core for an item, reusing the one it already has, or returns
    /// nullptr in the case the ceiling above is somehow reached.
    MpvCore *acquire(MpvVideoItem *item);
    void release(MpvVideoItem *item, MpvCore *core);

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
    bool m_hardwareDecoding = true;
};
