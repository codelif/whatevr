// SPDX-License-Identifier: BSD-3-Clause
#include <QTest>

#include "waveformitem.h"

/**
 * Waveform geometry tests.
 *
 * The bars used to be centred in whatever width they were given and capped at
 * the sample count, so a 48-sample envelope in a wider row floated in the
 * middle with dead space either side, and scrubbing (which maps x across the
 * whole item) landed on a different bar than the one under the pointer.
 */
class TestWaveformItem : public QObject
{
    Q_OBJECT

private Q_SLOTS:
    void reportsAnImplicitSizeFromItsSamples()
    {
        WaveformItem item;
        QCOMPARE(item.implicitWidth(), 0.0);
        QVERIFY(item.implicitHeight() > 0.0);

        item.setBarWidth(2);
        item.setBarSpacing(2);
        item.setValues(QVariantList{10, 20, 30});
        // Three bars at a 4px pitch, minus the trailing gap.
        QCOMPARE(item.implicitWidth(), 10.0);
    }

    void scrubbingMapsAcrossTheWholeWidth()
    {
        WaveformItem item;
        item.setWidth(200);
        item.setHeight(24);
        item.setValues(QVariantList{10, 90, 40});

        QCOMPARE(item.fractionAt(0), 0.0);
        QCOMPARE(item.fractionAt(100), 0.5);
        QCOMPARE(item.fractionAt(200), 1.0);
        // Out of bounds is clamped rather than seeking past the end.
        QCOMPARE(item.fractionAt(-40), 0.0);
        QCOMPARE(item.fractionAt(4000), 1.0);
    }

    void aShortWaveformStillSpansTheRow()
    {
        WaveformItem item;
        item.setBarWidth(2);
        item.setBarSpacing(2);
        item.setWidth(400);
        item.setHeight(24);
        item.setValues(QVariantList{10, 90, 40});

        // 400px at a 4px pitch is 100 bars; the three samples are stretched to
        // fill them rather than drawing three bars in a corner.
        QCOMPARE(item.barCountForWidth(), 100);
    }

    void amplitudesAreClampedAndFloored()
    {
        WaveformItem item;
        item.setValues(QVariantList{0, 100, 400, -20});
        const QList<qreal> normalized = item.normalizedValues();
        QCOMPARE(normalized.size(), 4);
        // Silence still draws something, and nothing exceeds full height.
        QVERIFY(normalized.at(0) > 0.0);
        QCOMPARE(normalized.at(1), 1.0);
        QCOMPARE(normalized.at(2), 1.0);
        QVERIFY(normalized.at(3) > 0.0);
    }

    // Senders ship envelopes that sit in a narrow band: steady speech comes
    // over the wire as 40-80 out of 100, which drawn literally is a picket
    // fence with no shape. The loudest bar has to reach the top for the
    // quieter ones to read as quieter.
    void aNarrowBandEnvelopeIsScaledToItsPeak()
    {
        WaveformItem item;
        item.setValues(QVariantList{40, 60, 80, 50});
        const QList<qreal> normalized = item.normalizedValues();

        QCOMPARE(normalized.at(2), 1.0);
        QCOMPARE(normalized.at(0), 0.5);
        QVERIFY(normalized.at(1) > normalized.at(0));
        QVERIFY(normalized.at(1) < normalized.at(2));
    }

    // ... but a genuinely quiet recording stays quiet. Scaling by the peak
    // alone would turn a near-silent note into a full-height block.
    void aQuietRecordingIsNotAmplifiedToFullHeight()
    {
        WaveformItem item;
        item.setValues(QVariantList{4, 8, 12});
        const QList<qreal> normalized = item.normalizedValues();

        for (const qreal amplitude : normalized) {
            QVERIFY(amplitude < 0.3);
        }
    }
};

QTEST_MAIN(TestWaveformItem)
#include "tst_waveformitem.moc"
