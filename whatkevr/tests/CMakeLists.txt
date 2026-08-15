# Protocol-core unit tests. The tested sources depend only on Qt6::Core and
# Qt6::Network, so the test compiles them directly rather than dragging in the
# whole GUI target.
qt_add_executable(tst_protocolcore
    tst_protocolcore.cpp
    ${CMAKE_CURRENT_SOURCE_DIR}/../src/protocol/protocolclient.cpp
    ${CMAKE_CURRENT_SOURCE_DIR}/../src/protocol/collectionviewmodel.cpp
    ${CMAKE_CURRENT_SOURCE_DIR}/../src/protocol/objectviewmodel.cpp
)

target_include_directories(tst_protocolcore PRIVATE
    ${CMAKE_CURRENT_SOURCE_DIR}/../src/protocol
)

target_link_libraries(tst_protocolcore PRIVATE
    Qt6::Core
    Qt6::Network
    Qt6::Test
)

add_test(NAME tst_protocolcore COMMAND tst_protocolcore)

qt_add_executable(tst_protocolmessagemodel
    tst_protocolmessagemodel.cpp
    ${CMAKE_CURRENT_SOURCE_DIR}/../src/models/protocolmessagemodel.cpp
    ${CMAKE_CURRENT_SOURCE_DIR}/../src/protocol/collectionviewmodel.cpp
    ${CMAKE_CURRENT_SOURCE_DIR}/../src/util/messagemarkup.cpp
    ${CMAKE_CURRENT_SOURCE_DIR}/../src/util/richtext.cpp
    ${CMAKE_CURRENT_SOURCE_DIR}/../src/util/tlds.cpp
)

target_include_directories(tst_protocolmessagemodel PRIVATE
    ${CMAKE_CURRENT_SOURCE_DIR}/../src/models
    ${CMAKE_CURRENT_SOURCE_DIR}/../src/protocol
    ${CMAKE_CURRENT_SOURCE_DIR}/../src/util
)

target_link_libraries(tst_protocolmessagemodel PRIVATE
    Qt6::Core
    Qt6::Gui
    Qt6::Network
    Qt6::Test
    KF6::I18n
)

add_test(NAME tst_protocolmessagemodel COMMAND tst_protocolmessagemodel)

# ProtocolController tests (D2a). The controller derives the status/login gate
# from the connection/login object views, so this target pulls in the controller
# plus the protocol client + object view model, and the libraries the controller
# touches (Gui for the clipboard seam, Qml for the QML singleton macros, I18n for
# the user-facing strings). Runs headless (QTEST_GUILESS_MAIN).
qt_add_executable(tst_protocolcontroller
    tst_protocolcontroller.cpp
    ${CMAKE_CURRENT_SOURCE_DIR}/../src/app/protocolcontroller.cpp
    ${CMAKE_CURRENT_SOURCE_DIR}/../src/app/protocolcontroller.h
    ${CMAKE_CURRENT_SOURCE_DIR}/../src/app/protocolstickercontroller.cpp
    ${CMAKE_CURRENT_SOURCE_DIR}/../src/app/protocolstickercontroller.h
    ${CMAKE_CURRENT_SOURCE_DIR}/../src/protocol/protocolclient.cpp
    ${CMAKE_CURRENT_SOURCE_DIR}/../src/protocol/objectviewmodel.cpp
    ${CMAKE_CURRENT_SOURCE_DIR}/../src/protocol/collectionviewmodel.cpp
    ${CMAKE_CURRENT_SOURCE_DIR}/../src/models/protocolmessagemodel.cpp
    ${CMAKE_CURRENT_SOURCE_DIR}/../src/models/protocolsearchmodel.cpp
    ${CMAKE_CURRENT_SOURCE_DIR}/../src/models/emojimodel.cpp
    ${CMAKE_CURRENT_SOURCE_DIR}/../src/util/messagemarkup.cpp
    ${CMAKE_CURRENT_SOURCE_DIR}/../src/util/messagerow.cpp
    ${CMAKE_CURRENT_SOURCE_DIR}/../src/util/richtext.cpp
    ${CMAKE_CURRENT_SOURCE_DIR}/../src/util/tlds.cpp
)

