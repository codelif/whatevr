# 3.21 is required by Qt 6.8's qt_add_grpc/qt_add_protobuf CMake API and by the
# ECM/KDEInstallDirs modules used below.
cmake_minimum_required(VERSION 3.21)

# Version is supplied by the top-level justfile (-DWHATEVR_VERSION numeric,
# -DWHATEVR_VERSION_FULL descriptive). When CMake is invoked standalone (e.g. by
# distro packagers) fall back to the repo-root VERSION file so there is still a
# single source of truth.
if(NOT DEFINED WHATEVR_VERSION)
    if(EXISTS "${CMAKE_CURRENT_SOURCE_DIR}/../VERSION")
        file(READ "${CMAKE_CURRENT_SOURCE_DIR}/../VERSION" WHATEVR_VERSION)
        string(STRIP "${WHATEVR_VERSION}" WHATEVR_VERSION)
        string(REGEX MATCH "^[0-9]+\\.[0-9]+\\.[0-9]+" WHATEVR_VERSION "${WHATEVR_VERSION}")
    endif()
    if(NOT WHATEVR_VERSION)
        set(WHATEVR_VERSION "0.0.0")
    endif()
endif()
if(NOT DEFINED WHATEVR_VERSION_FULL)
    set(WHATEVR_VERSION_FULL "${WHATEVR_VERSION}")
endif()

project(whatkevr VERSION ${WHATEVR_VERSION} LANGUAGES CXX)

# Build type is selected by the entrypoint. Release builds use LTO when the
# toolchain supports it; debug builds stay easy to inspect.
if(CMAKE_BUILD_TYPE STREQUAL "Release")
    include(CheckIPOSupported)
    check_ipo_supported(RESULT whatkevr_ipo_supported OUTPUT whatkevr_ipo_error)
    if(whatkevr_ipo_supported)
        set(CMAKE_INTERPROCEDURAL_OPTIMIZATION ON)
    else()
        message(STATUS "LTO not supported: ${whatkevr_ipo_error}")
    endif()
endif()

# Floors are chosen against the APIs actually used, not the dev's installed
# versions. Qt 6.8 is the HARD floor: Qt GRPC/Protobuf only became stable in
# 6.8 LTS, and the code relies on 6.8 signatures (QGrpcCallReply::finished(
# QGrpcStatus), shared_ptr channels). KF 6.5 is the current target floor for
# Kirigami/Prison/KI18n-QML; validated per-distro in CI.
set(QT_MIN_VERSION "6.8.0")
set(KF_MIN_VERSION "6.5.0")

find_package(ECM ${KF_MIN_VERSION} REQUIRED NO_MODULE)
set(CMAKE_MODULE_PATH ${ECM_MODULE_PATH})

include(FeatureSummary)
include(KDECompilerSettings NO_POLICY_SCOPE)
include(KDEInstallDirs)
include(KDECMakeSettings)

set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)

find_package(Qt6 ${QT_MIN_VERSION} REQUIRED COMPONENTS Core Gui Network Protobuf Grpc Qml Quick QuickControls2 ShaderTools Widgets)
find_package(KF6 ${KF_MIN_VERSION} REQUIRED COMPONENTS CoreAddons DBusAddons I18n Kirigami Prison)
find_package(KF6ColorScheme ${KF_MIN_VERSION} REQUIRED)
find_package(KF6KirigamiAddons 1.0.0 REQUIRED)
find_package(rlottie REQUIRED)

if(QT_KNOWN_POLICY_QTP0004)
    qt_policy(SET QTP0004 NEW)
endif()

qt_standard_project_setup(REQUIRES ${QT_MIN_VERSION})

set_source_files_properties(src/qml/I18n.qml PROPERTIES
    QT_QML_SINGLETON_TYPE TRUE
)

