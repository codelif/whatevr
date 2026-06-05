cmake_minimum_required(VERSION 3.16)

project(whatkevr VERSION 0.1.0 LANGUAGES CXX)

# Without an explicit build type the compiler runs with no optimisation, which
# makes the QML/scene-graph paths noticeably slower. Default to an optimised
# build (with debug info) when the user has not chosen one.
if(NOT CMAKE_BUILD_TYPE AND NOT CMAKE_CONFIGURATION_TYPES)
    set(CMAKE_BUILD_TYPE RelWithDebInfo CACHE STRING "Build type" FORCE)
endif()

set(QT_MIN_VERSION "6.11.0")
set(KF_MIN_VERSION "6.25.0")

find_package(ECM ${KF_MIN_VERSION} REQUIRED NO_MODULE)
set(CMAKE_MODULE_PATH ${ECM_MODULE_PATH})

include(FeatureSummary)
include(KDECompilerSettings NO_POLICY_SCOPE)
include(KDEInstallDirs)
include(KDECMakeSettings)

set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)

find_package(Qt6 ${QT_MIN_VERSION} REQUIRED COMPONENTS Core Gui Network Protobuf Grpc Qml Quick QuickControls2 ShaderTools Widgets)
find_package(KF6 ${KF_MIN_VERSION} REQUIRED COMPONENTS CoreAddons I18n Kirigami Prison)
find_package(KF6KirigamiAddons REQUIRED)
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
    src/models/chatlistmodel.cpp
    src/models/chatlistmodel.h
    src/models/emojimodel.cpp
    src/models/emojimodel.h
    src/models/messagelistmodel.cpp
    src/models/messagelistmodel.h
    src/ui/rlottiestickeritem.cpp
    src/ui/rlottiestickeritem.h
    src/util/messagemarkup.cpp
    src/util/messagemarkup.h
    src/util/richtext.cpp
    src/util/richtext.h
)

set(WHATEVR_PROTO_FILE "${CMAKE_CURRENT_SOURCE_DIR}/../whatevrd/proto/whatevr.proto")
set(WHATEVR_PROTO_INCLUDE_DIR "${CMAKE_CURRENT_SOURCE_DIR}/../whatevrd/proto")

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

target_include_directories(whatkevr PRIVATE
    ${CMAKE_CURRENT_SOURCE_DIR}/src/app
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
        src/qml/components/ChatListDelegate.qml
        src/qml/components/ChatListPane.qml
        src/qml/components/ConversationPane.qml
        src/qml/components/HistorySyncStrip.qml
        src/qml/components/KineticWheelScroller.qml
        src/qml/components/DiscreetScrollBar.qml
        src/qml/components/ReplyPreview.qml
        src/qml/components/RoundedImage.qml
        src/qml/components/ChatBubble.qml
        src/qml/components/EmojiPicker.qml
        src/qml/components/MessageView.qml
        src/qml/components/MessageComposer.qml
        src/qml/pages/LoginPage.qml
        src/qml/pages/StatusPage.qml
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
        data/emoji/LICENSE.googlefonts-emoji-metadata
        data/icons/checkmark-bold.svg
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
    data/in.codelif.Whatevr.metainfo.xml
    DESTINATION ${KDE_INSTALL_METAINFODIR}
)

qt_finalize_executable(whatkevr)

feature_summary(WHAT ALL FATAL_ON_MISSING_REQUIRED_PACKAGES)
