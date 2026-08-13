// SPDX-License-Identifier: BSD-3-Clause
#include "waveformitem.h"

#include <QQuickWindow>
#include <QSGGeometryNode>
#include <QSGVertexColorMaterial>

#include <algorithm>
#include <cmath>

namespace
{
// A bar never collapses to nothing: silence in the middle of speech should
// still read as part of the waveform.
constexpr qreal minimumBarFraction = 0.12;
constexpr int verticesPerBar = 6; // two triangles
// Height a waveform asks for when nothing else decides, in logical pixels.
constexpr qreal naturalHeight = 24.0;
}

WaveformItem::WaveformItem(QQuickItem *parent)
    : QQuickItem(parent)
{
    setFlag(ItemHasContents, true);
    // A layout has no other way to know what a waveform wants: it draws into
    // whatever box it is given and would otherwise collapse to nothing.
    setImplicitHeight(naturalHeight);
}

void WaveformItem::setValues(const QVariantList &values)
{
    if (m_values == values) {
        return;
    }
    m_values = values;

    m_normalized.clear();
    m_normalized.reserve(values.size());
    qreal peak = 0.0;
    for (const QVariant &value : values) {
        const qreal amplitude = std::clamp(value.toReal(), qreal(0), qreal(100)) / 100.0;
        peak = std::max(peak, amplitude);
        m_normalized.append(amplitude);
    }

    // Scale the envelope so its loudest bar reaches full height. Senders ship
    // waveforms that sit in a narrow band (steady speech lands around 0.4-0.8),
    // and drawn literally that is a picket fence with no shape to it. The floor
    // on the divisor keeps a genuinely quiet recording quiet instead of
    // amplifying its noise to look like shouting.
    const qreal scale = 1.0 / std::max(peak, qreal(0.5));
    for (qreal &amplitude : m_normalized) {
        amplitude = std::max(std::min(amplitude * scale, qreal(1)), minimumBarFraction);
    }

    setImplicitWidth(m_normalized.isEmpty()
                         ? 0.0
                         : m_normalized.size() * (m_barWidth + m_barSpacing) - m_barSpacing);
    Q_EMIT valuesChanged();
    update();
}

void WaveformItem::setProgress(qreal progress)
{
    progress = std::clamp(progress, qreal(0), qreal(1));
    if (qFuzzyCompare(m_progress + 1.0, progress + 1.0)) {
        return;
    }
    m_progress = progress;
    Q_EMIT progressChanged();
    update();
}

void WaveformItem::setPlayedColor(const QColor &color)
{
    if (m_playedColor == color) {
        return;
    }
    m_playedColor = color;
    Q_EMIT playedColorChanged();
    update();
}

void WaveformItem::setPendingColor(const QColor &color)
{
    if (m_pendingColor == color) {
        return;
    }
    m_pendingColor = color;
    Q_EMIT pendingColorChanged();
    update();
}

void WaveformItem::setBarWidth(qreal width)
{
    if (qFuzzyCompare(m_barWidth, width) || width <= 0) {
        return;
    }
    m_barWidth = width;
    Q_EMIT barWidthChanged();
    update();
}

void WaveformItem::setBarSpacing(qreal spacing)
{
    if (qFuzzyCompare(m_barSpacing, spacing) || spacing < 0) {
        return;
    }
    m_barSpacing = spacing;
    Q_EMIT barSpacingChanged();
    update();
}

qreal WaveformItem::fractionAt(qreal x) const
{
    if (width() <= 0) {
        return 0.0;
    }
    // The bars start at the left edge and the last one ends at the right edge,
    // so the mapping is the plain one and a tap lands on the bar under it.
    return std::clamp(x / width(), qreal(0), qreal(1));
}

qreal WaveformItem::devicePixel() const
{
    const qreal ratio = window() ? window()->effectiveDevicePixelRatio() : 1.0;
    return ratio > 0 ? 1.0 / ratio : 1.0;
}

qreal WaveformItem::snappedBarWidth() const
{
    // A bar is a hairline shape: at 1x a 2.0 logical bar is 2 device pixels, at
    // 1.5x it would be 3, and rounding it to a whole number of device pixels is
    // what keeps every bar the same weight instead of some rendering a pixel
    // fatter than their neighbours.
    const qreal unit = devicePixel();
    return std::max(unit, std::round(m_barWidth / unit) * unit);
}

int WaveformItem::visibleBarCount() const
{
    if (m_normalized.isEmpty() || width() <= 0) {
        return 0;
    }
    const qreal pitch = snappedBarWidth() + m_barSpacing;
    const int fits = int(std::floor((width() + m_barSpacing) / pitch));
    return std::max(0, fits);
}

