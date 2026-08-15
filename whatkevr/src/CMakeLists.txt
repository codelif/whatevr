set_source_files_properties(qml/I18n.qml qml/Palette.qml PROPERTIES
    QT_QML_SINGLETON_TYPE TRUE
)

# Everything the app is made of except main.cpp, kept in a variable so the DN9
# delegate benchmark below can build the exact same code into its own binary.
set(WHATKEVR_SOURCES
    app/protocolcontroller.cpp
    app/protocolcontroller.h
    app/protocolstickercontroller.cpp
    app/protocolstickercontroller.h
    app/settings.cpp
    app/settings.h
    models/emojimodel.cpp
    models/emojimodel.h
    models/protocolmessagemodel.cpp
    models/protocolmessagemodel.h
    models/protocolsearchmodel.cpp
    models/protocolsearchmodel.h
    media/audioplayer.cpp
    media/audioplayer.h
    media/mpvcore.cpp
    media/mpvcore.h
    media/mpvvideoitem.cpp
    media/mpvvideoitem.h
    media/playbacksession.cpp
    media/playbacksession.h
    media/videoarbiter.cpp
    media/videoarbiter.h
    media/videoframeprovider.cpp
    media/videoframeprovider.h
    protocol/protocolclient.cpp
    protocol/protocolclient.h
    protocol/collectionviewmodel.cpp
    protocol/collectionviewmodel.h
    protocol/objectviewmodel.cpp
    protocol/objectviewmodel.h
    ui/rlottiestickeritem.cpp
    ui/rlottiestickeritem.h
    ui/waveformitem.cpp
    ui/waveformitem.h
    util/messagemarkup.cpp
    util/messagemarkup.h
    util/messagerow.cpp
    util/messagerow.h
    util/richtext.cpp
    util/richtext.h
    util/tlds.cpp
    util/tlds.h
)

set(WHATKEVR_LINK_LIBRARIES
    Qt6::Core
    Qt6::Gui
    Qt6::Network
    Qt6::OpenGL
    Qt6::Qml
    Qt6::Quick
    Qt6::QuickControls2
    Qt6::Widgets
    KF6::ColorScheme
    KF6::CoreAddons
    KF6::DBusAddons
    KF6::I18n
    KF6::Kirigami
    KF6::Prison
    PkgConfig::RLottie
    PkgConfig::Mpv
)

set(WHATKEVR_INCLUDE_DIRS
    ${CMAKE_CURRENT_BINARY_DIR}
    ${CMAKE_CURRENT_SOURCE_DIR}/app
    ${CMAKE_CURRENT_SOURCE_DIR}/media
    ${CMAKE_CURRENT_SOURCE_DIR}/models
    ${CMAKE_CURRENT_SOURCE_DIR}/protocol
    ${CMAKE_CURRENT_SOURCE_DIR}/ui
    ${CMAKE_CURRENT_SOURCE_DIR}/util
)

qt_add_executable(whatkevr
    MANUAL_FINALIZATION
    main.cpp
    ${WHATKEVR_SOURCES}
)

set_target_properties(whatkevr PROPERTIES
    OUTPUT_NAME whatkevr
    RUNTIME_OUTPUT_DIRECTORY "${CMAKE_BINARY_DIR}/bin"
)

configure_file(version.h.in "${CMAKE_CURRENT_BINARY_DIR}/version.h" @ONLY)

target_include_directories(whatkevr PRIVATE ${WHATKEVR_INCLUDE_DIRS})

set(WHATKEVR_QML_FILES
        qml/Main.qml
        qml/I18n.qml
        qml/Palette.qml
        qml/components/AvatarImage.qml
        qml/components/StatusChip.qml
        qml/components/DateSeparatorPill.qml
        qml/components/UnreadSeparator.qml
        qml/components/ProgressCircle.qml
        qml/components/ChatListDelegate.qml
        qml/components/ChatListPane.qml
        qml/components/ConversationPane.qml
        qml/components/StarredMessagesPage.qml
        qml/components/ContactInfoDialog.qml
        qml/components/ProfilePictureViewer.qml
        qml/components/SearchResultDelegate.qml
        qml/components/SearchHighlight.js
        qml/components/PinnedMessagesBanner.qml
        qml/components/HistorySyncStrip.qml
        qml/components/KineticWheelScroller.qml
        qml/components/DiscreetScrollBar.qml
        qml/components/RowScrollBar.qml
        qml/components/ReplyPreview.qml
        qml/components/RoundedImage.qml
        qml/components/ChatWallpaper.qml
        qml/components/ChatBubble.qml
    qml/components/ChatMediaGalleryPage.qml
    qml/components/DocumentBubble.qml
    qml/components/MediaViewer.qml
    qml/components/VideoBubble.qml
    qml/components/MpvVideoSurface.qml
    qml/components/VideoSurface.qml
    qml/components/VideoSurfaceBackend.qml
    qml/components/MediaFormat.js
    qml/components/MediaOverlayButton.qml
    qml/components/MediaDragArea.qml
    qml/components/VoiceBubble.qml
        qml/components/FramelessBubble.qml
        qml/components/QuickReactionBar.qml
        qml/components/QuickReactionPopup.qml
        qml/components/ReactionEmojiPopup.qml
        qml/components/ReactionRow.qml
        qml/components/ReactionDetailsDialog.qml
        qml/components/ExpressionPicker.qml
        qml/components/EmojiPane.qml
        qml/components/StickerPane.qml
        qml/components/MessageView.qml
        qml/components/MessageComposer.qml
        qml/components/CenteredDialog.qml
        qml/components/MessageInfoDialog.qml
        qml/components/MessageContentDialog.qml
        qml/components/ForwardChatPickerDialog.qml
        qml/components/ChatListSidebar.qml
        qml/components/Wallpapers.js
        qml/pages/LoginPage.qml
        qml/pages/StatusPage.qml
        qml/settings/SettingsSearch.js
        qml/settings/SettingsView.qml
        qml/settings/SettingsWindow.qml
        qml/settings/SettingsPage.qml
        qml/settings/PreviewSelector.qml
        qml/settings/FormSliderDelegate.qml
        qml/settings/PrivacyAudienceCombo.qml
        qml/settings/AppearancePage.qml
        qml/settings/ChatsPage.qml
        qml/settings/NotificationsPage.qml
        qml/settings/PrivacyPage.qml
        qml/settings/BlockedContactsPage.qml
        qml/settings/ProfilePage.qml
        qml/settings/WindowLayoutPage.qml
        qml/settings/StoragePage.qml
        qml/settings/EmojiStickersPage.qml
        qml/settings/KeyboardShortcutsPage.qml
        qml/settings/AboutPage.qml
    qml/settings/AdvancedPage.qml
)

