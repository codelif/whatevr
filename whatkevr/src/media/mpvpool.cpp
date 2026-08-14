// SPDX-License-Identifier: BSD-3-Clause
#include "mpvpool.h"

#include "mpvcore.h"
#include "mpvvideoitem.h"

MpvPool *MpvPool::instance()
{
    static MpvPool pool;
    return &pool;
}

MpvPool::MpvPool(QObject *parent)
    : QObject(parent)
{
}

MpvCore *MpvPool::acquire(MpvVideoItem *item)
{
    if (!item) {
        return nullptr;
    }

    for (Slot &slot : m_slots) {
        if (slot.owner == item) {
            return slot.core;
        }
    }
    if (MpvCore *core = takeFreeCore()) {
        for (Slot &slot : m_slots) {
            if (slot.core == core) {
                slot.owner = item;
                break;
            }
        }
        return core;
    }
    return nullptr;
}

MpvCore *MpvPool::takeFreeCore()
{
    for (Slot &slot : m_slots) {
        if (!slot.owner) {
            return slot.core;
        }
    }
    if (m_slots.size() >= coreLimit) {
        return nullptr;
    }
    auto *core = new MpvCore(MpvCore::Mode::Video, this);
    if (!core->isValid()) {
        delete core;
        return nullptr;
    }
    core->setHardwareDecoding(m_hardwareDecoding);
    m_slots.append(Slot{core, nullptr});
    return core;
}

void MpvPool::release(MpvVideoItem *item, MpvCore *core)
{
    if (!core) {
        return;
    }
    for (Slot &slot : m_slots) {
        if (slot.core == core && slot.owner == item) {
            // Stopping rather than destroying is the point of the pool: the
            // next bubble reuses this decoder with a loadfile.
            core->stop();
            slot.owner = nullptr;
            return;
        }
    }
}

void MpvPool::setHardwareDecoding(bool enabled)
{
    if (m_hardwareDecoding == enabled) {
        return;
    }
    m_hardwareDecoding = enabled;
    for (Slot &slot : m_slots) {
        slot.core->setHardwareDecoding(enabled);
    }
}
