set_source_files_properties(qml/I18n.qml PROPERTIES
    QT_QML_SINGLETON_TYPE TRUE
)

qt_add_executable(whatkevr
    MANUAL_FINALIZATION
    main.cpp
    app/appcontroller.cpp
    app/appcontroller.h
    app/protocolcontroller.cpp
    app/protocolcontroller.h
    app/protocolstickercontroller.cpp
    app/protocolstickercontroller.h
    app/settings.cpp
    app/settings.h
    app/stickercontroller.cpp
    app/stickercontroller.h
    models/chatlistmodel.cpp
    models/chatlistmodel.h
    models/chatlistfiltermodel.cpp
    models/chatlistfiltermodel.h
    models/emojimodel.cpp
    models/emojimodel.h
    models/messagelistmodel.cpp
    models/messagelistmodel.h
    models/protocolmessagemodel.cpp
    models/protocolmessagemodel.h
    models/starredmessagesmodel.cpp
    models/starredmessagesmodel.h
    models/pinnedmessagesmodel.cpp
    models/pinnedmessagesmodel.h
    models/protocolsearchmodel.cpp
    models/protocolsearchmodel.h
    models/searchresultsmodel.cpp
    models/searchresultsmodel.h
    models/stickermodel.cpp
    models/stickermodel.h
    models/stickerpackmodel.cpp
    models/stickerpackmodel.h
    protocol/protocolclient.cpp
    protocol/protocolclient.h
    protocol/collectionviewmodel.cpp
    protocol/collectionviewmodel.h
    protocol/objectviewmodel.cpp
    protocol/objectviewmodel.h
    ui/rlottiestickeritem.cpp
    ui/rlottiestickeritem.h
    util/messagemarkup.cpp
    util/messagemarkup.h
    util/messagerow.cpp
    util/messagerow.h
    util/richtext.cpp
    util/richtext.h
    util/tlds.cpp
    util/tlds.h
)

set(WHATEVR_PROTO_FILE "${PROJECT_SOURCE_DIR}/../proto/whatevr.proto")
set(WHATEVR_PROTO_INCLUDE_DIR "${PROJECT_SOURCE_DIR}/../proto")

qt_add_protobuf(whatkevr
    GENERATE_PACKAGE_SUBFOLDERS
    PROTO_FILES ${WHATEVR_PROTO_FILE}
    PROTO_INCLUDES ${WHATEVR_PROTO_INCLUDE_DIR}
)

qt_add_grpc(whatkevr CLIENT
    GENERATE_PACKAGE_SUBFOLDERS
    PROTO_FILES ${WHATEVR_PROTO_FILE}
    PROTO_INCLUDES ${WHATEVR_PROTO_INCLUDE_DIR}
)

set_target_properties(whatkevr PROPERTIES
    OUTPUT_NAME whatkevr
    RUNTIME_OUTPUT_DIRECTORY "${CMAKE_BINARY_DIR}/bin"
)

configure_file(version.h.in "${CMAKE_CURRENT_BINARY_DIR}/version.h" @ONLY)

target_include_directories(whatkevr PRIVATE
    ${CMAKE_CURRENT_BINARY_DIR}
    ${CMAKE_CURRENT_SOURCE_DIR}/app
    ${CMAKE_CURRENT_SOURCE_DIR}/models
    ${CMAKE_CURRENT_SOURCE_DIR}/protocol
    ${CMAKE_CURRENT_SOURCE_DIR}/ui
    ${CMAKE_CURRENT_SOURCE_DIR}/util
)

qt_add_qml_module(whatkevr
    URI Whatevr
    VERSION 1.0
    NO_IMPORT_SCAN
    QML_FILES
        qml/Main.qml
        qml/I18n.qml
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
    KF6::ColorScheme
    KF6::CoreAddons
    KF6::DBusAddons
    KF6::I18n
    KF6::Kirigami
    KF6::Prison
    PkgConfig::RLottie
)

install(TARGETS whatkevr RUNTIME DESTINATION ${KDE_INSTALL_BINDIR})

qt_finalize_executable(whatkevr)