qt_add_qml_module(whatkevr
    URI Whatevr
    VERSION 1.0
    NO_IMPORT_SCAN
    QML_FILES ${WHATKEVR_QML_FILES}
)

qt6_add_shaders(whatkevr "roundedimage_shaders"
    PREFIX "/qt/qml/Whatevr"
    BASE "qml"
    FILES
        qml/shaders/roundedimage.frag
        qml/shaders/chatwallpaper.frag
)

qt_add_resources(whatkevr emoji_metadata
    PREFIX /
    BASE ${PROJECT_SOURCE_DIR}
    FILES
        ${PROJECT_SOURCE_DIR}/data/emoji/emoji_17_0_ordering.json
        ${PROJECT_SOURCE_DIR}/data/emoji/emoji_keywords_en.json
        ${PROJECT_SOURCE_DIR}/data/emoji/LICENSE.googlefonts-emoji-metadata
        ${PROJECT_SOURCE_DIR}/data/emoji/LICENSE.emojilib
        ${PROJECT_SOURCE_DIR}/data/icons/checkmark-bold.svg
        ${PROJECT_SOURCE_DIR}/data/wallpapers/doodle.svg
)

qt_add_resources(whatkevr app_license
    PREFIX /
    BASE ${PROJECT_SOURCE_DIR}/..
    FILES ${PROJECT_SOURCE_DIR}/../LICENSE
)

target_link_libraries(whatkevr PRIVATE ${WHATKEVR_LINK_LIBRARIES})

install(TARGETS whatkevr RUNTIME DESTINATION ${KDE_INSTALL_BINDIR})

qt_finalize_executable(whatkevr)

# ---------------------------------------------------------------------------
# DN9 delegate-construction benchmark.
#
# The app's QML module is attached to the `whatkevr` executable, so nothing
# outside that binary can instantiate ChatBubble. Rather than restructure the
# app into a library (static QML module plugins need Q_IMPORT_QML_PLUGIN
# wiring), this target builds its own copy of the same sources and the same
# QML module from the shared lists above, into a private OUTPUT_DIRECTORY so
# the two `Whatevr` modules cannot collide in one build tree.
# ---------------------------------------------------------------------------
if(WHATEVR_BUILD_TESTS)
    qt_add_executable(tst_chatbubbleperf
        MANUAL_FINALIZATION
        ${CMAKE_CURRENT_SOURCE_DIR}/../tests/tst_chatbubbleperf.cpp
        ${WHATKEVR_SOURCES}
    )

    target_include_directories(tst_chatbubbleperf PRIVATE ${WHATKEVR_INCLUDE_DIRS})

    qt_add_qml_module(tst_chatbubbleperf
        URI Whatevr
        VERSION 1.0
        NO_IMPORT_SCAN
        OUTPUT_DIRECTORY "${CMAKE_CURRENT_BINARY_DIR}/perf_qml/Whatevr"
        QML_FILES ${WHATKEVR_QML_FILES}
    )

    qt6_add_shaders(tst_chatbubbleperf "perf_roundedimage_shaders"
        PREFIX "/qt/qml/Whatevr"
        BASE "qml"
        FILES
            qml/shaders/roundedimage.frag
            qml/shaders/chatwallpaper.frag
    )

    qt_add_resources(tst_chatbubbleperf perf_emoji_metadata
        PREFIX /
        BASE ${PROJECT_SOURCE_DIR}
        FILES
            ${PROJECT_SOURCE_DIR}/data/emoji/emoji_17_0_ordering.json
            ${PROJECT_SOURCE_DIR}/data/emoji/emoji_keywords_en.json
            ${PROJECT_SOURCE_DIR}/data/icons/checkmark-bold.svg
            ${PROJECT_SOURCE_DIR}/data/wallpapers/doodle.svg
    )

    target_link_libraries(tst_chatbubbleperf PRIVATE
        ${WHATKEVR_LINK_LIBRARIES}
        Qt6::Test
    )

    add_test(NAME tst_chatbubbleperf COMMAND tst_chatbubbleperf)

    qt_finalize_executable(tst_chatbubbleperf)
endif()
