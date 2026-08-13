// SPDX-License-Identifier: BSD-3-Clause
#include "mpvpool.h"

#include <algorithm>

#include "app/settings.h"
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

    // The full-screen viewer gets its own core so opening it never steals one
    // from the conversation behind it.
    if (item == m_reservedItem) {
        if (!m_reservedCore) {
            m_reservedCore = new MpvCore(MpvCore::Mode::Video, this);
            m_reservedCore->setHardwareDecoding(m_hardwareDecoding);
        }
        return m_reservedCore->isValid() ? m_reservedCore : nullptr;
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
    // The user can cap inline decoding, down to none at all, in which case
    // bubbles stay thumbnails and only the full-screen viewer plays.
    const Settings *settings = Settings::instance();
    const int limit = settings ? std::clamp(settings->inlineVideoLimit(), 0, inlineCoreLimit) : inlineCoreLimit;
    if (limit == 0) {
        return nullptr;
    }
    for (Slot &slot : m_slots) {
        if (!slot.owner) {
            return slot.core;
        }
    }
    if (m_slots.size() >= limit) {
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
    if (core == m_reservedCore) {
        core->stop();
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

void MpvPool::setReservedItem(MpvVideoItem *item)
{
    m_reservedItem = item;
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
    if (m_reservedCore) {
        m_reservedCore->setHardwareDecoding(enabled);
    }
}