qt_add_executable(whatkevr
    MANUAL_FINALIZATION
    src/main.cpp
    src/app/appcontroller.cpp
    src/app/appcontroller.h
    src/app/settings.cpp
    src/app/settings.h
    src/app/stickercontroller.cpp
    src/app/stickercontroller.h
    src/models/chatlistmodel.cpp
    src/models/chatlistmodel.h
    src/models/chatlistfiltermodel.cpp
    src/models/chatlistfiltermodel.h
    src/models/emojimodel.cpp
    src/models/emojimodel.h
    src/models/messagelistmodel.cpp
    src/models/messagelistmodel.h
    src/models/starredmessagesmodel.cpp
    src/models/starredmessagesmodel.h
    src/models/pinnedmessagesmodel.cpp
    src/models/pinnedmessagesmodel.h
    src/models/searchresultsmodel.cpp
    src/models/searchresultsmodel.h
    src/models/stickermodel.cpp
    src/models/stickermodel.h
    src/models/stickerpackmodel.cpp
    src/models/stickerpackmodel.h
    src/ui/rlottiestickeritem.cpp
    src/ui/rlottiestickeritem.h
    src/util/messagemarkup.cpp
    src/util/messagemarkup.h
    src/util/richtext.cpp
    src/util/richtext.h
    src/util/tlds.cpp
    src/util/tlds.h
)

set(WHATEVR_PROTO_FILE "${CMAKE_CURRENT_SOURCE_DIR}/../proto/whatevr.proto")
set(WHATEVR_PROTO_INCLUDE_DIR "${CMAKE_CURRENT_SOURCE_DIR}/../proto")

qt_add_protobuf(whatkevr
    GENERATE_PACKAGE_SUBFOLDERS
    PROTO_FILES
        ${WHATEVR_PROTO_FILE}
    PROTO_INCLUDES
        ${WHATEVR_PROTO_INCLUDE_DIR}
)

qt_add_grpc(whatkevr CLIENT
    GENERATE_PACKAGE_SUBFOLDERS
    PROTO_FILES
        ${WHATEVR_PROTO_FILE}
    PROTO_INCLUDES
        ${WHATEVR_PROTO_INCLUDE_DIR}
)

set_target_properties(whatkevr PROPERTIES
    OUTPUT_NAME whatkevr
    RUNTIME_OUTPUT_DIRECTORY "${CMAKE_BINARY_DIR}/bin"
)

configure_file(
    "${CMAKE_CURRENT_SOURCE_DIR}/src/version.h.in"
    "${CMAKE_CURRENT_BINARY_DIR}/version.h"
    @ONLY
)

target_include_directories(whatkevr PRIVATE
    ${CMAKE_CURRENT_BINARY_DIR}
    ${CMAKE_CURRENT_SOURCE_DIR}/src/app
    ${CMAKE_CURRENT_SOURCE_DIR}/src/models
    ${CMAKE_CURRENT_SOURCE_DIR}/src/ui
    ${CMAKE_CURRENT_SOURCE_DIR}/src/util
)

