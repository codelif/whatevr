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
    ${CMAKE_CURRENT_SOURCE_DIR}/../src/protocol/protocolclient.cpp
    ${CMAKE_CURRENT_SOURCE_DIR}/../src/protocol/objectviewmodel.cpp
    ${CMAKE_CURRENT_SOURCE_DIR}/../src/protocol/collectionviewmodel.cpp
)

target_include_directories(tst_protocolcontroller PRIVATE
    ${CMAKE_CURRENT_SOURCE_DIR}/../src/app
    ${CMAKE_CURRENT_SOURCE_DIR}/../src/protocol
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
