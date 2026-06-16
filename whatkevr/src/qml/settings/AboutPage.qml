import QtQuick
import org.kde.kirigamiaddons.formcard as FormCard
import org.kde.coreaddons as Core

// The upstream formcard AboutPage renders the license through KAboutLicense,
// which couples a license's display name to its key: KAboutLicense::BSD_3_Clause
// yields only a "see the SPDX website" stub, and feeding the full text from a
// file labels it "Custom". Neither gives "BSD-3-Clause" + the real terms.
//
// AboutPage's `aboutData` is a plain-object property (same shape as KAboutData),
// so we pass through every field from the live KAboutData gadget
// (Core.AboutData) and override only `licenses` with a single entry pairing the
// proper SPDX name with the full text. The text is the one KAboutData already
// loaded from :/LICENSE via setLicenseTextFile() (its only quirk is the "Custom"
// name, which is exactly what this override replaces). The Components section
// reads from a separate C++-backed singleton, so it is unaffected.
FormCard.AboutPage {
    id: page

    readonly property var _about: Core.AboutData

    readonly property string _licenseText: _about.licenses.length ? _about.licenses[0].text : ""

    aboutData: ({
        "displayName": _about.displayName,
        "productName": _about.productName,
        "componentName": _about.componentName,
        "shortDescription": _about.shortDescription,
        "homepage": _about.homepage,
        "bugAddress": _about.bugAddress,
        "version": _about.version,
        "otherText": _about.otherText,
        "authors": _about.authors,
        "credits": _about.credits,
        "translators": _about.translators,
        "copyrightStatement": _about.copyrightStatement,
        "desktopFileName": _about.desktopFileName,
        "programLogo": _about.programLogo,
        "programIconName": _about.programIconName,
        "licenses": [{
            "name": "BSD-3-Clause",
            "text": page._licenseText,
            "spdx": "BSD-3-Clause"
        }]
    })
}