qt_add_qml_module(whatkevr
    URI Whatevr
    VERSION 1.0
    NO_IMPORT_SCAN
    QML_FILES
        src/qml/Main.qml
        src/qml/I18n.qml
        src/qml/components/AvatarImage.qml
        src/qml/components/StatusChip.qml
        src/qml/components/DateSeparatorPill.qml
        src/qml/components/UnreadSeparator.qml
        src/qml/components/ProgressCircle.qml
        src/qml/components/ChatListDelegate.qml
        src/qml/components/ChatListPane.qml
        src/qml/components/ConversationPane.qml
        src/qml/components/StarredMessagesPage.qml
        src/qml/components/ContactInfoDialog.qml
        src/qml/components/ProfilePictureViewer.qml
        src/qml/components/SearchResultDelegate.qml
        src/qml/components/SearchHighlight.js
        src/qml/components/PinnedMessagesBanner.qml
        src/qml/components/HistorySyncStrip.qml
        src/qml/components/KineticWheelScroller.qml
        src/qml/components/DiscreetScrollBar.qml
        src/qml/components/RowScrollBar.qml
        src/qml/components/ReplyPreview.qml
        src/qml/components/RoundedImage.qml
        src/qml/components/ChatBubble.qml
        src/qml/components/QuickReactionBar.qml
        src/qml/components/QuickReactionPopup.qml
        src/qml/components/ReactionEmojiPopup.qml
        src/qml/components/ReactionRow.qml
        src/qml/components/ReactionDetailsDialog.qml
        src/qml/components/ExpressionPicker.qml
        src/qml/components/EmojiPane.qml
        src/qml/components/StickerPane.qml
        src/qml/components/MessageView.qml
        src/qml/components/MessageComposer.qml
        src/qml/components/CenteredDialog.qml
        src/qml/components/MessageInfoDialog.qml
        src/qml/components/MessageContentDialog.qml
        src/qml/components/ForwardChatPickerDialog.qml
        src/qml/components/ChatListSidebar.qml
        src/qml/components/Wallpapers.js
        src/qml/pages/LoginPage.qml
        src/qml/pages/StatusPage.qml
        src/qml/settings/SettingsSearch.js
        src/qml/settings/SettingsView.qml
        src/qml/settings/SettingsWindow.qml
        src/qml/settings/SettingsPage.qml
        src/qml/settings/PreviewSelector.qml
        src/qml/settings/PrivacyAudienceCombo.qml
        src/qml/settings/AppearancePage.qml
        src/qml/settings/ChatsPage.qml
        src/qml/settings/NotificationsPage.qml
        src/qml/settings/PrivacyPage.qml
        src/qml/settings/BlockedContactsPage.qml
        src/qml/settings/ProfilePage.qml
        src/qml/settings/WindowLayoutPage.qml
        src/qml/settings/StoragePage.qml
        src/qml/settings/EmojiStickersPage.qml
        src/qml/settings/KeyboardShortcutsPage.qml
        src/qml/settings/AboutPage.qml
)

qt6_add_shaders(whatkevr "roundedimage_shaders"
    PREFIX "/qt/qml/Whatevr"
    BASE "src/qml"
    FILES src/qml/shaders/roundedimage.frag
)

qt_add_resources(whatkevr emoji_metadata
    PREFIX /
    FILES
        data/emoji/emoji_17_0_ordering.json
        data/emoji/emoji_keywords_en.json
        data/emoji/LICENSE.googlefonts-emoji-metadata
        data/emoji/LICENSE.emojilib
        data/icons/checkmark-bold.svg
)

# Embed the repo-root LICENSE (one dir up from this project) so the About page can
# show the full BSD-3-Clause text via KAboutData::setLicenseTextFile(":/LICENSE").
qt_add_resources(whatkevr app_license
    PREFIX /
    BASE ${CMAKE_CURRENT_SOURCE_DIR}/..
    FILES ${CMAKE_CURRENT_SOURCE_DIR}/../LICENSE
)

target_link_libraries(whatkevr PRIVATE
    Qt6::Core
    Qt6::Gui
    Qt6::Grpc
    Qt6::Network
    Qt6::Protobuf
    Qt6::Qml
    Qt6::Quick
    Qt6::QuickControls2
    Qt6::Widgets
    KF6::CoreAddons
    KF6::ColorScheme
    KF6::DBusAddons
    KF6::I18n
    KF6::Kirigami
    KF6::Prison
    KirigamiAddonsComponents
    rlottie::rlottie
)

install(TARGETS whatkevr RUNTIME DESTINATION ${KDE_INSTALL_BINDIR})
install(FILES
    data/in.codelif.Whatevr.desktop
    DESTINATION ${KDE_INSTALL_APPDIR}
)
install(FILES
    data/icons/in.codelif.Whatevr.svg
    DESTINATION ${KDE_INSTALL_ICONDIR}/hicolor/scalable/apps
)
install(FILES
    data/in.codelif.Whatevr.metainfo.xml
    DESTINATION ${KDE_INSTALL_METAINFODIR}
)
install(FILES
    data/in.codelif.Whatevr.xml
    DESTINATION ${KDE_INSTALL_MIMEDIR}
)

qt_finalize_executable(whatkevr)

feature_summary(WHAT ALL FATAL_ON_MISSING_REQUIRED_PACKAGES)
