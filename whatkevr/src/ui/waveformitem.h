// SPDX-License-Identifier: BSD-3-Clause
#pragma once

#include <QColor>
#include <QList>
#include <QQmlEngine>
#include <QQuickItem>

/**
 * WaveformItem draws a voice note's amplitude envelope as a row of rounded
 * bars, filled up to the playback position.
 *
 * It is a scene-graph item rather than a Repeater of Rectangles because a
 * conversation can hold many voice notes: 64 QML items each would cost far more
 * than one geometry node, and this way the whole waveform is a single batched
 * draw with no per-bar bindings to re-evaluate while scrolling.
 */
class WaveformItem : public QQuickItem
{
    Q_OBJECT
    QML_NAMED_ELEMENT(Waveform)

    /// Amplitude buckets, 0-100, as delivered in media.waveform.
    Q_PROPERTY(QVariantList values READ values WRITE setValues NOTIFY valuesChanged)
    /// Playback progress, 0.0 to 1.0.
    Q_PROPERTY(qreal progress READ progress WRITE setProgress NOTIFY progressChanged)
    Q_PROPERTY(QColor playedColor READ playedColor WRITE setPlayedColor NOTIFY playedColorChanged)
    Q_PROPERTY(QColor pendingColor READ pendingColor WRITE setPendingColor NOTIFY pendingColorChanged)
    /// Bar width and gap in pixels.
    Q_PROPERTY(qreal barWidth READ barWidth WRITE setBarWidth NOTIFY barWidthChanged)
    Q_PROPERTY(qreal barSpacing READ barSpacing WRITE setBarSpacing NOTIFY barSpacingChanged)

public:
    explicit WaveformItem(QQuickItem *parent = nullptr);

    QVariantList values() const
    {
        return m_values;
    }
    void setValues(const QVariantList &values);

    qreal progress() const
    {
        return m_progress;
    }
    void setProgress(qreal progress);

    QColor playedColor() const
    {
        return m_playedColor;
    }
    void setPlayedColor(const QColor &color);

    QColor pendingColor() const
    {
        return m_pendingColor;
    }
    void setPendingColor(const QColor &color);

    qreal barWidth() const
    {
        return m_barWidth;
    }
    void setBarWidth(qreal width);

    qreal barSpacing() const
    {
        return m_barSpacing;
    }
    void setBarSpacing(qreal spacing);

    /// Maps an x position to a fraction of the waveform, for scrubbing.
    Q_INVOKABLE qreal fractionAt(qreal x) const;

    /// Geometry the tests assert on: how many bars the current width draws,
    /// and the clamped 0-1 envelope the samples were reduced to.
    [[nodiscard]] int barCountForWidth() const
    {
        return visibleBarCount();
    }
    [[nodiscard]] QList<qreal> normalizedValues() const
    {
        return m_normalized;
    }

Q_SIGNALS:
    void valuesChanged();
    void progressChanged();
    void playedColorChanged();
    void pendingColorChanged();
    void barWidthChanged();
    void barSpacingChanged();

protected:
    QSGNode *updatePaintNode(QSGNode *oldNode, UpdatePaintNodeData *data) override;
    void geometryChange(const QRectF &newGeometry, const QRectF &oldGeometry) override;

private:
    /// How many bars fit at the current width. The envelope is resampled to
    /// this many, so it always spans the full width rather than leaving the
    /// sample count to decide how much of the row gets used.
    int visibleBarCount() const;
    /// The envelope value for one bar: the peak of the samples it covers when
    /// downsampling, an interpolation between neighbours when upsampling.
    qreal sampleForBar(int bar, int bars) const;
    /// One device pixel in logical units, for snapping.
    qreal devicePixel() const;
    /// The bar width rounded to a whole number of device pixels, so no bar
    /// renders a pixel fatter than its neighbours.
    qreal snappedBarWidth() const;

    QVariantList m_values;
    QList<qreal> m_normalized;
    qreal m_progress = 0.0;
    QColor m_playedColor;
    QColor m_pendingColor;
    qreal m_barWidth = 3.0;
    qreal m_barSpacing = 2.0;
};