qreal WaveformItem::sampleForBar(int bar, int bars) const
{
    const int count = int(m_normalized.size());
    if (count == 0 || bars <= 0) {
        return 0.0;
    }
    if (count == 1) {
        return m_normalized.first();
    }

    const qreal from = qreal(bar) * count / bars;
    const qreal to = qreal(bar + 1) * count / bars;
    if (to - from >= 1.0) {
        // More samples than bars: the peak of the span, so a downsampled
        // envelope keeps its shape instead of averaging into a flat band.
        const int first = std::clamp(int(std::floor(from)), 0, count - 1);
        const int last = std::clamp(int(std::ceil(to)) - 1, first, count - 1);
        qreal peak = m_normalized.at(first);
        for (int i = first + 1; i <= last; ++i) {
            peak = std::max(peak, m_normalized.at(i));
        }
        return peak;
    }

    // Fewer samples than bars: interpolate, so a short waveform stretches
    // smoothly across the row rather than leaving it half empty.
    const qreal position = (from + to) / 2.0;
    const int low = std::clamp(int(std::floor(position)), 0, count - 1);
    const int high = std::min(low + 1, count - 1);
    const qreal blend = std::clamp(position - qreal(low), qreal(0), qreal(1));
    return m_normalized.at(low) * (1.0 - blend) + m_normalized.at(high) * blend;
}

void WaveformItem::geometryChange(const QRectF &newGeometry, const QRectF &oldGeometry)
{
    QQuickItem::geometryChange(newGeometry, oldGeometry);
    if (newGeometry.size() != oldGeometry.size()) {
        update();
    }
}

QSGNode *WaveformItem::updatePaintNode(QSGNode *oldNode, UpdatePaintNodeData *)
{
    const int bars = visibleBarCount();
    if (bars == 0) {
        delete oldNode;
        return nullptr;
    }

    auto *node = static_cast<QSGGeometryNode *>(oldNode);
    if (!node) {
        node = new QSGGeometryNode;
        auto *geometry = new QSGGeometry(QSGGeometry::defaultAttributes_ColoredPoint2D(), bars * verticesPerBar);
        geometry->setDrawingMode(QSGGeometry::DrawTriangles);
        node->setGeometry(geometry);
        node->setFlag(QSGNode::OwnsGeometry);

        auto *material = new QSGVertexColorMaterial;
        node->setMaterial(material);
        node->setFlag(QSGNode::OwnsMaterial);
    } else if (node->geometry()->vertexCount() != bars * verticesPerBar) {
        node->geometry()->allocate(bars * verticesPerBar);
    }

    auto *vertices = node->geometry()->vertexDataAsColoredPoint2D();
    // Bars start at the left edge and the last one ends at the right edge: the
    // leftover from the integer bar count is spread across the gaps rather than
    // left as dead space, which is what made the waveform look adrift in its
    // row. The bar width itself stays put so the texture does not change.
    const qreal barWidth = snappedBarWidth();
    const qreal pitch = bars > 0 ? (width() + m_barSpacing) / qreal(bars) : barWidth + m_barSpacing;
    const qreal centreY = height() / 2.0;
    // Never quite touch the top and bottom edges: a waveform that fills its
    // whole box reads as a solid block rather than a signal.
    const qreal maximumBarHeight = height() * 0.85;
    const int playedBars = int(std::round(m_progress * bars));
    // Bars are axis-aligned quads, so snapping them to whole device pixels is
    // what keeps the edges crisp without paying for antialiasing.
    const qreal unit = devicePixel();
    const auto snap = [unit](qreal value) {
        return std::round(value / unit) * unit;
    };

    for (int i = 0; i < bars; ++i) {
        const qreal amplitude = sampleForBar(i, bars);
        const qreal barHeight = std::max(unit, amplitude * maximumBarHeight);

        // Snap the left edge, then add the already-snapped width, so every bar
        // is the same number of device pixels wide however the pitch falls.
        const float left = float(snap(i * pitch));
        const float right = float(left + barWidth);
        const float top = float(snap(centreY - barHeight / 2.0));
        const float bottom = float(snap(centreY + barHeight / 2.0));

        const QColor &color = i < playedBars ? m_playedColor : m_pendingColor;
        const uchar r = uchar(color.red() * color.alphaF());
        const uchar g = uchar(color.green() * color.alphaF());
        const uchar b = uchar(color.blue() * color.alphaF());
        const uchar a = uchar(color.alpha());

        const auto put = [&](int index, float x, float y) {
            vertices[index].set(x, y, r, g, b, a);
        };
        const int base = i * verticesPerBar;
        put(base + 0, left, top);
        put(base + 1, right, top);
        put(base + 2, left, bottom);
        put(base + 3, right, top);
        put(base + 4, right, bottom);
        put(base + 5, left, bottom);
    }

    node->markDirty(QSGNode::DirtyGeometry);
    return node;
}