target_include_directories(tst_protocolcontroller PRIVATE
    ${CMAKE_CURRENT_SOURCE_DIR}/../src/app
    ${CMAKE_CURRENT_SOURCE_DIR}/../src/models
    ${CMAKE_CURRENT_SOURCE_DIR}/../src/protocol
    ${CMAKE_CURRENT_SOURCE_DIR}/../src/util
)

target_link_libraries(tst_protocolcontroller PRIVATE
    Qt6::Core
    Qt6::Gui
    Qt6::Network
    Qt6::Qml
    Qt6::Test
    KF6::I18n
)

add_test(NAME tst_protocolcontroller COMMAND tst_protocolcontroller)

# Playback tests. They link libmpv directly (plus Settings, which AudioPlayer
# consults for the resume/speed preferences) and drive a real decoder against a
# generated WAV, which is the only way to catch an engine that silently fails to
# start. Runs headless with mpv's null audio output.
qt_add_executable(tst_mediaplayback
    tst_mediaplayback.cpp
    ${CMAKE_CURRENT_SOURCE_DIR}/../src/media/audioplayer.cpp
    ${CMAKE_CURRENT_SOURCE_DIR}/../src/media/audioplayer.h
    ${CMAKE_CURRENT_SOURCE_DIR}/../src/media/mpvcore.cpp
    ${CMAKE_CURRENT_SOURCE_DIR}/../src/media/mpvcore.h
    ${CMAKE_CURRENT_SOURCE_DIR}/../src/app/settings.cpp
    ${CMAKE_CURRENT_SOURCE_DIR}/../src/app/settings.h
)

target_include_directories(tst_mediaplayback PRIVATE
    ${CMAKE_CURRENT_SOURCE_DIR}/../src
    ${CMAKE_CURRENT_SOURCE_DIR}/../src/app
    ${CMAKE_CURRENT_SOURCE_DIR}/../src/media
)

target_link_libraries(tst_mediaplayback PRIVATE
    Qt6::Core
    Qt6::Gui
    Qt6::Qml
    Qt6::Test
    KF6::ColorScheme
    KF6::I18n
    PkgConfig::Mpv
)

add_test(NAME tst_mediaplayback COMMAND tst_mediaplayback)

# Who is allowed to be playing, and the frame each claimant left behind. Pure
# bookkeeping over plain QObjects: no window, no decoder and no libmpv, which is
# the point of the arbiter living above the engine seam rather than inside the
# mpv pool. Multimedia is here only for QVideoFrame, the type a still arrives
# as; nothing in this target opens a player.
qt_add_executable(tst_videoarbiter
    tst_videoarbiter.cpp
    ${CMAKE_CURRENT_SOURCE_DIR}/../src/media/videoarbiter.cpp
    ${CMAKE_CURRENT_SOURCE_DIR}/../src/media/videoarbiter.h
    ${CMAKE_CURRENT_SOURCE_DIR}/../src/app/settings.cpp
    ${CMAKE_CURRENT_SOURCE_DIR}/../src/app/settings.h
)

target_include_directories(tst_videoarbiter PRIVATE
    ${CMAKE_CURRENT_SOURCE_DIR}/../src
    ${CMAKE_CURRENT_SOURCE_DIR}/../src/app
    ${CMAKE_CURRENT_SOURCE_DIR}/../src/media
)

target_link_libraries(tst_videoarbiter PRIVATE
    Qt6::Core
    Qt6::Gui
    Qt6::Multimedia
    Qt6::Qml
    Qt6::Test
    KF6::ColorScheme
    KF6::I18n
)

add_test(NAME tst_videoarbiter COMMAND tst_videoarbiter)

# Waveform geometry. Pure scene-graph item, so it needs only Gui and Qml.
qt_add_executable(tst_waveformitem
    tst_waveformitem.cpp
    ${CMAKE_CURRENT_SOURCE_DIR}/../src/ui/waveformitem.cpp
    ${CMAKE_CURRENT_SOURCE_DIR}/../src/ui/waveformitem.h
)

target_include_directories(tst_waveformitem PRIVATE
    ${CMAKE_CURRENT_SOURCE_DIR}/../src/ui
)

target_link_libraries(tst_waveformitem PRIVATE
    Qt6::Core
    Qt6::Gui
    Qt6::Qml
    Qt6::Quick
    Qt6::Test
)

add_test(NAME tst_waveformitem COMMAND tst_waveformitem)
